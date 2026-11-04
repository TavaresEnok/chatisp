package erp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// InsertConnection stores the single fake connection of the tenant.
func InsertConnection(ctx context.Context, tx pgx.Tx, conn Connection) error {
	if tx == nil || conn.TenantID == uuid.Nil || conn.ID == uuid.Nil || conn.Driver != DriverFake || !conn.Simulated {
		return ErrInvalid
	}
	if conn.Availability == "" {
		conn.Availability = AvailabilityUp
	}
	names := make([]string, 0, len(conn.Capabilities))
	for name, on := range conn.Capabilities {
		if on {
			if !knownCapability(name) {
				return ErrInvalid
			}
			names = append(names, name)
		}
	}
	var sector any
	if conn.SectorID != uuid.Nil {
		sector = conn.SectorID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_connections (tenant_id, id, driver, simulated, enabled, availability, sector_id, capabilities)
		VALUES ($1, $2, 'fake', true, $3, $4, $5, $6)`,
		conn.TenantID, conn.ID, conn.Enabled, conn.Availability, sector, names)
	return mapPG(err)
}

// LoadConnection reads the tenant connection under the current row security scope.
func LoadConnection(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Connection, error) {
	var conn Connection
	var names []string
	var sector *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT tenant_id, id, driver, simulated, enabled, availability, sector_id, capabilities
		FROM erp_connections
		WHERE tenant_id = $1`, tenant).Scan(
		&conn.TenantID, &conn.ID, &conn.Driver, &conn.Simulated, &conn.Enabled, &conn.Availability, &sector, &names)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, mapPG(err)
	}
	if sector != nil {
		conn.SectorID = *sector
	}
	conn.Capabilities = map[string]bool{}
	for _, name := range names {
		conn.Capabilities[name] = true
	}
	return conn, nil
}

// CommitAction inserts the action once. The same key with another hash conflicts.
// The same key and hash is a replay and leaves the transaction usable.
func CommitAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	_, _, err := writeAction(ctx, tx, tenant, action)
	return err
}

// writeAction inserts the action. replayed is true when the stored row already
// has this hash. A different hash returns ErrConflict without aborting the transaction.
func writeAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) (Action, bool, error) {
	if tx == nil || tenant == uuid.Nil || action.ID == uuid.Nil || action.LocalKey == "" || action.ConnectionID == uuid.Nil {
		return Action{}, false, ErrInvalid
	}
	if action.Outcome != OutcomeOK && action.Outcome != OutcomeUncertain {
		return action, false, nil
	}
	var conversation any
	if action.Conversation != uuid.Nil {
		conversation = action.Conversation
	}
	var ref any
	if action.ExternalRef != "" {
		ref = action.ExternalRef
	}
	if action.ReasonCode == "" {
		action.ReasonCode = reasonCodeOf(action)
	}
	raw, err := encodeSnapshot(action.View)
	if err != nil {
		return Action{}, false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO erp_actions (
			tenant_id, id, connection_id, conversation_id, capability, local_key, request_hash, effect, outcome, external_ref,
			reason, result, client_request_id, outbox_operation, principal_id, message_id, attempt_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (tenant_id, local_key) DO NOTHING`,
		tenant, action.ID, action.ConnectionID, conversation, action.Capability, action.LocalKey, action.Hash[:], action.Effect, action.Outcome, ref,
		nullText(action.ReasonCode), string(raw), nullText(action.ClientRequestID), nullText(action.OutboxOperation),
		uuidOrNil(action.PrincipalID), uuidOrNil(action.MessageID), uuidOrNil(action.AttemptID))
	if err != nil {
		return Action{}, false, mapPG(err)
	}
	if tag.RowsAffected() == 0 {
		prior, loadErr := LoadActionByKey(ctx, tx, tenant, action.LocalKey)
		if loadErr != nil {
			return Action{}, false, loadErr
		}
		if prior.Hash != action.Hash {
			return prior, false, ErrConflict
		}
		return prior, true, nil
	}
	if action.Conversation != uuid.Nil {
		if err = insertTimeline(ctx, tx, tenant, action); err != nil {
			return Action{}, false, err
		}
	}
	if err = insertAudit(ctx, tx, tenant, action); err != nil {
		return Action{}, false, err
	}
	return action, false, nil
}

func insertTimeline(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO conversation_events (
			tenant_id, id, conversation_id, cycle_number, kind, actor_id, at, version, controller_epoch, controller, assignee_id
		)
		SELECT tenant_id, $3, id, cycle_number, $5, $4, now(), version, controller_epoch, controller, assignee_id
		FROM conversations
		WHERE tenant_id = $1 AND id = $2`, tenant, action.Conversation, action.ID, uuidOrNil(action.ActorID), eventKind(action))
	if err != nil {
		return mapPG(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, tenant_id, membership_id, actor_user_id, action, target_type, target_id, result, correlation_id)
		VALUES ($1, $2, $3, $4, $5, 'erp_action', $6, $7, $8)`,
		uuid.New(), tenant, uuidOrNil(action.MembershipID), uuidOrNil(action.ActorID), "erp."+action.Capability, action.ID, auditResult(action), nullText(action.LocalKey))
	return mapPG(err)
}

func uuidOrNil(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// RecordFailure writes the timeline and the audit row for an outcome that is not stored as a successful action.
func RecordFailure(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action Action) error {
	if tx == nil || tenant == uuid.Nil || action.Conversation == uuid.Nil || action.Capability == "" || action.Outcome == "" {
		return ErrInvalid
	}
	if action.ID == uuid.Nil {
		action.ID = uuid.New()
	}
	if err := insertTimeline(ctx, tx, tenant, action); err != nil {
		return err
	}
	return insertAudit(ctx, tx, tenant, action)
}

// LoadActionByKey reads the stored action for a local key.
func LoadActionByKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, localKey string) (Action, error) {
	var action Action
	var hash []byte
	var ref, reason, requestID, operation *string
	var result []byte
	var conversation, principal, message, attempt *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, connection_id, conversation_id, capability, local_key, request_hash, effect, outcome, external_ref,
		       reason, result, client_request_id, outbox_operation, principal_id, message_id, attempt_id
		FROM erp_actions
		WHERE tenant_id = $1 AND local_key = $2`, tenant, localKey).Scan(
		&action.ID, &action.ConnectionID, &conversation, &action.Capability, &action.LocalKey, &hash, &action.Effect, &action.Outcome, &ref,
		&reason, &result, &requestID, &operation, &principal, &message, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, mapPG(err)
	}
	if conversation != nil {
		action.Conversation = *conversation
	}
	if ref != nil {
		action.ExternalRef = *ref
	}
	if reason != nil {
		action.ReasonCode = *reason
	}
	if requestID != nil {
		action.ClientRequestID = *requestID
	}
	if operation != nil {
		action.OutboxOperation = *operation
	}
	if principal != nil {
		action.PrincipalID = *principal
	}
	if message != nil {
		action.MessageID = *message
	}
	if attempt != nil {
		action.AttemptID = *attempt
	}
	if len(hash) != sha256Len {
		return Action{}, ErrInvalid
	}
	copy(action.Hash[:], hash)
	if len(result) > 0 {
		view, err := decodeSnapshot(result)
		if err != nil {
			return Action{}, err
		}
		action.View = view
	} else {
		action.View = View{
			Driver: DriverFake, Simulated: true, Capability: action.Capability,
			Outcome: action.Outcome, Effect: action.Effect, ExternalRef: action.ExternalRef,
		}
	}
	return action, nil
}

// ConfirmOutbox checks that the stored second copy still points at the original
// outbox pair for the same principal, operation and client request.
func ConfirmOutbox(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, prior Action) error {
	if prior.MessageID == uuid.Nil || prior.AttemptID == uuid.Nil || prior.PrincipalID == uuid.Nil || prior.ClientRequestID == "" || prior.OutboxOperation == "" || prior.Conversation == uuid.Nil {
		return ErrConflict
	}
	var one int
	err := tx.QueryRow(ctx, `
		SELECT 1
		FROM messages m
		JOIN outbound_attempts a ON a.tenant_id = m.tenant_id AND a.id = $3 AND a.message_id = m.id
		JOIN idempotency_records i ON i.tenant_id = m.tenant_id AND i.id = m.idempotency_id
		WHERE m.tenant_id = $1 AND m.id = $2 AND m.conversation_id = $4
		  AND i.principal_id = $5 AND i.operation = $6 AND i.client_request_id = $7`,
		tenant, prior.MessageID, prior.AttemptID, prior.Conversation, prior.PrincipalID, prior.OutboxOperation, prior.ClientRequestID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPG(err)
	}
	return nil
}

const sha256Len = 32

// InsertLink stores an explicit customer binding.
func InsertLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, link Link) error {
	if link.ID == uuid.Nil || link.ContactID == uuid.Nil || link.ExternalCustomerID == "" {
		return ErrInvalid
	}
	var conversation any
	if link.ConversationID != uuid.Nil {
		conversation = link.ConversationID
	}
	var contract any
	if link.ExternalContractID != "" {
		contract = link.ExternalContractID
	}
	var protocol any
	if link.ExternalProtocol != "" {
		protocol = link.ExternalProtocol
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_links (
			tenant_id, id, contact_id, conversation_id, external_customer_id, external_contract_id, external_protocol, selected
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		tenant, link.ID, link.ContactID, conversation, link.ExternalCustomerID, contract, protocol, link.Selected)
	return mapPG(err)
}

// ListLinks returns the bindings of one contact.
func ListLinks(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID) ([]Link, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, contact_id, conversation_id, external_customer_id, external_contract_id, external_protocol, selected
		FROM erp_links
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY id`, tenant, contact)
	if err != nil {
		return nil, mapPG(err)
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var link Link
		var conversation *uuid.UUID
		var contract, protocol *string
		if err := rows.Scan(&link.ID, &link.ContactID, &conversation, &link.ExternalCustomerID, &contract, &protocol, &link.Selected); err != nil {
			return nil, mapPG(err)
		}
		if conversation != nil {
			link.ConversationID = *conversation
		}
		if contract != nil {
			link.ExternalContractID = *contract
		}
		if protocol != nil {
			link.ExternalProtocol = *protocol
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPG(err)
	}
	return out, nil
}

// ClearSelected drops the previous choice of a conversation before a new one is stored.
func ClearSelected(ctx context.Context, tx pgx.Tx, tenant, conversation, except uuid.UUID) error {
	if tx == nil || tenant == uuid.Nil || conversation == uuid.Nil {
		return ErrInvalid
	}
	_, err := tx.Exec(ctx, `
		UPDATE erp_links
		SET selected = false
		WHERE tenant_id = $1 AND conversation_id = $2 AND selected AND id <> $3`,
		tenant, conversation, except)
	return mapPG(err)
}

// DeleteLink removes one explicit binding of the contact.
func DeleteLink(ctx context.Context, tx pgx.Tx, tenant, contact, conversation, link uuid.UUID) error {
	if link == uuid.Nil || contact == uuid.Nil || conversation == uuid.Nil {
		return ErrInvalid
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM erp_links
		WHERE tenant_id = $1 AND id = $2 AND contact_id = $3 AND conversation_id = $4`,
		tenant, link, contact, conversation)
	if err != nil {
		return mapPG(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// SetExternalProtocol stores the ERP protocol beside the local one and returns the local protocol.
func SetExternalProtocol(ctx context.Context, tx pgx.Tx, tenant, link, conversation uuid.UUID, external string) (string, error) {
	external = strings.TrimSpace(external)
	if link == uuid.Nil || conversation == uuid.Nil || external == "" {
		return "", ErrInvalid
	}
	tag, err := tx.Exec(ctx, `
		UPDATE erp_links
		SET external_protocol = $4
		WHERE tenant_id = $1 AND id = $2 AND conversation_id = $3`,
		tenant, link, conversation, external)
	if err != nil {
		return "", mapPG(err)
	}
	if tag.RowsAffected() != 1 {
		return "", ErrNotFound
	}
	var local string
	err = tx.QueryRow(ctx, `
		UPDATE conversations
		SET external_protocol = $3
		WHERE tenant_id = $1 AND id = $2
		RETURNING protocol`, tenant, conversation, external).Scan(&local)
	if err != nil {
		return "", mapPG(err)
	}
	return KeepLocalProtocol(local, external)
}

// InsertGrant stores an explicit permission for one sensitive capability.
func InsertGrant(ctx context.Context, tx pgx.Tx, tenant, membership uuid.UUID, capability string) error {
	if membership == uuid.Nil || !sensitive(capability) {
		return ErrInvalid
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO erp_capability_grants (tenant_id, membership_id, capability)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, membership_id, capability) DO NOTHING`,
		tenant, membership, capability)
	return mapPG(err)
}

// HasGrant reports whether the membership may run a sensitive capability.
func HasGrant(ctx context.Context, tx pgx.Tx, tenant, membership uuid.UUID, capability string) (bool, error) {
	if !sensitive(capability) {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM erp_capability_grants
			WHERE tenant_id = $1 AND membership_id = $2 AND capability = $3
		)`, tenant, membership, capability).Scan(&ok)
	if err != nil {
		return false, mapPG(err)
	}
	return ok, nil
}

func eventKind(action Action) string {
	reason := action.ReasonCode
	if reason == "" {
		reason = reasonCodeOf(action)
	}
	outcome := action.Outcome
	if outcome == "" {
		outcome = OutcomeUnavailable
	}
	return "erp_action:" + action.Capability + ":" + outcome + ":" + reason
}

func auditResult(action Action) string {
	reason := action.ReasonCode
	if reason == "" {
		reason = reasonCodeOf(action)
	}
	if action.Outcome == OutcomeUnavailable && reason != "" {
		return action.Outcome + ":" + reason
	}
	if action.Outcome == "" {
		return OutcomeUnavailable
	}
	return action.Outcome
}

func reasonCodeOf(action Action) string {
	if action.ReasonCode != "" {
		return action.ReasonCode
	}
	switch action.View.Reason {
	case ReasonDown:
		return "down"
	case ReasonTimeout:
		return "timeout"
	case ReasonCircuit:
		return "circuit_open"
	case ReasonUnavailable, ReasonNotSimulation:
		return "unavailable"
	case ReasonHidden:
		return "hidden"
	case ReasonNotFound:
		return "not_found"
	case ReasonInvoice:
		return "invoice"
	case ReasonUncertain:
		return "uncertain"
	}
	switch action.Outcome {
	case OutcomeOK:
		return "ok"
	case OutcomeDenied:
		return "grant"
	case OutcomeUncertain:
		return "uncertain"
	case OutcomeNotFound:
		return "not_found"
	case OutcomeHidden:
		return "hidden"
	case OutcomeUnavailable:
		return "down"
	default:
		if action.Outcome != "" {
			return "invalid"
		}
		return "unavailable"
	}
}

type snapshot struct {
	Driver      string            `json:"driver"`
	Simulated   bool              `json:"simulated"`
	Capability  string            `json:"capability,omitempty"`
	Outcome     string            `json:"outcome,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Effect      string            `json:"effect,omitempty"`
	ConsultedAt *time.Time        `json:"consulted_at,omitempty"`
	Customer    *snapshotCustomer `json:"customer,omitempty"`
	Invoices    []snapshotInvoice `json:"invoices,omitempty"`
	Document    string            `json:"document,omitempty"`
	PixCode     string            `json:"pix_code,omitempty"`
	ExternalRef string            `json:"external_ref,omitempty"`
}

type snapshotCustomer struct {
	ExternalID  string `json:"external_id"`
	ContractID  string `json:"contract_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type snapshotInvoice struct {
	ExternalID string `json:"external_id"`
	Label      string `json:"label,omitempty"`
}

func encodeSnapshot(view View) ([]byte, error) {
	snap := snapshot{
		Driver: view.Driver, Simulated: view.Simulated, Capability: view.Capability,
		Outcome: view.Outcome, Reason: view.Reason, Effect: view.Effect,
		PixCode: view.PixCode, ExternalRef: view.ExternalRef, Document: string(view.Document),
	}
	if view.Driver == "" {
		snap.Driver = DriverFake
	}
	if snap.Driver == DriverFake {
		snap.Simulated = true
	}
	if !view.ConsultedAt.IsZero() {
		at := view.ConsultedAt.UTC()
		snap.ConsultedAt = &at
	}
	if view.Customer != nil {
		snap.Customer = &snapshotCustomer{
			ExternalID: view.Customer.ExternalID, ContractID: view.Customer.ContractID, DisplayName: view.Customer.DisplayName,
		}
	}
	for _, item := range view.Invoices {
		snap.Invoices = append(snap.Invoices, snapshotInvoice{ExternalID: item.ExternalID, Label: item.Label})
	}
	raw, err := json.Marshal(snap)
	if err != nil || len(raw) > 8192 {
		return nil, ErrInvalid
	}
	return raw, nil
}

func decodeSnapshot(raw []byte) (View, error) {
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return View{}, ErrInvalid
	}
	view := View{
		Driver: snap.Driver, Simulated: snap.Simulated, Capability: snap.Capability,
		Outcome: snap.Outcome, Reason: snap.Reason, Effect: snap.Effect,
		PixCode: snap.PixCode, ExternalRef: snap.ExternalRef,
	}
	if view.Driver == DriverFake {
		view.Simulated = true
	}
	if snap.ConsultedAt != nil {
		view.ConsultedAt = snap.ConsultedAt.UTC()
	}
	if snap.Document != "" {
		view.Document = []byte(snap.Document)
	}
	if snap.Customer != nil && snap.Customer.ExternalID != "" {
		view.Customer = &Customer{
			ExternalID: snap.Customer.ExternalID, ContractID: snap.Customer.ContractID, DisplayName: snap.Customer.DisplayName,
		}
	}
	for _, item := range snap.Invoices {
		view.Invoices = append(view.Invoices, Invoice{ExternalID: item.ExternalID, Label: item.Label})
	}
	return view, nil
}

func mapPG(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return errors.New("falha ao gravar o contrato do erp")
}
