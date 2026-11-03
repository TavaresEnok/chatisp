// Package channelpolicy implements dispatch policy without sending messages.
package channelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalid = errors.New("invalid policy input")
var ErrUnsupported = errors.New("official policy unavailable for this driver")
var ErrStaleCatalog = errors.New("catalog synchronization superseded")

type Service struct {
	DB     *db.Store
	Clock  func(context.Context, pgx.Tx) (time.Time, error)
	Driver CatalogDriver
}
type Scope struct {
	InboxID   uuid.UUID `json:"inbox_id"`
	ContactID uuid.UUID `json:"contact_id"`
	Purpose   string    `json:"purpose"`
}
type Dispatch struct {
	Scope
	ConversationID uuid.UUID `json:"conversation_id"`
	Mode           string    `json:"mode"`
	Kind           string    `json:"kind"`
	TemplateID     string    `json:"template_id,omitempty"`
}
type Decision struct {
	Allowed        bool       `json:"allowed"`
	Reason         string     `json:"reason"`
	Driver         string     `json:"driver"`
	OfficialWindow bool       `json:"official_window"`
	EvaluatedAt    time.Time  `json:"evaluated_at"`
	WindowEndsAt   *time.Time `json:"window_ends_at,omitempty"`
}

func (s Service) now(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	if s.Clock != nil {
		return s.Clock(ctx, tx)
	}
	var n time.Time
	e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&n)
	return n, e
}
func validScope(v Scope) bool {
	return v.InboxID != uuid.Nil && v.ContactID != uuid.Nil && len(v.Purpose) > 0 && len(v.Purpose) <= 80 && strings.TrimSpace(v.Purpose) == v.Purpose
}

// lockScope serializes consent, opt-out, registration and dispatch, including an absent consent.
// Order is the phone and connector configuration, then the contact. The caller
// locks the conversation only after this returns. The dispatch fence uses the
// same order: connector, contact, conversation, each in its own statement.
func lockScope(ctx context.Context, tx pgx.Tx, p sectors.Principal, v Scope) error {
	if !validScope(v) {
		return ErrInvalid
	}
	if e := lockInboxConfiguration(ctx, tx, p, v.InboxID); e != nil {
		return e
	}
	// One table only. The identity predicate does not lock the connector again.
	var id uuid.UUID
	e := tx.QueryRow(ctx, `SELECT c.id FROM contacts c WHERE c.tenant_id=$1 AND c.id=$2 AND EXISTS(SELECT 1 FROM contact_channel_identities ci JOIN inboxes i ON i.tenant_id=ci.tenant_id AND i.connector_id=ci.connector_id WHERE ci.tenant_id=c.tenant_id AND ci.contact_id=c.id AND i.id=$3) FOR UPDATE OF c`, p.TenantID, v.ContactID, v.InboxID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return sectors.ErrForbidden
	}
	return e
}

// LockConnector, LockContact and LockConversation are the shared row order for
// acceptance, revocation and the final dispatch fence. Each statement locks one
// table. A join must not be the first acquisition: its plan can lock the
// conversation before the connector. exclusive asks for FOR UPDATE; otherwise
// the row is taken FOR SHARE. The caller chooses the mode. The order does not.
func LockConnector(ctx context.Context, tx pgx.Tx, tenant, connector uuid.UUID, exclusive bool) error {
	return lockID(ctx, tx, `SELECT id FROM channel_connectors WHERE tenant_id=$1 AND id=$2`, tenant, connector, exclusive)
}

func LockContact(ctx context.Context, tx pgx.Tx, tenant, contact uuid.UUID, exclusive bool) error {
	return lockID(ctx, tx, `SELECT id FROM contacts WHERE tenant_id=$1 AND id=$2`, tenant, contact, exclusive)
}

func LockConversation(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, exclusive bool) error {
	return lockID(ctx, tx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, conversation, exclusive)
}

func lockID(ctx context.Context, tx pgx.Tx, query string, tenant, id uuid.UUID, exclusive bool) error {
	if id == uuid.Nil {
		return ErrInvalid
	}
	mode := " FOR SHARE"
	if exclusive {
		mode = " FOR UPDATE"
	}
	var locked uuid.UUID
	e := tx.QueryRow(ctx, query+mode, tenant, id).Scan(&locked)
	if errors.Is(e, pgx.ErrNoRows) {
		return sectors.ErrForbidden
	}
	return e
}

type channel struct {
	driver    string
	enabled   bool
	status    string
	connector uuid.UUID
	waba      uuid.UUID
}

// lockInboxConfiguration follows M2 lockPhone: number, connector, then inbox.
// Discovering the connector does not authorize access. Pin and revalidate the
// inbox association only after the configuration locks, in this same tenant tx.
func lockInboxConfiguration(ctx context.Context, tx pgx.Tx, p sectors.Principal, inbox uuid.UUID) error {
	if e := sectors.ValidatePrincipal(ctx, tx, p); e != nil {
		return e
	}
	var connector uuid.UUID
	e := tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2`, p.TenantID, inbox).Scan(&connector)
	if errors.Is(e, pgx.ErrNoRows) {
		return sectors.ErrForbidden
	}
	if e != nil {
		return e
	}
	var id uuid.UUID
	e = tx.QueryRow(ctx, `SELECT n.id FROM meta_phone_numbers n JOIN channel_connectors cc ON cc.tenant_id=n.tenant_id AND cc.id=n.connector_id WHERE n.tenant_id=$1 AND n.inbox_id=$2 AND n.connector_id=$3 FOR SHARE OF n,cc`, p.TenantID, inbox, connector).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		// Web connectors have no Meta number. A Cloud connector without a
		// number remains unavailable; do not acquire a number after the inbox.
		e = tx.QueryRow(ctx, `SELECT id FROM channel_connectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, connector).Scan(&id)
	}
	if e != nil {
		return e
	}
	if e = sectors.RequireInbox(ctx, tx, p, inbox); e != nil {
		return e
	}
	var current uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT connector_id FROM inboxes WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, inbox).Scan(&current); e != nil {
		return e
	}
	if current != connector {
		return sectors.ErrForbidden
	}
	return nil
}

func loadChannel(ctx context.Context, tx pgx.Tx, p sectors.Principal, inbox uuid.UUID) (channel, error) {
	var c channel
	if e := lockInboxConfiguration(ctx, tx, p, inbox); e != nil {
		return c, e
	}
	e := tx.QueryRow(ctx, `SELECT cc.connector_type,cc.enabled,cc.status,cc.id FROM inboxes i JOIN channel_connectors cc ON cc.tenant_id=i.tenant_id AND cc.id=i.connector_id WHERE i.tenant_id=$1 AND i.id=$2 FOR SHARE OF i,cc`, p.TenantID, inbox).Scan(&c.driver, &c.enabled, &c.status, &c.connector)
	if e != nil || c.driver != "meta_cloud" {
		return c, e
	}
	e = tx.QueryRow(ctx, `SELECT w.id FROM meta_phone_numbers n JOIN meta_wabas w ON w.tenant_id=n.tenant_id AND w.id=n.meta_waba_id WHERE n.tenant_id=$1 AND n.inbox_id=$2 AND n.connector_id=$3 AND n.status='active' AND w.status='active' FOR SHARE OF w`, p.TenantID, inbox, c.connector).Scan(&c.waba)
	return c, e
}

// EvaluateDispatchTx must be called immediately before dispatch in the caller's tenant tx.
// It does not replace outbox ownership/epoch/idempotency checks or perform remote I/O.
func (s Service) EvaluateDispatchTx(ctx context.Context, tx pgx.Tx, p sectors.Principal, in Dispatch) (Decision, error) {
	d := Decision{Reason: "invalid_request"}
	if (in.Mode != "requested" && in.Mode != "proactive") || (in.Kind != "freeform" && in.Kind != "template") {
		return d, ErrInvalid
	}
	// Match ingress and the dispatch fence: connector, contact, then conversation.
	// The conversation row is locked before RequireConversation joins sector
	// tables, so that join cannot take those tables ahead of the conversation.
	if e := lockScope(ctx, tx, p, in.Scope); e != nil {
		return d, e
	}
	if e := LockConversation(ctx, tx, p.TenantID, in.ConversationID, false); e != nil {
		return d, e
	}
	if e := sectors.RequireConversation(ctx, tx, p, in.InboxID, in.ConversationID); e != nil {
		return d, e
	}
	var matches bool
	var identity uuid.UUID
	// RequireConversation already pins this destination row through commit.
	// A contact can have multiple external identities on the same connector.
	if e := tx.QueryRow(ctx, `SELECT contact_id=$3,contact_identity_id FROM conversations WHERE tenant_id=$1 AND id=$2`, p.TenantID, in.ConversationID, in.ContactID).Scan(&matches, &identity); e != nil {
		return d, e
	}
	if !matches {
		return d, sectors.ErrForbidden
	}
	c, e := loadChannel(ctx, tx, p, in.InboxID)
	if errors.Is(e, pgx.ErrNoRows) {
		d.Reason = "channel_unavailable"
		return d, nil
	}
	if e != nil {
		return d, e
	}
	d.Driver = c.driver
	d.OfficialWindow = c.driver == "meta_cloud"
	d.EvaluatedAt, e = s.now(ctx, tx)
	if e != nil {
		return d, e
	}
	var blocked bool
	if e = tx.QueryRow(ctx, `SELECT blocked FROM contacts WHERE tenant_id=$1 AND id=$2`, p.TenantID, in.ContactID).Scan(&blocked); e != nil {
		return d, e
	}
	if blocked {
		d.Reason = "contact_blocked"
		return d, nil
	}
	if !c.enabled || c.status != "connected" {
		d.Reason = "channel_unavailable"
		return d, nil
	}
	if in.Mode == "proactive" {
		var granted bool
		e = tx.QueryRow(ctx, `SELECT revoked_at IS NULL AND granted_at<=$5 FROM contact_consents WHERE tenant_id=$1 AND contact_id=$2 AND inbox_id=$3 AND purpose=$4 ORDER BY sequence DESC LIMIT 1`, p.TenantID, in.ContactID, in.InboxID, in.Purpose, d.EvaluatedAt).Scan(&granted)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return d, e
		}
		if !granted {
			d.Reason = "consent_required"
			return d, nil
		}
	}
	if c.driver != "meta_cloud" {
		d.Reason = "web_capability_requires_driver_policy"
		return d, nil
	}
	if in.Kind == "template" {
		var approved bool
		e = tx.QueryRow(ctx, `SELECT state='APPROVED' AND sync_source='driver' FROM wa_templates WHERE tenant_id=$1 AND waba_id=$2 AND external_id=$3 FOR SHARE`, p.TenantID, c.waba, in.TemplateID).Scan(&approved)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return d, e
		}
		if !approved {
			d.Reason = "template_not_authorized"
			return d, nil
		}
		d.Allowed = true
		d.Reason = "approved_template"
		return d, nil
	}
	var last *time.Time
	e = tx.QueryRow(ctx, `SELECT max(m.trusted_at) FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id WHERE m.tenant_id=$1 AND c.contact_id=$2 AND c.inbox_id=$3 AND m.connector_id=$4 AND c.contact_identity_id=$6 AND m.direction='inbound' AND m.origin='customer' AND m.visibility='public' AND NOT m.historical AND m.local_state='received' AND m.trusted_at<=$5`, p.TenantID, in.ContactID, in.InboxID, c.connector, d.EvaluatedAt, identity).Scan(&last)
	if e != nil {
		return d, e
	}
	d.Reason = "window_closed"
	if last != nil {
		end := last.Add(24 * time.Hour)
		d.WindowEndsAt = &end
		if d.EvaluatedAt.Before(end) {
			d.Allowed = true
			d.Reason = "window_open"
		}
	}
	return d, nil
}

type ConsentInput struct {
	Scope
	Source   string `json:"source"`
	Evidence string `json:"evidence"`
	Revoke   bool   `json:"revoke"`
}

// RecordConsentTx appends evidence; revocation and pending invalidation commit together.
func (s Service) RecordConsentTx(ctx context.Context, tx pgx.Tx, p sectors.Principal, in ConsentInput) ([]uuid.UUID, error) {
	if len(strings.TrimSpace(in.Source)) == 0 || len(in.Source) > 120 || len(strings.TrimSpace(in.Evidence)) == 0 || len(in.Evidence) > 500 {
		return nil, ErrInvalid
	}
	if e := lockScope(ctx, tx, p, in.Scope); e != nil {
		return nil, e
	}
	if p.Role == "agent" {
		// Pin every conversation of this contact before the sector join. The
		// join still authorizes visibility, but it is not the first lock.
		list, e := tx.Query(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND inbox_id=$2 AND contact_id=$3 ORDER BY id`, p.TenantID, in.InboxID, in.ContactID)
		if e != nil {
			return nil, e
		}
		var conversations []uuid.UUID
		for list.Next() {
			var id uuid.UUID
			if e = list.Scan(&id); e != nil {
				list.Close()
				return nil, e
			}
			conversations = append(conversations, id)
		}
		list.Close()
		if e = list.Err(); e != nil {
			return nil, e
		}
		for _, id := range conversations {
			if e = LockConversation(ctx, tx, p.TenantID, id, false); e != nil {
				return nil, e
			}
		}
		var visible uuid.UUID
		e = tx.QueryRow(ctx, `SELECT c.id FROM conversations c JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.inbox_id=c.inbox_id AND si.sector_id=c.sector_id JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active JOIN sector_memberships sm ON sm.tenant_id=s.tenant_id AND sm.sector_id=s.id AND sm.membership_id=$4 WHERE c.tenant_id=$1 AND c.inbox_id=$2 AND c.contact_id=$3 ORDER BY c.id LIMIT 1 FOR SHARE OF c,si,s,sm`, p.TenantID, in.InboxID, in.ContactID, p.MembershipID).Scan(&visible)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, sectors.ErrForbidden
		}
		if e != nil {
			return nil, e
		}
	}
	n, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	var revoked any
	if in.Revoke {
		revoked = n
	}
	_, e = tx.Exec(ctx, `INSERT INTO contact_consents(tenant_id,id,contact_id,inbox_id,purpose,source,evidence,granted_at,revoked_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, uuid.New(), in.ContactID, in.InboxID, in.Purpose, in.Source, in.Evidence, n, revoked)
	if e != nil {
		return nil, e
	}
	if in.Revoke {
		return InvalidateProactiveTx(ctx, tx, p, in.Scope)
	}
	return []uuid.UUID{}, nil
}

// InvalidateProactiveTx returns pending IDs to cancel in the joined outbox transaction.
// Only registered proactive pending operations are touched. Uncertain/in-flight sends are preserved.
// Conversations, messages and attempts are locked in that order before pending changes.
func InvalidateProactiveTx(ctx context.Context, tx pgx.Tx, p sectors.Principal, v Scope) ([]uuid.UUID, error) {
	if e := lockScope(ctx, tx, p, v); e != nil {
		return nil, e
	}
	if _, e := tx.Exec(ctx, `SELECT public.channel_policy_lock_outbox($1,$2,$3,$4)`, p.TenantID, v.ContactID, v.InboxID, v.Purpose); e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `UPDATE channel_policy_pending SET state='cancelled' WHERE tenant_id=$1 AND contact_id=$2 AND inbox_id=$3 AND purpose=$4 AND state='pending' RETURNING id`, p.TenantID, v.ContactID, v.InboxID, v.Purpose)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RegisterPendingTx is for proactive intents only. ID must be the eventual outbox ID.
func (s Service) RegisterPendingTx(ctx context.Context, tx pgx.Tx, p sectors.Principal, v Scope, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrInvalid
	}
	if e := lockScope(ctx, tx, p, v); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `INSERT INTO channel_policy_pending(tenant_id,id,contact_id,inbox_id,purpose,state) VALUES($1,$2,$3,$4,$5,'pending') ON CONFLICT(tenant_id,id) DO NOTHING`, p.TenantID, id, v.ContactID, v.InboxID, v.Purpose)
	if e != nil {
		return e
	}
	var same bool
	e = tx.QueryRow(ctx, `SELECT contact_id=$3 AND inbox_id=$4 AND purpose=$5 FROM channel_policy_pending WHERE tenant_id=$1 AND id=$2`, p.TenantID, id, v.ContactID, v.InboxID, v.Purpose).Scan(&same)
	if e != nil {
		return e
	}
	if !same {
		return ErrInvalid
	}
	return nil
}

type Template struct {
	ExternalID string          `json:"external_id"`
	Name       string          `json:"name"`
	Language   string          `json:"language"`
	Category   string          `json:"category"`
	State      string          `json:"state"`
	Components json.RawMessage `json:"components"`
	Source     string          `json:"source"`
}

// CatalogDriver supplies a complete WABA snapshot, never credentials or approval creation.
type CatalogDriver interface {
	Templates(context.Context, string) ([]Template, error)
	IsFake() bool
}

// FakeCatalog is a test double. The zero value is not a configured provider:
// SyncTemplates rejects it before touching the catalog. Explicit opts in to
// applying this double, including an empty snapshot. IsFake stays true, so
// rows it writes do not authorize dispatch.
type FakeCatalog struct {
	Items    []Template
	Err      error
	Explicit bool
}

func (f FakeCatalog) Templates(context.Context, string) ([]Template, error) { return f.Items, f.Err }
func (FakeCatalog) IsFake() bool                                            { return true }

// unconfiguredCatalog is true for the implicit empty fake. A driver with items,
// an error, or Explicit set is a deliberate snapshot source.
func unconfiguredCatalog(driver CatalogDriver) bool {
	switch fake := driver.(type) {
	case FakeCatalog:
		return !fake.Explicit && fake.Err == nil && len(fake.Items) == 0
	case *FakeCatalog:
		return fake == nil || (!fake.Explicit && fake.Err == nil && len(fake.Items) == 0)
	default:
		return false
	}
}
