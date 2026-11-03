package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/store"
	"chatisp/internal/cannedreplies"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db/dbgen"
	"chatisp/internal/sectors"
)

const purposeService = "atendimento"

// PolicyDenied is a local refusal. The attempt is not handed to the network.
type PolicyDenied struct{ Reason string }

func (e *PolicyDenied) Error() string { return ErrPolicyDenied.Error() }
func (e *PolicyDenied) Unwrap() error { return ErrPolicyDenied }

type messageContext struct {
	Mode       string `json:"mode"`
	Kind       string `json:"kind"`
	Purpose    string `json:"purpose"`
	TemplateID string `json:"template_id,omitempty"`
	Language   string `json:"language,omitempty"`
}

// TemplateSendInput starts one proactive template. ClientRequestID is scoped
// by OpSendTemplate, not by freeform text.
type TemplateSendInput struct {
	InboxID         uuid.UUID
	ConversationID  uuid.UUID
	ExpectedVersion int64
	ClientRequestID string
	TemplateID      string
	Language        string
	Purpose         string
	At              time.Time
}

func (s Service) policy() channelpolicy.Service {
	return channelpolicy.Service{DB: s.DB}
}

func acceptanceUUID(p Principal, operation, requestID string) uuid.UUID {
	sum := sha256.Sum256([]byte(p.TenantID.String() + "\x00" + p.MembershipID.String() + "\x00" + operation + "\x00" + requestID))
	var id uuid.UUID
	copy(id[:], sum[:16])
	return id
}

func mapSignature(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cannedreplies.ErrConflict):
		return store.ErrIdempotencyConflict
	case errors.Is(err, cannedreplies.ErrInvalid):
		return store.ErrInvalid
	case errors.Is(err, cannedreplies.ErrForbidden), errors.Is(err, sectors.ErrForbidden):
		return ErrForbidden
	default:
		return err
	}
}

func mapDecision(d channelpolicy.Decision, err error) error {
	if err != nil {
		if errors.Is(err, sectors.ErrForbidden) || errors.Is(err, channelpolicy.ErrInvalid) {
			if errors.Is(err, channelpolicy.ErrInvalid) {
				return store.ErrInvalid
			}
			return ErrForbidden
		}
		return err
	}
	if d.Allowed {
		return nil
	}
	switch d.Reason {
	case "channel_unavailable", "web_capability_requires_driver_policy":
		return ErrChannelUnavailable
	case "contact_blocked":
		return ErrForbidden
	default:
		return &PolicyDenied{Reason: d.Reason}
	}
}

type publicIntent struct {
	InboxID         uuid.UUID
	ConversationID  uuid.UUID
	ExpectedVersion int64
	ClientRequestID string
	Body            string
	At              time.Time
	Operation       string
	Mode            string
	Kind            string
	Purpose         string
	TemplateID      string
	Language        string
}

func (s Service) enqueuePublic(ctx context.Context, p Principal, in publicIntent) (SendResult, error) {
	var out SendResult
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizePrincipal(ctx, tx, p); err != nil {
			return err
		}
		body := stringsTrimPolicy(in.Body)
		requestID := stringsTrimPolicy(in.ClientRequestID)
		if body == "" || requestID == "" || in.At.IsZero() || in.ExpectedVersion < 1 || in.Purpose == "" {
			return store.ErrInvalid
		}
		if in.Kind == "template" && (in.Mode != "proactive" || in.TemplateID == "") {
			return store.ErrInvalid
		}
		replay, err := queuedOperation(ctx, queries(tx), p, in.Operation, requestID)
		if err != nil {
			return err
		}
		if !replay && in.Kind == "template" && !validTemplateLanguage(in.Language) {
			return store.ErrInvalid
		}
		var contactID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT contact_id FROM conversations
			 WHERE tenant_id = $1 AND id = $2 AND inbox_id = $3`, p.TenantID, in.ConversationID, in.InboxID).Scan(&contactID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		var decision channelpolicy.Decision
		if !replay {
			decision, err = s.admit(ctx, tx, p, contactID, in.InboxID, in.ConversationID, in.Mode, in.Kind, in.Purpose, in.TemplateID, in.Language)
			if err != nil {
				return err
			}
		}
		stored := body
		if in.Kind != "template" {
			if replay {
				stored, err = s.storedHumanBody(ctx, tx, p, in.Operation, requestID, body)
			} else {
				stored, err = s.signHuman(ctx, tx, p, in.Operation, requestID, body)
			}
			if err != nil {
				return err
			}
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
		if err != nil {
			return err
		}
		if !replay && in.Mode == "proactive" {
			decision, err = s.admit(ctx, tx, p, contactID, in.InboxID, in.ConversationID, in.Mode, in.Kind, in.Purpose, in.TemplateID, in.Language)
			if err != nil {
				return err
			}
		}
		if conv.InboxID != in.InboxID {
			return store.ErrNotFound
		}
		connector, err := queries(tx).GetInboxConnector(ctx, dbgen.GetInboxConnectorParams{TenantID: p.TenantID, ID: conv.InboxID})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		var payload []byte
		if in.Kind == "template" {
			payload, err = canonicalTemplate(conv.ID.String(), connector.String(), stored, in.Mode, in.Purpose, in.Language, in.TemplateID)
		} else {
			payload, err = canonicalSend(conv.ID.String(), connector.String(), stored)
		}
		if err != nil {
			return err
		}
		if !replay {
			if conv.Version != in.ExpectedVersion {
				return store.ErrStaleVersion
			}
			if conv.Status == store.StatusClosed {
				return store.ErrClosed
			}
			if conv.Controller != store.ControllerHuman || conv.AssigneeID != p.MembershipID {
				return ErrForbidden
			}
		}
		sent, err := s.Repo.EnqueueText(ctx, tx, store.OutboundInput{
			TenantID: p.TenantID, ConversationID: conv.ID, ConnectorID: connector, PrincipalID: p.MembershipID,
			Operation: in.Operation, ClientRequestID: requestID, Payload: payload, Body: stored, At: in.At,
		})
		if err != nil {
			return err
		}
		out.OutboundResult = sent
		if sent.Replayed {
			return nil
		}
		columnKind := "text"
		if in.Kind == "template" {
			columnKind = "template"
		}
		if err := s.stampMessage(ctx, tx, p.TenantID, sent.MessageID, columnKind, messageContext{
			Mode: in.Mode, Kind: in.Kind, Purpose: in.Purpose, TemplateID: in.TemplateID, Language: in.Language,
		}); err != nil {
			return err
		}
		if err := s.rememberDecision(ctx, tx, p.TenantID, sent.AttemptID, "enqueue", 0, decision, in.Mode, in.Kind, in.Purpose, in.TemplateID); err != nil {
			return err
		}
		if in.Mode == "proactive" {
			if err := s.policy().RegisterPendingTx(ctx, tx, sectorPrincipal(p), channelpolicy.Scope{
				InboxID: in.InboxID, ContactID: contactID, Purpose: in.Purpose,
			}, sent.AttemptID); err != nil {
				return err
			}
		}
		id, err := enqueueJob(ctx, s.Jobs, tx, p.TenantID, sent.AttemptID)
		if err != nil {
			return err
		}
		out.JobID = id
		return nil
	})
	return out, err
}

func queuedOperation(ctx context.Context, q *dbgen.Queries, p Principal, operation, requestID string) (bool, error) {
	_, err := q.GetIdempotency(ctx, dbgen.GetIdempotencyParams{
		TenantID: p.TenantID, PrincipalID: p.MembershipID, Operation: operation, ClientRequestID: requestID,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func stringsTrimPolicy(v string) string { return strings.TrimSpace(v) }

func (s Service) admit(ctx context.Context, tx pgx.Tx, p Principal, contact, inbox, conversation uuid.UUID, mode, kind, purpose, templateID, language string) (channelpolicy.Decision, error) {
	decision, err := s.policy().EvaluateDispatchTx(ctx, tx, sectorPrincipal(p), channelpolicy.Dispatch{
		Scope:          channelpolicy.Scope{InboxID: inbox, ContactID: contact, Purpose: purpose},
		ConversationID: conversation,
		Mode:           mode,
		Kind:           kind,
		TemplateID:     templateID,
	})
	if err != nil || !decision.Allowed || kind != "template" {
		return decision, mapDecision(decision, err)
	}
	if !validTemplateLanguage(language) {
		return decision, store.ErrInvalid
	}
	matched, err := templateLanguageAuthorized(ctx, tx, p.TenantID, inbox, templateID, language)
	if err != nil {
		return decision, err
	}
	if !matched {
		decision.Allowed = false
		decision.Reason = "template_not_authorized"
	}
	return decision, mapDecision(decision, nil)
}

func validTemplateLanguage(value string) bool {
	if value == "" || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func templateLanguageAuthorized(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID, templateID, language string) (bool, error) {
	var matched bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM inboxes AS inbox
			  JOIN meta_phone_numbers AS number
			    ON number.tenant_id = inbox.tenant_id
			   AND number.inbox_id = inbox.id
			   AND number.connector_id = inbox.connector_id
			   AND number.status = 'active'
			  JOIN meta_wabas AS waba
			    ON waba.tenant_id = number.tenant_id
			   AND waba.id = number.meta_waba_id
			   AND waba.status = 'active'
			  JOIN wa_templates AS template
			    ON template.tenant_id = waba.tenant_id
			   AND template.waba_id = waba.id
			   AND template.external_id = $3
			 WHERE inbox.tenant_id = $1
			   AND inbox.id = $2
			   AND template.state = 'APPROVED'
			   AND template.sync_source = 'driver'
			   AND template.language = $4
		)`, tenant, inbox, templateID, language).Scan(&matched)
	return matched, err
}

func (s Service) signHuman(ctx context.Context, tx pgx.Tx, p Principal, operation, requestID, body string) (string, error) {
	signed, err := cannedreplies.AcceptPublicHumanTx(ctx, tx, auth.Principal{
		UserID: p.UserID, Email: p.Email, TenantID: p.TenantID, MembershipID: p.MembershipID, Role: p.Role,
	}, acceptanceUUID(p, operation, requestID), body)
	return signed, mapSignature(err)
}

// storedHumanBody resolves a key accepted before this call.
// No receipt means the previous contract stored the original text: the current
// signature settings are not applied and no receipt is inserted. An existing
// receipt returns the text accepted then, or conflicts when the payload changed.
func (s Service) storedHumanBody(ctx context.Context, tx pgx.Tx, p Principal, operation, requestID, body string) (string, error) {
	var actor uuid.UUID
	var original, accepted string
	err := tx.QueryRow(ctx, `
		SELECT membership_id, original_text, accepted_text
		  FROM attendant_signature_receipts
		 WHERE tenant_id = $1 AND acceptance_id = $2`, p.TenantID, acceptanceUUID(p, operation, requestID)).Scan(&actor, &original, &accepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return body, nil
	}
	if err != nil {
		return "", err
	}
	if actor != p.MembershipID || original != body {
		return "", store.ErrIdempotencyConflict
	}
	return accepted, nil
}

func (s Service) rememberDecision(ctx context.Context, tx pgx.Tx, tenant, attempt uuid.UUID, phase string, lease int64, d channelpolicy.Decision, mode, kind, purpose, templateID string) error {
	var leaseValue any
	if lease > 0 {
		leaseValue = lease
	}
	if d.EvaluatedAt.IsZero() {
		d.EvaluatedAt = time.Now().UTC()
	}
	if d.Reason == "" {
		d.Reason = "invalid_request"
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO dispatch_policy_decisions (
			tenant_id, id, attempt_id, phase, lease_version, allowed, reason, driver,
			official_window, evaluated_at, window_ends_at, mode, kind, purpose, template_id
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		)`, tenant, uuid.New(), attempt, phase, leaseValue, d.Allowed, d.Reason, d.Driver,
		d.OfficialWindow, d.EvaluatedAt, d.WindowEndsAt, mode, kind, purpose, templateID)
	return err
}

func (s Service) stampMessage(ctx context.Context, tx pgx.Tx, tenant, message uuid.UUID, kind string, doc messageContext) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE messages
		   SET content_kind = $3, content_context = $4::jsonb
		 WHERE tenant_id = $1
		   AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND id = $2`, tenant, message, kind, raw)
	return err
}

func loadAttemptContext(ctx context.Context, tx pgx.Tx, tenant, attempt uuid.UUID) (state, origin, kind, mode, purpose, templateID, language string, contact, inbox, conversation uuid.UUID, author *uuid.UUID, err error) {
	var raw []byte
	err = tx.QueryRow(ctx, `
		SELECT o.state, o.origin, c.contact_id, c.inbox_id, c.id, m.content_kind, m.content_context, m.author_membership_id
		  FROM outbound_attempts o
		  JOIN conversations c ON c.tenant_id = o.tenant_id AND c.id = o.conversation_id
		  JOIN messages m ON m.tenant_id = o.tenant_id AND m.id = o.message_id
		 WHERE o.tenant_id = $1
		   AND o.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND o.id = $2`, tenant, attempt).Scan(&state, &origin, &contact, &inbox, &conversation, &kind, &raw, &author)
	if err != nil {
		return
	}
	var doc messageContext
	_ = json.Unmarshal(raw, &doc)
	mode = doc.Mode
	purpose = doc.Purpose
	templateID = doc.TemplateID
	language = doc.Language
	if mode == "" {
		mode = "requested"
	}
	if purpose == "" {
		purpose = purposeService
	}
	if kind == "" {
		kind = "text"
	}
	if kind == "template" && doc.Kind == "" {
		doc.Kind = "template"
	}
	if doc.Kind == "template" {
		kind = "template"
	} else if kind != "template" {
		kind = "freeform"
	}
	return
}

// FenceDecision is the policy photograph taken under the dispatch fence.
// Code is empty when the stored intent may still be handed to the driver.
// Kind is "template" or "freeform". The caller commits a denial before any
// network call and does not keep this transaction open across that call.
type FenceDecision struct {
	Code       string
	Message    string
	Kind       string
	TemplateID string
	Language   string
}

// RevalidateDispatchFence re-reads window, consent and template approval for
// an attempt the caller already locked. It persists a dispatch observation
// with the lease and does not change the attempt. Uncertain and finished
// attempts stay untouched. It does not lock the contact or the membership:
// the fence already ordered those locks.
func (s Service) RevalidateDispatchFence(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID, leaseVersion int64) (FenceDecision, error) {
	if leaseVersion < 1 || attemptID == uuid.Nil {
		return FenceDecision{}, store.ErrInvalid
	}
	code, message, err := s.EvaluateAttemptPolicy(ctx, tx, tenantID, attemptID, leaseVersion)
	if err != nil {
		return FenceDecision{}, err
	}
	_, _, kind, _, _, templateID, language, _, _, _, _, err := loadAttemptContext(ctx, tx, tenantID, attemptID)
	if err != nil {
		return FenceDecision{}, err
	}
	return FenceDecision{Code: code, Message: message, Kind: kind, TemplateID: templateID, Language: language}, nil
}

// EvaluateAttemptPolicy rechecks the stored intent under the caller's lease.
// Possession stays on AuthorAllowed so this read does not take the membership
// lock ahead of that query. It also does not lock the contact: the caller may
// already hold the conversation. Uncertain and finished attempts stay put.
// No network I/O happens here.
func (s Service) EvaluateAttemptPolicy(ctx context.Context, tx pgx.Tx, tenantID, attemptID uuid.UUID, leaseVersion int64) (string, string, error) {
	state, _, kind, mode, purpose, templateID, language, contact, inbox, conversation, _, err := loadAttemptContext(ctx, tx, tenantID, attemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", store.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	switch state {
	case "uncertain", "accepted", "failed", "cancelled":
		return "", "", nil
	}
	if mode == "proactive" || kind == "template" {
		return s.evaluateProactive(ctx, tx, tenantID, attemptID, leaseVersion, contact, inbox, mode, kind, purpose, templateID, language)
	}
	open, reason, evaluated, ends, driver, official, werr := windowSnapshot(ctx, tx, tenantID, contact, inbox, conversation)
	if werr != nil {
		return "", "", werr
	}
	decision := channelpolicy.Decision{Allowed: open, Reason: reason, Driver: driver, OfficialWindow: official, EvaluatedAt: evaluated, WindowEndsAt: ends}
	if err := s.rememberDecision(ctx, tx, tenantID, attemptID, "dispatch", leaseVersion, decision, mode, "freeform", purpose, ""); err != nil {
		return "", "", err
	}
	if open {
		return "", "", nil
	}
	return denyCode(reason)
}

func (s Service) evaluateProactive(ctx context.Context, tx pgx.Tx, tenant, attempt uuid.UUID, lease int64, contact, inbox uuid.UUID, mode, kind, purpose, templateID, language string) (string, string, error) {
	var evaluated time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&evaluated); err != nil {
		return "", "", err
	}
	var waba *uuid.UUID
	var driver, status string
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT cc.connector_type, cc.enabled, cc.status, w.id
		  FROM inboxes i
		  JOIN channel_connectors cc ON cc.tenant_id = i.tenant_id AND cc.id = i.connector_id
		  LEFT JOIN meta_phone_numbers n ON n.tenant_id = i.tenant_id AND n.inbox_id = i.id AND n.connector_id = cc.id AND n.status = 'active'
		  LEFT JOIN meta_wabas w ON w.tenant_id = n.tenant_id AND w.id = n.meta_waba_id AND w.status = 'active'
		 WHERE i.tenant_id = $1 AND i.id = $2`, tenant, inbox).Scan(&driver, &enabled, &status, &waba)
	if err != nil {
		return "", "", err
	}
	decision := channelpolicy.Decision{EvaluatedAt: evaluated, Driver: driver, OfficialWindow: driver == "meta_cloud"}
	if !enabled || status != "connected" || driver != "meta_cloud" || waba == nil {
		decision.Reason = "channel_unavailable"
	} else {
		var granted bool
		err = tx.QueryRow(ctx, `
			SELECT COALESCE((
				SELECT revoked_at IS NULL AND granted_at <= $5
				  FROM contact_consents
				 WHERE tenant_id = $1 AND contact_id = $2 AND inbox_id = $3 AND purpose = $4
				 ORDER BY sequence DESC LIMIT 1
			), false)`, tenant, contact, inbox, purpose, evaluated).Scan(&granted)
		if err != nil {
			return "", "", err
		}
		if !granted {
			decision.Reason = "consent_required"
		} else if kind == "template" {
			if !validTemplateLanguage(language) {
				decision.Reason = "invalid_request"
			} else {
				var approved bool
				err = tx.QueryRow(ctx, `
				SELECT state = 'APPROVED' AND sync_source = 'driver' AND language = $4
				  FROM wa_templates
				 WHERE tenant_id = $1 AND waba_id = $2 AND external_id = $3
				 FOR SHARE`, tenant, *waba, templateID, language).Scan(&approved)
				if errors.Is(err, pgx.ErrNoRows) {
					approved = false
					err = nil
				}
				if err != nil {
					return "", "", err
				}
				if !approved {
					decision.Reason = "template_not_authorized"
				} else {
					decision.Allowed = true
					decision.Reason = "approved_template"
				}
			}
		} else {
			decision.Allowed = true
			decision.Reason = "consent_granted"
		}
	}
	if err := s.rememberDecision(ctx, tx, tenant, attempt, "dispatch", lease, decision, mode, policyKind(kind), purpose, templateID); err != nil {
		return "", "", err
	}
	if decision.Allowed {
		return "", "", nil
	}
	return denyCode(decision.Reason)
}

func policyKind(kind string) string {
	if kind == "template" {
		return "template"
	}
	return "freeform"
}

func denyCode(reason string) (string, string, error) {
	switch reason {
	case "consent_required":
		return "consent_required", "consentimento necessário antes do despacho", nil
	case "template_not_authorized":
		return "template_not_authorized", "template não autorizado antes do despacho", nil
	case "contact_blocked":
		return "contact_blocked", "contato bloqueado antes do despacho", nil
	case "channel_unavailable", "web_capability_requires_driver_policy":
		return "channel_unavailable", "canal indisponível antes do despacho", nil
	case "permissao_revogada":
		return "permissao_revogada", "acesso revogado antes do despacho", nil
	case "invalid_request":
		return "invalid_request", "intenção de envio inválida antes do despacho", nil
	default:
		return "window_closed", "janela oficial fechada antes do despacho", nil
	}
}

func windowSnapshot(ctx context.Context, tx pgx.Tx, tenant, contact, inbox, conversation uuid.UUID) (bool, string, time.Time, *time.Time, string, bool, error) {
	var evaluated time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&evaluated); err != nil {
		return false, "", time.Time{}, nil, "", false, err
	}
	var driver, status string
	var enabled bool
	var connector, identity uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT cc.connector_type, cc.enabled, cc.status, cc.id, c.contact_identity_id
		  FROM conversations c
		  JOIN inboxes i ON i.tenant_id = c.tenant_id AND i.id = c.inbox_id
		  JOIN channel_connectors cc ON cc.tenant_id = i.tenant_id AND cc.id = i.connector_id
		 WHERE c.tenant_id = $1 AND c.id = $2 AND c.inbox_id = $3 AND c.contact_id = $4`,
		tenant, conversation, inbox, contact).Scan(&driver, &enabled, &status, &connector, &identity)
	if err != nil {
		return false, "", time.Time{}, nil, "", false, err
	}
	if !enabled || status != "connected" || driver != "meta_cloud" {
		return false, "channel_unavailable", evaluated, nil, driver, driver == "meta_cloud", nil
	}
	var last *time.Time
	err = tx.QueryRow(ctx, `
		SELECT max(m.trusted_at)
		  FROM messages m
		  JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
		 WHERE m.tenant_id = $1 AND c.contact_id = $2 AND c.inbox_id = $3
		   AND m.connector_id = $4 AND c.contact_identity_id = $6
		   AND m.direction = 'inbound' AND m.origin = 'customer' AND m.visibility = 'public'
		   AND NOT m.historical AND m.local_state = 'received' AND m.trusted_at <= $5`,
		tenant, contact, inbox, connector, evaluated, identity).Scan(&last)
	if err != nil {
		return false, "", time.Time{}, nil, "", false, err
	}
	if last == nil {
		return false, "window_closed", evaluated, nil, driver, true, nil
	}
	end := last.Add(24 * time.Hour)
	if evaluated.Before(end) {
		return true, "window_open", evaluated, &end, driver, true, nil
	}
	return false, "window_closed", evaluated, &end, driver, true, nil
}

func (s Service) refuseQueued(ctx context.Context, tx pgx.Tx, tenant, attempt uuid.UUID, state, code, message string) (string, error) {
	to := storeStateCancelled
	if state == "retrying" {
		to = "failed"
	}
	if state != "queued" && state != "retrying" {
		return state, nil
	}
	return s.Repo.TransitionAttempt(ctx, tx, store.AttemptTransition{
		TenantID: tenant, AttemptID: attempt, To: to, ErrorCode: code, ErrorMessage: message,
	})
}
