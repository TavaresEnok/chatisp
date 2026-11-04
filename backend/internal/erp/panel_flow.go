package erp

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	store "chatisp/internal/attendance/store"
	"chatisp/internal/channelpolicy"
)

// recorded is a business failure. The transaction rolls back to the savepoint
// and then stores the timeline and audit rows without keeping partial writes.
type recorded struct {
	err         error
	outcome     string
	reason      string
	correlation string
}

func (e *recorded) Error() string {
	if e == nil || e.err == nil {
		return "erp"
	}
	return e.err.Error()
}

func (e *recorded) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func fail(err error, outcome, reason, correlation string) error {
	return &recorded{err: err, outcome: outcome, reason: reason, correlation: correlation}
}

func selectionFailure(err error, correlation string) error {
	if errors.Is(err, ErrNotFound) {
		return fail(ErrNotFound, OutcomeNotFound, "missing", correlation)
	}
	if errors.Is(err, ErrChoiceRequired) {
		return fail(ErrChoiceRequired, "conflict", "choice", correlation)
	}
	return err
}

func observedResult(err error) string {
	switch {
	case errors.Is(err, ErrDenied):
		return OutcomeDenied
	case errors.Is(err, ErrDown), errors.Is(err, ErrOpen), errors.Is(err, ErrTimeout):
		return OutcomeUnavailable
	case errors.Is(err, ErrConflict), errors.Is(err, ErrChoiceRequired), errors.Is(err, ErrDispatch):
		return "conflict"
	case errors.Is(err, ErrNotFound):
		return OutcomeNotFound
	default:
		return "unavailable"
	}
}

func unavailableErr(action Action) error {
	switch action.ReasonCode {
	case "timeout":
		return ErrTimeout
	case "circuit_open":
		return ErrOpen
	default:
		return ErrDown
	}
}

func (p *Panel) withConv(ctx context.Context, actor Principal, conversation uuid.UUID, capability string, pin bool, fn func(context.Context, pgx.Tx, convRow) (Result, error)) (Result, error) {
	out, err := p.transact(ctx, actor, conversation, capability, "", pin, false, fn)
	if err != nil {
		p.observe(observedResult(err))
		return Result{}, err
	}
	p.observe(OutcomeOK)
	return out, nil
}

func (p *Panel) consult(ctx context.Context, actor Principal, conversation uuid.UUID, in Consult, send bool, requestID ...string) (Result, error) {
	clientRequest := ""
	if len(requestID) > 0 {
		clientRequest = requestID[0]
	}
	out, err := p.transact(ctx, actor, conversation, in.Capability, strings.TrimSpace(in.LocalKey), send, send, func(ctx context.Context, tx pgx.Tx, row convRow) (Result, error) {
		return p.consultTx(ctx, tx, actor, row, in, send, clientRequest)
	})
	if err != nil {
		p.observe(observedResult(err))
		return Result{}, err
	}
	observed := out.View.Outcome
	if observed == "" {
		observed = OutcomeOK
	}
	p.observe(observed)
	return out, nil
}

func (p *Panel) transact(ctx context.Context, actor Principal, conversation uuid.UUID, capability, localKey string, pin, send bool, fn func(context.Context, pgx.Tx, convRow) (Result, error)) (Result, error) {
	if p == nil || p.db == nil || actor.TenantID == uuid.Nil || conversation == uuid.Nil {
		return Result{}, ErrInvalid
	}
	var out Result
	var status error
	err := p.db.WithTenantTx(ctx, actor.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// The local-key advisory only serializes ERP commands. The dispatch
		// fence does not take it. A send must already hold connector, contact
		// and conversation, in that order, before authorize shares the
		// conversation. Sharing it first deadlocks with the fence, which holds
		// the connector and then waits for the conversation.
		if err := serializeERP(ctx, tx, actor.TenantID, conversation, localKey, pin); err != nil {
			return err
		}
		if send {
			if err := lockSendRows(ctx, tx, actor.TenantID, conversation); err != nil {
				return err
			}
		}
		row, denied, err := p.authorize(ctx, tx, actor, conversation)
		if err != nil {
			return err
		}
		if denied {
			if row.ID != uuid.Nil {
				if err := recordEvent(ctx, tx, actor, row.ID, capability, OutcomeDenied, "sector", conversation.String()); err != nil {
					return err
				}
			}
			status = ErrDenied
			return nil
		}
		if _, err := tx.Exec(ctx, "SAVEPOINT erp_panel"); err != nil {
			return err
		}
		var opErr error
		out, opErr = fn(ctx, tx, row)
		var rec *recorded
		if errors.As(opErr, &rec) {
			if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT erp_panel"); err != nil {
				return err
			}
			if err := recordEvent(ctx, tx, actor, row.ID, capability, rec.outcome, rec.reason, rec.correlation); err != nil {
				return err
			}
			status = rec.err
			return nil
		}
		if opErr != nil {
			return opErr
		}
		_, err = tx.Exec(ctx, "RELEASE SAVEPOINT erp_panel")
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if status != nil {
		return Result{}, status
	}
	return out, nil
}

func (p *Panel) consultTx(ctx context.Context, tx pgx.Tx, actor Principal, row convRow, in Consult, send bool, clientRequest string) (Result, error) {
	var normErr error
	in, clientRequest, normErr = canonConsult(in, send, clientRequest)
	if normErr != nil {
		return Result{}, fail(normErr, "conflict", "invalid", safeKey(in.LocalKey))
	}
	conn, err := LoadConnection(ctx, tx, actor.TenantID)
	if errors.Is(err, ErrNotFound) {
		return Result{}, fail(ErrDown, OutcomeUnavailable, "down", in.LocalKey)
	}
	if err != nil {
		return Result{}, err
	}
	// Authorize before any replay. A stored action must not outlive the
	// connection binding: a nil sector denies an agent the same way a live
	// consult does. The conversation sector still has to match when set.
	caller, err := authorizeConnection(ctx, tx, actor, conn, row, in.LocalKey)
	if err != nil {
		return Result{}, err
	}
	if sensitive(in.Capability) && in.Verification != "" {
		ok, err := HasGrant(ctx, tx, actor.TenantID, actor.Membership, in.Capability)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, fail(ErrDenied, OutcomeDenied, "grant", in.LocalKey)
		}
	}
	if in.Capability == GetCustomerSummary || in.Capability == ListOpenInvoices || send {
		chosen, err := selectedLink(ctx, tx, actor.TenantID, row)
		if err != nil {
			return Result{}, selectionFailure(err, in.LocalKey)
		}
		in.CustomerID = chosen.ExternalCustomerID
		in.ContractID = chosen.ExternalContractID
	}
	var prior Action
	replay := false
	if in.LocalKey != "" {
		loaded, loadErr := LoadActionByKey(ctx, tx, actor.TenantID, in.LocalKey)
		if loadErr == nil {
			if loaded.Hash != fingerprint(in) {
				return Result{}, fail(ErrConflict, "conflict", "local_key", in.LocalKey)
			}
			if loaded.Outcome == OutcomeOK || loaded.Outcome == OutcomeUncertain {
				prior = loaded
				replay = true
			}
		} else if !errors.Is(loadErr, ErrNotFound) {
			return Result{}, loadErr
		}
	}
	if replay {
		if send && prior.Outcome == OutcomeOK {
			if prior.ClientRequestID != clientRequest || prior.OutboxOperation != outboxOperation {
				return Result{}, fail(ErrConflict, "conflict", "local_key", in.LocalKey)
			}
			if err := ConfirmOutbox(ctx, tx, actor.TenantID, prior); err != nil {
				if errors.Is(err, ErrConflict) {
					return Result{}, fail(ErrConflict, "conflict", "outbox", in.LocalKey)
				}
				return Result{}, err
			}
			return Result{View: prior.View, Replayed: true, MessageID: prior.MessageID, AttemptID: prior.AttemptID}, nil
		}
		return Result{View: prior.View, Replayed: true}, nil
	}
	if send {
		if _, ok := invoiceRecord(in.InvoiceID); !ok {
			return Result{}, fail(ErrNotFound, OutcomeNotFound, "not_found", in.LocalKey)
		}
		if _, ok := ownedInvoice(in); !ok {
			return Result{}, fail(ErrNotFound, OutcomeNotFound, "invoice", in.LocalKey)
		}
		if err := p.admitSend(ctx, tx, actor, row); err != nil {
			var deniedSend *DispatchError
			if errors.As(err, &deniedSend) {
				return Result{}, fail(err, "conflict", "dispatch", in.LocalKey)
			}
			if errors.Is(err, ErrDenied) {
				return Result{}, fail(ErrDenied, OutcomeDenied, "sector", in.LocalKey)
			}
			return Result{}, err
		}
	}
	svc, err := p.open(conn)
	if err != nil {
		return Result{}, err
	}
	in.Conversation = row.ID
	in.Now = p.now()
	action, err := svc.Consult(ctx, caller, in)
	if err != nil {
		switch {
		case errors.Is(err, ErrDenied):
			return Result{}, fail(ErrDenied, OutcomeDenied, "sector", in.LocalKey)
		case errors.Is(err, ErrConflict):
			return Result{}, fail(ErrConflict, "conflict", "local_key", in.LocalKey)
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrReferenceRequired), errors.Is(err, ErrPhoneNotSelector), errors.Is(err, ErrKeyRequired):
			return Result{}, fail(err, "conflict", "invalid", safeKey(in.LocalKey))
		default:
			return Result{}, err
		}
	}
	action.ActorID = actor.UserID
	action.MembershipID = actor.Membership
	if action.ReasonCode == "" {
		action.ReasonCode = reasonCodeOf(action)
	}
	if action.Outcome == OutcomeUnavailable {
		return Result{}, fail(unavailableErr(action), OutcomeUnavailable, action.ReasonCode, in.LocalKey)
	}
	if send && action.Outcome == OutcomeNotFound {
		return Result{}, fail(ErrNotFound, OutcomeNotFound, action.ReasonCode, in.LocalKey)
	}
	if action.Outcome == OutcomeOK || action.Outcome == OutcomeUncertain {
		out := Result{View: action.View}
		if send && action.Outcome == OutcomeOK {
			body, err := secondCopyBody(action.View)
			if err != nil {
				return Result{}, fail(ErrInvalid, "conflict", "invalid", in.LocalKey)
			}
			sent, err := (store.Repo{}).EnqueueText(ctx, tx, store.OutboundInput{
				TenantID: actor.TenantID, ConversationID: row.ID, ConnectorID: row.Connector,
				PrincipalID: actor.Membership, Operation: outboxOperation, ClientRequestID: clientRequest,
				Payload: []byte(`{"artifact":"erp"}`), Body: body, At: in.Now,
			})
			if err != nil {
				if errors.Is(err, store.ErrIdempotencyConflict) {
					return Result{}, fail(ErrConflict, "conflict", "outbox", in.LocalKey)
				}
				return Result{}, fail(ErrDown, OutcomeUnavailable, "outbox", in.LocalKey)
			}
			action.ClientRequestID = clientRequest
			action.OutboxOperation = outboxOperation
			action.PrincipalID = actor.Membership
			action.MessageID = sent.MessageID
			action.AttemptID = sent.AttemptID
			action.ReasonCode = "sent"
			out.MessageID = sent.MessageID
			out.AttemptID = sent.AttemptID
			out.Replayed = sent.Replayed
		}
		if action.LocalKey != "" {
			prior, replayed, err := writeAction(ctx, tx, actor.TenantID, action)
			if errors.Is(err, ErrConflict) {
				return Result{}, fail(ErrConflict, "conflict", "local_key", in.LocalKey)
			}
			if err != nil {
				return Result{}, err
			}
			if replayed {
				return replayedAction(ctx, tx, actor.TenantID, prior, send, clientRequest)
			}
		} else if err := recordEvent(ctx, tx, actor, row.ID, action.Capability, action.Outcome, action.ReasonCode, in.LocalKey); err != nil {
			return Result{}, err
		}
		return out, nil
	}
	if err := recordEvent(ctx, tx, actor, row.ID, action.Capability, action.Outcome, action.ReasonCode, in.LocalKey); err != nil {
		return Result{}, err
	}
	return Result{View: action.View}, nil
}

// lockSendRows follows the dispatch fence: connector, contact, conversation.
// Each helper locks one table. The conversation share in authorize and the
// later outbox update must not run before the connector lock.
func lockSendRows(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) error {
	row, err := loadConversation(ctx, tx, tenant, conversation)
	if err != nil {
		return err
	}
	if err = channelpolicy.LockConnector(ctx, tx, tenant, row.Connector, false); err != nil {
		return err
	}
	if err = channelpolicy.LockContact(ctx, tx, tenant, row.Contact, false); err != nil {
		return err
	}
	return channelpolicy.LockConversation(ctx, tx, tenant, row.ID, false)
}

// serializeERP takes transaction-scoped advisory locks before any shared row lock.
// pin is set for protocol, second copy, and link mutations. Those commands update
// erp_links and then the conversation, so one must not reach FOR SHARE while another
// already holds the link row. The conversation lock is first, then the local key,
// so two panel commands cannot invert that order. The key lock also makes the loser
// of a same-key race observe the committed action instead of inserting again.
func serializeERP(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, localKey string, pin bool) error {
	if pin && conversation != uuid.Nil {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "erp-conv:"+tenant.String()+":"+conversation.String()); err != nil {
			return err
		}
	}
	if localKey != "" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "erp-key:"+tenant.String()+":"+localKey); err != nil {
			return err
		}
	}
	return nil
}

// canonConsult applies the same normalization Service.Consult uses before the
// replay hash. Validation errors stay with the caller so the refusal is recorded.
func canonConsult(in Consult, send bool, clientRequest string) (Consult, string, error) {
	in.LocalKey = strings.TrimSpace(in.LocalKey)
	in.Verification = strings.TrimSpace(in.Verification)
	clientRequest = strings.TrimSpace(clientRequest)
	// Optional on identify and invoices. erp_actions rejects a longer key and
	// would abort the attempt, including its audit row. Reject it here.
	if len(in.LocalKey) > 200 {
		return in, clientRequest, ErrInvalid
	}
	if in.Capability == SearchCustomer {
		ref, err := NormalizeReference(in.Reference)
		if err != nil {
			return in, clientRequest, err
		}
		in.Reference = ref
	}
	if send || in.Capability == GetInvoiceDocument || in.Capability == GetPixCode {
		if in.Capability != GetInvoiceDocument && in.Capability != GetPixCode {
			return in, clientRequest, ErrInvalid
		}
		invoice, err := NormalizeReference(in.InvoiceID)
		if err != nil {
			return in, clientRequest, err
		}
		in.InvoiceID = invoice
		if clientRequest == "" {
			return in, clientRequest, ErrKeyRequired
		}
		// The outbox stores this id. A longer value becomes ErrDown there and
		// drops the audit of the attempt. Reject it before the adapter runs.
		if len(clientRequest) > 200 {
			return in, clientRequest, ErrInvalid
		}
		if err := requireKey(in); err != nil {
			return in, clientRequest, err
		}
	}
	return in, clientRequest, nil
}

// authorizeConnection applies Authorize to the current principal and keeps the
// panel rule that an agent's connection sector must be the conversation sector.
func authorizeConnection(ctx context.Context, tx pgx.Tx, actor Principal, conn Connection, row convRow, localKey string) (Principal, error) {
	ids, err := memberSectors(ctx, tx, actor)
	if err != nil {
		return Principal{}, err
	}
	caller := actor
	caller.SectorIDs = ids
	if err := Authorize(caller, conn); err != nil {
		return Principal{}, fail(ErrDenied, OutcomeDenied, "sector", localKey)
	}
	if conn.SectorID != uuid.Nil && actor.Role == "agent" && conn.SectorID != row.Sector {
		return Principal{}, fail(ErrDenied, OutcomeDenied, "sector", localKey)
	}
	return caller, nil
}

func safeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 || strings.ContainsAny(key, " \t\r\n") {
		return ""
	}
	return key
}

// replayedAction drops anything this attempt wrote and returns the stored pair.
func replayedAction(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, prior Action, send bool, clientRequest string) (Result, error) {
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT erp_panel"); err != nil {
		return Result{}, err
	}
	if send && prior.Outcome == OutcomeOK {
		if prior.ClientRequestID != clientRequest || prior.OutboxOperation != outboxOperation {
			return Result{}, fail(ErrConflict, "conflict", "local_key", prior.LocalKey)
		}
		if err := ConfirmOutbox(ctx, tx, tenant, prior); err != nil {
			if errors.Is(err, ErrConflict) {
				return Result{}, fail(ErrConflict, "conflict", "outbox", prior.LocalKey)
			}
			return Result{}, err
		}
		return Result{View: prior.View, Replayed: true, MessageID: prior.MessageID, AttemptID: prior.AttemptID}, nil
	}
	return Result{View: prior.View, Replayed: true}, nil
}
