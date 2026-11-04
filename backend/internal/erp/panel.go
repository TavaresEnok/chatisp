package erp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/channelpolicy"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/sectors"
)

const (
	secondCopyPurpose = "cobranca"
	outboxOperation   = "erp_second_copy"

	capLink     = "link"
	capUnlink   = "unlink"
	capProtocol = "protocol"
)

// PanelOptions wires the simulated connector into the panel. The supplier adapter is ignored.
type PanelOptions struct {
	DB       *db.Store
	Meter    *metrics.Metrics
	Adapter  Adapter
	Now      func() time.Time
	Timeout  time.Duration
	InFlight int
}

// Panel is the tenant-scoped ERP use case. One fake adapter serves every tenant.
type Panel struct {
	db      *db.Store
	meter   *metrics.Metrics
	inner   Adapter
	now     func() time.Time
	timeout time.Duration
	gate    *Gate

	mu sync.Mutex
	by map[uuid.UUID]*Service
}

// LinkInput binds the conversation contact to an opaque customer. Phone numbers are rejected.
type LinkInput struct {
	ExternalCustomerID string
	ExternalContractID string
	Selected           bool
}

// SecondCopyInput asks for a simulated invoice document or Pix and, when allowed, queues a text.
type SecondCopyInput struct {
	InvoiceID       string
	Verification    string
	LocalKey        string
	ClientRequestID string
	Artifact        string
}

// Result is one panel response. Replayed means the local key was not executed again.
type Result struct {
	View          View
	Links         []Link
	LocalProtocol string
	Replayed      bool
	MessageID     uuid.UUID
	AttemptID     uuid.UUID
}

// NewPanel composes the fake adapter with the package gate, timeout and breaker cache.
// SupplierEnabled is not consulted here; callers must keep the real connector unwired.
func NewPanel(opts PanelOptions) *Panel {
	adapter := opts.Adapter
	if adapter == nil {
		adapter = NewFake(AllCapabilities())
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Panel{
		db: opts.DB, meter: opts.Meter, inner: adapter, now: now, timeout: timeout,
		gate: NewGate(opts.InFlight), by: map[uuid.UUID]*Service{},
	}
}

// SetTimeout changes the bound used by the next adapter call.
func (p *Panel) SetTimeout(timeout time.Duration) {
	if p == nil || timeout <= 0 {
		return
	}
	p.timeout = timeout
}

// Adapter returns the composed fake. The supplier implementation is not returned.
func (p *Panel) Adapter() Adapter {
	if p == nil {
		return nil
	}
	return p.inner
}

type panelAdapter struct{ panel *Panel }

func (a panelAdapter) Call(ctx context.Context, in Consult) (View, error) {
	if a.panel == nil {
		return View{}, ErrDown
	}
	return guarded{inner: a.panel.inner, gate: a.panel.gate, timeout: a.panel.timeout}.Call(ctx, in)
}

type convRow struct {
	ID        uuid.UUID
	Inbox     uuid.UUID
	Sector    uuid.UUID
	Contact   uuid.UUID
	Connector uuid.UUID
	Protocol  string
	Status    string
}

// ListLinks returns the bindings of the conversation contact.
func (p *Panel) ListLinks(ctx context.Context, actor Principal, conversation uuid.UUID) (Result, error) {
	return p.withConv(ctx, actor, conversation, capLink, false, func(ctx context.Context, tx pgx.Tx, row convRow) (Result, error) {
		links, err := ListLinks(ctx, tx, actor.TenantID, row.Contact)
		if err != nil {
			return Result{}, err
		}
		out := make([]Link, 0, len(links))
		for _, link := range links {
			if link.ConversationID == row.ID || link.ConversationID == uuid.Nil {
				out = append(out, link)
			}
		}
		return Result{Links: out}, nil
	})
}

// Link binds the contact. Selecting a link clears the previous choice of this conversation.
// The conversation advisory is acquired before authorize, same as protocol and second copy,
// so this update cannot hold FOR SHARE while the other still owns the link row.
func (p *Panel) Link(ctx context.Context, actor Principal, conversation uuid.UUID, in LinkInput) (Result, error) {
	return p.withConv(ctx, actor, conversation, capLink, true, func(ctx context.Context, tx pgx.Tx, row convRow) (Result, error) {
		customer, err := NormalizeReference(in.ExternalCustomerID)
		if err != nil {
			return Result{}, fail(err, "conflict", "invalid", "")
		}
		contract := strings.TrimSpace(in.ExternalContractID)
		if contract != "" {
			contract, err = NormalizeReference(contract)
			if err != nil {
				return Result{}, fail(err, "conflict", "invalid", "")
			}
		}
		link := Link{
			ID: uuid.New(), ContactID: row.Contact, ConversationID: row.ID,
			ExternalCustomerID: customer, ExternalContractID: contract, Selected: in.Selected,
		}
		if link.Selected {
			if err := ClearSelected(ctx, tx, actor.TenantID, row.ID, link.ID); err != nil {
				return Result{}, err
			}
		}
		if err := InsertLink(ctx, tx, actor.TenantID, link); err != nil {
			if errors.Is(err, ErrConflict) {
				return Result{}, fail(ErrConflict, "conflict", "local_key", link.ExternalCustomerID)
			}
			return Result{}, err
		}
		if err := recordEvent(ctx, tx, actor, row.ID, capLink, OutcomeOK, "linked", link.ID.String()); err != nil {
			return Result{}, err
		}
		return Result{Links: []Link{link}}, nil
	})
}

// Unlink removes one binding of this conversation.
func (p *Panel) Unlink(ctx context.Context, actor Principal, conversation, link uuid.UUID) (Result, error) {
	if link == uuid.Nil {
		return Result{}, ErrInvalid
	}
	return p.withConv(ctx, actor, conversation, capUnlink, true, func(ctx context.Context, tx pgx.Tx, row convRow) (Result, error) {
		if err := DeleteLink(ctx, tx, actor.TenantID, row.Contact, row.ID, link); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Result{}, fail(ErrNotFound, OutcomeNotFound, "missing", link.String())
			}
			return Result{}, err
		}
		if err := recordEvent(ctx, tx, actor, row.ID, capUnlink, OutcomeOK, "unlinked", link.String()); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	})
}

// Identify resolves an authorized opaque reference. It does not authenticate the customer and does not link by phone.
func (p *Panel) Identify(ctx context.Context, actor Principal, conversation uuid.UUID, reference, localKey string) (Result, error) {
	return p.consult(ctx, actor, conversation, Consult{
		Capability: SearchCustomer, Reference: reference, LocalKey: strings.TrimSpace(localKey), Conversation: conversation,
	}, false)
}

// Summary reads the selected customer.
func (p *Panel) Summary(ctx context.Context, actor Principal, conversation uuid.UUID) (Result, error) {
	return p.consult(ctx, actor, conversation, Consult{
		Capability: GetCustomerSummary, Conversation: conversation,
	}, false)
}

// Invoices lists open items when the actor has an explicit grant and a verification reference.
func (p *Panel) Invoices(ctx context.Context, actor Principal, conversation uuid.UUID, verification, localKey string) (Result, error) {
	return p.consult(ctx, actor, conversation, Consult{
		Capability: ListOpenInvoices, Verification: strings.TrimSpace(verification),
		LocalKey: strings.TrimSpace(localKey), Conversation: conversation,
	}, false)
}

// SecondCopy fetches a simulated document or Pix and queues one text on the existing outbox.
func (p *Panel) SecondCopy(ctx context.Context, actor Principal, conversation uuid.UUID, in SecondCopyInput) (Result, error) {
	artifact := strings.TrimSpace(in.Artifact)
	capability := GetPixCode
	if artifact == "document" {
		capability = GetInvoiceDocument
	}
	if artifact != "pix" && artifact != "document" {
		return p.withConv(ctx, actor, conversation, GetPixCode, true, func(context.Context, pgx.Tx, convRow) (Result, error) {
			return Result{}, fail(ErrInvalid, "conflict", "invalid", safeKey(in.LocalKey))
		})
	}
	return p.consult(ctx, actor, conversation, Consult{
		Capability: capability, InvoiceID: in.InvoiceID, Verification: in.Verification,
		LocalKey: in.LocalKey, Conversation: conversation,
	}, true, in.ClientRequestID)
}

// Protocol stores the ERP protocol without replacing the local protocol already shown to the customer.
func (p *Panel) Protocol(ctx context.Context, actor Principal, conversation uuid.UUID, external string) (Result, error) {
	return p.withConv(ctx, actor, conversation, capProtocol, true, func(ctx context.Context, tx pgx.Tx, row convRow) (Result, error) {
		external = strings.TrimSpace(external)
		if external == "" || strings.ContainsAny(external, "\r\n") || len(external) > 128 {
			return Result{}, fail(ErrInvalid, "conflict", "invalid", "")
		}
		if _, err := KeepLocalProtocol(row.Protocol, external); err != nil {
			return Result{}, fail(ErrInvalid, "conflict", "invalid", "")
		}
		chosen, err := selectedLink(ctx, tx, actor.TenantID, row)
		if err != nil {
			return Result{}, selectionFailure(err, "")
		}
		local, err := SetExternalProtocol(ctx, tx, actor.TenantID, chosen.ID, row.ID, external)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Result{}, fail(ErrNotFound, OutcomeNotFound, "missing", "")
			}
			if errors.Is(err, ErrInvalid) {
				return Result{}, fail(ErrInvalid, "conflict", "invalid", "")
			}
			return Result{}, err
		}
		if err := recordEvent(ctx, tx, actor, row.ID, capProtocol, OutcomeOK, "protocol", external); err != nil {
			return Result{}, err
		}
		return Result{LocalProtocol: local, Links: []Link{{
			ID: chosen.ID, ContactID: chosen.ContactID, ConversationID: row.ID,
			ExternalCustomerID: chosen.ExternalCustomerID, ExternalContractID: chosen.ExternalContractID,
			ExternalProtocol: external, Selected: true,
		}}}, nil
	})
}

func (p *Panel) authorize(ctx context.Context, tx pgx.Tx, actor Principal, conversation uuid.UUID) (convRow, bool, error) {
	sp := sectors.Principal{TenantID: actor.TenantID, UserID: actor.UserID, MembershipID: actor.Membership, Role: actor.Role}
	if err := sectors.ValidatePrincipal(ctx, tx, sp); err != nil {
		return convRow{}, true, nil
	}
	row, err := loadConversation(ctx, tx, actor.TenantID, conversation)
	if errors.Is(err, ErrNotFound) {
		return convRow{}, false, ErrNotFound
	}
	if err != nil {
		return convRow{}, false, err
	}
	if actor.Role == "agent" {
		var member bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sector_memberships sm
				JOIN sectors s ON s.tenant_id = sm.tenant_id AND s.id = sm.sector_id AND s.active
				WHERE sm.tenant_id = $1 AND sm.membership_id = $2 AND sm.sector_id = $3
			)`, actor.TenantID, actor.Membership, row.Sector).Scan(&member)
		if err != nil {
			return convRow{}, false, mapPG(err)
		}
		if !member {
			return row, true, nil
		}
	}
	if err := sectors.RequireConversation(ctx, tx, sp, row.Inbox, row.ID); err != nil {
		return row, true, nil
	}
	return row, false, nil
}

func (p *Panel) admitSend(ctx context.Context, tx pgx.Tx, actor Principal, row convRow) error {
	decision, err := (channelpolicy.Service{}).EvaluateDispatchTx(ctx, tx, sectors.Principal{
		TenantID: actor.TenantID, UserID: actor.UserID, MembershipID: actor.Membership, Role: actor.Role,
	}, channelpolicy.Dispatch{
		Scope:          channelpolicy.Scope{InboxID: row.Inbox, ContactID: row.Contact, Purpose: secondCopyPurpose},
		ConversationID: row.ID, Mode: "proactive", Kind: "freeform",
	})
	if err != nil {
		if errors.Is(err, sectors.ErrForbidden) {
			return ErrDenied
		}
		return err
	}
	if !decision.Allowed {
		return &DispatchError{Reason: decision.Reason}
	}
	return nil
}

func (p *Panel) open(conn Connection) (*Service, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if svc := p.by[conn.ID]; svc != nil {
		svc.SetConnection(conn)
		return svc, nil
	}
	svc, err := New(conn, panelAdapter{panel: p})
	if err != nil {
		return nil, err
	}
	p.by[conn.ID] = svc
	return svc, nil
}

func (p *Panel) observe(result string) {
	if p == nil || p.meter == nil {
		return
	}
	p.meter.ObserveERP(DriverFake, result)
}

func loadConversation(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (convRow, error) {
	var row convRow
	var protocol *string
	err := tx.QueryRow(ctx, `
		SELECT c.id, c.inbox_id, c.sector_id, c.contact_id, i.connector_id, c.protocol, c.status
		FROM conversations c
		JOIN inboxes i ON i.tenant_id = c.tenant_id AND i.id = c.inbox_id
		WHERE c.tenant_id = $1 AND c.id = $2`, tenant, id).Scan(
		&row.ID, &row.Inbox, &row.Sector, &row.Contact, &row.Connector, &protocol, &row.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return convRow{}, ErrNotFound
	}
	if err != nil {
		return convRow{}, mapPG(err)
	}
	if protocol != nil {
		row.Protocol = *protocol
	}
	return row, nil
}

func memberSectors(ctx context.Context, tx pgx.Tx, actor Principal) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT sector_id FROM sector_memberships
		WHERE tenant_id = $1 AND membership_id = $2
		ORDER BY sector_id`, actor.TenantID, actor.Membership)
	if err != nil {
		return nil, mapPG(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapPG(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPG(err)
	}
	return ids, nil
}

func linksOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row convRow) ([]Link, error) {
	links, err := ListLinks(ctx, tx, tenant, row.Contact)
	if err != nil {
		return nil, err
	}
	out := make([]Link, 0, len(links))
	for _, link := range links {
		if link.ConversationID == row.ID {
			out = append(out, link)
		}
	}
	return out, nil
}

func selectedLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row convRow) (Link, error) {
	links, err := linksOf(ctx, tx, tenant, row)
	if err != nil {
		return Link{}, err
	}
	return Choose(links)
}

func secondCopyBody(view View) (string, error) {
	switch view.Capability {
	case GetPixCode:
		if view.PixCode == "" || !view.Simulated {
			return "", ErrInvalid
		}
		return "Simulação de Pix\n" + view.PixCode, nil
	case GetInvoiceDocument:
		if len(view.Document) == 0 || !view.Simulated {
			return "", ErrInvalid
		}
		return string(view.Document), nil
	default:
		return "", ErrInvalid
	}
}

func recordEvent(ctx context.Context, tx pgx.Tx, actor Principal, conversation uuid.UUID, capability, outcome, reason, correlation string) error {
	return RecordFailure(ctx, tx, actor.TenantID, Action{
		ID: uuid.New(), Conversation: conversation, Capability: capability, Outcome: outcome,
		ReasonCode: reason, LocalKey: correlation, ActorID: actor.UserID, MembershipID: actor.Membership,
	})
}
