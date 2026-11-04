package bothook

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Service is the hook. Fake, when set, replaces the quiet in-process bot.
// Now, when set, is the clock for offers and replies. ExpireDue uses its own now.
type Service struct {
	DB   *db.Store
	Fake Fake
	Now  func() time.Time
}

// New binds the application store. The clock and the fake are optional.
func New(database *db.Store) Service { return Service{DB: database} }

func (s Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (s Service) engine() Fake {
	if s.Fake != nil {
		return s.Fake
	}
	return QuietFake{}
}

// ParseToken splits v1.<tenant>.<secret>. The secret is the only authenticator.
func ParseToken(raw string) (uuid.UUID, string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[2] == "" {
		return uuid.Nil, "", ErrUnauthenticated
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, "", ErrUnauthenticated
	}
	return id, parts[2], nil
}

func issueToken(tenant uuid.UUID) (raw, prefix string, hash []byte, err error) {
	secret, sum, err := auth.NewSecret()
	if err != nil {
		return "", "", nil, ErrUnavailable
	}
	prefix = secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return "v1." + tenant.String() + "." + secret, prefix, sum, nil
}

// Install creates the only hook of the inbox and reveals the token once.
func (s Service) Install(ctx context.Context, p Principal, inbox uuid.UUID, cmd InstallCommand) (Installation, error) {
	var out Installation
	if err := p.write(); err != nil {
		return out, err
	}
	if inbox == uuid.Nil || cmd.RequestID == uuid.Nil || cmd.DeadlineSeconds < 30 || cmd.DeadlineSeconds > 86400 {
		return out, ErrInvalid
	}
	sum, err := digest(struct {
		Inbox uuid.UUID
		Cmd   InstallCommand
	}{inbox, cmd})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireInbox(ctx, tx, p.sector(), inbox); err != nil {
			return err
		}
		if err := lockCommand(ctx, tx, p.TenantID, p.MembershipID, "install", cmd.RequestID); err != nil {
			return err
		}
		if hit, err := readCommand(ctx, tx, p.TenantID, p.MembershipID, "install", cmd.RequestID, sum, &out); hit || err != nil {
			return err
		}
		var existing int
		err := tx.QueryRow(ctx, `SELECT 1 FROM bot_hooks WHERE tenant_id=$1 AND inbox_id=$2`, p.TenantID, inbox).Scan(&existing)
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cmd.ExpectedVersion != 0 {
			return ErrConflict
		}
		raw, prefix, hash, err := issueToken(p.TenantID)
		if err != nil {
			return err
		}
		now := s.clock()
		if _, err = tx.Exec(ctx, `INSERT INTO bot_hooks (tenant_id, inbox_id, token_hash, token_prefix, deadline_seconds, enabled, switch_generation, version, simulation, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,true,1,1,'in-process',$6,$6)`, p.TenantID, inbox, hash, prefix, cmd.DeadlineSeconds, now); err != nil {
			return mapWrite(err)
		}
		out = Installation{InboxID: inbox, TokenPrefix: prefix, DeadlineSeconds: cmd.DeadlineSeconds, Enabled: true, Version: 1, Simulation: SimulationInProcess}
		if err = saveCommand(ctx, tx, p.TenantID, p.MembershipID, "install", cmd.RequestID, sum, out); err != nil {
			return err
		}
		out.Token = raw
		out.TokenRevealed = true
		return nil
	})
	return out, err
}

// Rotate replaces the token. The stored replay does not contain the new secret.
func (s Service) Rotate(ctx context.Context, p Principal, inbox uuid.UUID, cmd RotateCommand) (Installation, error) {
	var out Installation
	if err := p.write(); err != nil {
		return out, err
	}
	if inbox == uuid.Nil || cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 1 {
		return out, ErrInvalid
	}
	sum, err := digest(struct {
		Inbox uuid.UUID
		Cmd   RotateCommand
	}{inbox, cmd})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireInbox(ctx, tx, p.sector(), inbox); err != nil {
			return err
		}
		if err := lockCommand(ctx, tx, p.TenantID, p.MembershipID, "rotate", cmd.RequestID); err != nil {
			return err
		}
		if hit, err := readCommand(ctx, tx, p.TenantID, p.MembershipID, "rotate", cmd.RequestID, sum, &out); hit || err != nil {
			return err
		}
		raw, prefix, hash, err := issueToken(p.TenantID)
		if err != nil {
			return err
		}
		now := s.clock()
		err = tx.QueryRow(ctx, `UPDATE bot_hooks SET token_hash=$4, token_prefix=$5, version=version+1, updated_at=$6 WHERE tenant_id=$1 AND inbox_id=$2 AND version=$3 RETURNING deadline_seconds, enabled, version`, p.TenantID, inbox, cmd.ExpectedVersion, hash, prefix, now).Scan(&out.DeadlineSeconds, &out.Enabled, &out.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		out.InboxID = inbox
		out.TokenPrefix = prefix
		out.Simulation = SimulationInProcess
		if err = saveCommand(ctx, tx, p.TenantID, p.MembershipID, "rotate", cmd.RequestID, sum, out); err != nil {
			return err
		}
		out.Token = raw
		out.TokenRevealed = true
		return nil
	})
	return out, err
}

// SetSwitch turns the hook on or off. Off increments the epoch of every bot conversation
// of the inbox, cancels queued automatic sends of that possession and returns them to the queue.
func (s Service) SetSwitch(ctx context.Context, p Principal, inbox uuid.UUID, cmd SwitchCommand) (SwitchResult, error) {
	var out SwitchResult
	if err := p.write(); err != nil {
		return out, err
	}
	if inbox == uuid.Nil || cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 1 {
		return out, ErrInvalid
	}
	sum, err := digest(struct {
		Inbox uuid.UUID
		Cmd   SwitchCommand
	}{inbox, cmd})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockCommand(ctx, tx, p.TenantID, p.MembershipID, "switch", cmd.RequestID); err != nil {
			return err
		}
		if hit, err := readCommand(ctx, tx, p.TenantID, p.MembershipID, "switch", cmd.RequestID, sum, &out); hit || err != nil {
			return err
		}
		if err := lockTenant(ctx, tx, p.TenantID, true); err != nil {
			return err
		}
		if err := sectors.RequireInbox(ctx, tx, p.sector(), inbox); err != nil {
			return err
		}
		now := s.clock()
		err := tx.QueryRow(ctx, `UPDATE bot_hooks SET enabled=$4, version=version+1, switch_generation=CASE WHEN $4 THEN switch_generation ELSE switch_generation+1 END, updated_at=$5 WHERE tenant_id=$1 AND inbox_id=$2 AND version=$3 RETURNING version, enabled`, p.TenantID, inbox, cmd.ExpectedVersion, cmd.Enabled, now).Scan(&out.Version, &out.Enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		out.InboxID = inbox
		if !cmd.Enabled {
			n, err := releaseInbox(ctx, tx, p.TenantID, inbox, now)
			if err != nil {
				return err
			}
			out.Released = n
		}
		return saveCommand(ctx, tx, p.TenantID, p.MembershipID, "switch", cmd.RequestID, sum, out)
	})
	return out, err
}

// Get reads the hook. The token is never included.
func (s Service) Get(ctx context.Context, p Principal, inbox uuid.UUID) (Hook, error) {
	var out Hook
	if err := p.read(); err != nil {
		return out, err
	}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireInbox(ctx, tx, p.sector(), inbox); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT inbox_id, token_prefix, deadline_seconds, enabled, version, simulation FROM bot_hooks WHERE tenant_id=$1 AND inbox_id=$2`, p.TenantID, inbox).Scan(&out.InboxID, &out.TokenPrefix, &out.DeadlineSeconds, &out.Enabled, &out.Version, &out.Simulation)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return out, err
}

// Notices lists automation pending items, including uncertain results. It does not drop uncertain rows.
func (s Service) Notices(ctx context.Context, p Principal, inbox uuid.UUID) (NoticeList, error) {
	out := NoticeList{Notices: []Notice{}}
	if err := p.read(); err != nil {
		return out, err
	}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.RequireInbox(ctx, tx, p.sector(), inbox); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, conversation_id, state, notice, granted_epoch FROM bot_sessions WHERE tenant_id=$1 AND inbox_id=$2 AND notice <> '' AND state IN ('pending','received','uncertain','expired','unavailable','switched_off') ORDER BY updated_at DESC, id`, p.TenantID, inbox)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Notice
			if err := rows.Scan(&item.ID, &item.ConversationID, &item.State, &item.Notice, &item.GrantedEpoch); err != nil {
				return err
			}
			out.Notices = append(out.Notices, item)
		}
		return rows.Err()
	})
	return out, err
}

// Offer grants the inbox bot possession of one conversation and then runs the in-process fake
// outside the transaction. A second call for the same open possession does not call the fake again.
func (s Service) Offer(ctx context.Context, tenant, conversation uuid.UUID) (Session, error) {
	var session Session
	var invoke bool
	if s.DB == nil || tenant == uuid.Nil || conversation == uuid.Nil {
		return session, ErrUnavailable
	}
	err := s.tx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, tenant, false); err != nil {
			return err
		}
		row, err := lockConversation(ctx, tx, tenant, conversation)
		if err != nil {
			return err
		}
		if row.Status == "closed" || row.Controller == "human" {
			return ErrConflict
		}
		hook, err := lockHook(ctx, tx, tenant, row.InboxID, false)
		if err != nil {
			return err
		}
		if !hook.Enabled {
			return ErrDisabled
		}
		if row.Controller == "bot" {
			existing, ok, err := openSession(ctx, tx, tenant, conversation)
			if err != nil || ok {
				session = existing
				return err
			}
		}
		now := s.clock()
		var epoch int64
		if err = tx.QueryRow(ctx, `UPDATE conversations SET controller='bot', controller_epoch=controller_epoch+1, assignee_id=NULL, status='triage', version=version+1, updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND status<>'closed' AND controller<>'human' RETURNING controller_epoch`, tenant, conversation, now).Scan(&epoch); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		session = Session{
			ID: uuid.New(), TenantID: tenant, InboxID: row.InboxID, ConversationID: conversation,
			GrantedEpoch: epoch, DeadlineAt: now.Add(time.Duration(hook.DeadlineSeconds) * time.Second),
			State: statePending, Notice: noticePending,
		}
		_, err = tx.Exec(ctx, `INSERT INTO bot_sessions (tenant_id, id, inbox_id, conversation_id, granted_epoch, deadline_at, state, notice, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$8,$8)`, tenant, session.ID, session.InboxID, conversation, epoch, session.DeadlineAt, noticePending, now)
		if err != nil {
			return mapWrite(err)
		}
		invoke = true
		return nil
	})
	if err != nil || !invoke {
		return session, err
	}
	result, ferr := s.engine().Handle(ctx, Delivery{
		TenantID: tenant, InboxID: session.InboxID, ConversationID: conversation,
		Epoch: session.GrantedEpoch, DeadlineAt: session.DeadlineAt,
	})
	if ferr != nil {
		result = FakeResult{Action: actionUncertain}
	}
	return s.apply(ctx, session, result)
}

// Reply queues one bot message when the token and the granted epoch are still current.
func (s Service) Reply(ctx context.Context, token string, conversation uuid.UUID, cmd ReplyCommand) (ReplyResult, error) {
	var out ReplyResult
	tenant, secret, err := ParseToken(token)
	if err != nil {
		return out, err
	}
	body := strings.TrimSpace(cmd.Body)
	if conversation == uuid.Nil || cmd.RequestID == uuid.Nil || cmd.Epoch < 1 || body == "" || len(body) > 4000 {
		return out, ErrInvalid
	}
	cmd.Body = body
	sum, err := digest(struct {
		Conversation uuid.UUID
		Cmd          ReplyCommand
	}{conversation, cmd})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		row, hook, err := authorizeBot(ctx, tx, tenant, conversation, secret)
		if err != nil {
			return err
		}
		if err := lockCommand(ctx, tx, tenant, row.InboxID, "reply", cmd.RequestID); err != nil {
			return err
		}
		if hit, err := readCommand(ctx, tx, tenant, row.InboxID, "reply", cmd.RequestID, sum, &out); hit || err != nil {
			return err
		}
		if row.Controller != "bot" || row.Epoch != cmd.Epoch {
			return ErrEpoch
		}
		id, err := queueBotText(ctx, tx, tenant, row, cmd.Epoch, body, "public", s.clock())
		if err != nil {
			return err
		}
		if err = touchDeadline(ctx, tx, tenant, conversation, cmd.Epoch, s.clock(), hook.DeadlineSeconds); err != nil {
			return err
		}
		out = ReplyResult{MessageID: id, Epoch: cmd.Epoch, State: "queued"}
		return saveCommand(ctx, tx, tenant, row.InboxID, "reply", cmd.RequestID, sum, out)
	})
	return out, err
}

// Handoff moves the conversation to the human queue of an authorized sector.
// An empty sector roster does not block the transfer.
func (s Service) Handoff(ctx context.Context, token string, conversation uuid.UUID, cmd HandoffCommand) (HandoffResult, error) {
	var out HandoffResult
	tenant, secret, err := ParseToken(token)
	if err != nil {
		return out, err
	}
	reason := strings.TrimSpace(cmd.Reason)
	note := strings.TrimSpace(cmd.Note)
	if conversation == uuid.Nil || cmd.RequestID == uuid.Nil || cmd.Epoch < 1 || cmd.SectorID == uuid.Nil || reason == "" || len(reason) > 500 || len(note) > 500 {
		return out, ErrInvalid
	}
	cmd.Reason, cmd.Note = reason, note
	sum, err := digest(struct {
		Conversation uuid.UUID
		Cmd          HandoffCommand
	}{conversation, cmd})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		row, _, err := authorizeBot(ctx, tx, tenant, conversation, secret)
		if err != nil {
			return err
		}
		if err := lockCommand(ctx, tx, tenant, row.InboxID, "handoff", cmd.RequestID); err != nil {
			return err
		}
		if hit, err := readCommand(ctx, tx, tenant, row.InboxID, "handoff", cmd.RequestID, sum, &out); hit || err != nil {
			return err
		}
		if row.Controller != "bot" || row.Epoch != cmd.Epoch {
			return ErrEpoch
		}
		out, err = handoffTx(ctx, tx, tenant, row, cmd.Epoch, cmd.SectorID, reason, note, s.clock())
		if err != nil {
			return err
		}
		return saveCommand(ctx, tx, tenant, row.InboxID, "handoff", cmd.RequestID, sum, out)
	})
	return out, err
}

// ExpireDue returns due bot possessions of one tenant to the human queue.
// A stored receipt does not count as resolution. now is the job clock.
func (s Service) ExpireDue(ctx context.Context, tenant uuid.UUID, now time.Time) (int, error) {
	if s.DB == nil || tenant == uuid.Nil || now.IsZero() {
		return 0, ErrUnavailable
	}
	now = now.UTC().Truncate(time.Microsecond)
	var released int
	err := s.tx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, tenant, true); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, conversation_id, granted_epoch FROM bot_sessions WHERE tenant_id=$1 AND state IN ('pending','received') AND deadline_at <= $2 ORDER BY conversation_id, id`, tenant, now)
		if err != nil {
			return err
		}
		type due struct {
			id, conversation uuid.UUID
			epoch            int64
		}
		var items []due
		for rows.Next() {
			var item due
			if err := rows.Scan(&item.id, &item.conversation, &item.epoch); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range items {
			row, err := lockConversation(ctx, tx, tenant, item.conversation)
			if err != nil {
				return err
			}
			ok, _, err := releaseBot(ctx, tx, tenant, row, item.epoch, uuid.Nil, now)
			if err != nil {
				return err
			}
			notice := ""
			if ok {
				notice = noticeExpired
				released++
			}
			if _, err = tx.Exec(ctx, `UPDATE bot_sessions SET state='expired', notice=$4, updated_at=$5 WHERE tenant_id=$1 AND id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, tenant, item.id, item.epoch, notice, now); err != nil {
				return err
			}
		}
		return nil
	})
	return released, err
}

func (s Service) apply(ctx context.Context, session Session, result FakeResult) (Session, error) {
	err := s.tx(ctx, session.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockTenant(ctx, tx, session.TenantID, false); err != nil {
			return err
		}
		row, err := lockConversation(ctx, tx, session.TenantID, session.ConversationID)
		if err != nil {
			return err
		}
		current, err := loadSession(ctx, tx, session.TenantID, session.ID)
		if err != nil {
			return err
		}
		if current.State != statePending && current.State != stateReceived {
			session = current
			return nil
		}
		now := s.clock()
		action := result.Action
		if action == "" && !result.Received {
			action = actionUnavailable
		}
		switch action {
		case "", actionReply:
			if action == actionReply {
				body := strings.TrimSpace(result.Body)
				if body == "" {
					body = "Resposta do gancho simulado."
				}
				if _, err = queueBotText(ctx, tx, session.TenantID, row, session.GrantedEpoch, body, "public", now); err != nil {
					if !errors.Is(err, ErrEpoch) {
						return err
					}
					if err = markUncertain(ctx, tx, session.TenantID, row, session, now); err != nil {
						return err
					}
					session.State = stateUncertain
					session.Notice = noticeUncertain
					break
				}
			}
			session.State = stateReceived
			session.Receipt = receiptReceived
			session.Notice = noticeReceived
		case actionHandoff:
			reason := strings.TrimSpace(result.Reason)
			if result.SectorID == uuid.Nil || reason == "" {
				if err = markUncertain(ctx, tx, session.TenantID, row, session, now); err != nil {
					return err
				}
				session.State = stateUncertain
				session.Notice = noticeUncertain
				break
			}
			if _, err = handoffTx(ctx, tx, session.TenantID, row, session.GrantedEpoch, result.SectorID, reason, strings.TrimSpace(result.Note), now); err != nil {
				if errors.Is(err, ErrInvalid) || errors.Is(err, ErrEpoch) {
					if err = markUncertain(ctx, tx, session.TenantID, row, session, now); err != nil {
						return err
					}
					session.State = stateUncertain
					session.Notice = noticeUncertain
					break
				}
				return err
			}
			session.State = stateHandedOff
			session.Notice = ""
		case actionUncertain:
			if err = markUncertain(ctx, tx, session.TenantID, row, session, now); err != nil {
				return err
			}
			session.State = stateUncertain
			session.Notice = noticeUncertain
		default:
			if err = markReturned(ctx, tx, session.TenantID, row, session, stateUnavailable, noticeUnavailable, now); err != nil {
				return err
			}
			session.State = stateUnavailable
			session.Notice = noticeUnavailable
		}
		if session.State == stateHandedOff {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE bot_sessions SET state=$4, receipt=COALESCE($5, receipt), notice=$6, updated_at=$7 WHERE tenant_id=$1 AND id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, session.TenantID, session.ID, session.GrantedEpoch, session.State, emptyNil(session.Receipt), session.Notice, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func markUncertain(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row conv, session Session, now time.Time) error {
	return markReturned(ctx, tx, tenant, row, session, stateUncertain, noticeUncertain, now)
}

func markReturned(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row conv, session Session, state, notice string, now time.Time) error {
	ok, _, err := releaseBot(ctx, tx, tenant, row, session.GrantedEpoch, uuid.Nil, now)
	if err != nil {
		return err
	}
	if !ok && state == stateUncertain {
		notice = noticeUncertain
	}
	_, err = tx.Exec(ctx, `UPDATE bot_sessions SET state=$4, notice=$5, updated_at=$6 WHERE tenant_id=$1 AND id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, tenant, session.ID, session.GrantedEpoch, state, notice, now)
	return err
}

func handoffTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row conv, epoch int64, sector uuid.UUID, reason, note string, now time.Time) (HandoffResult, error) {
	var out HandoffResult
	var linked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sector_inboxes si JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active WHERE si.tenant_id=$1 AND si.inbox_id=$2 AND si.sector_id=$3)`, tenant, row.InboxID, sector).Scan(&linked); err != nil {
		return out, err
	}
	if !linked {
		return out, ErrInvalid
	}
	ok, newEpoch, err := releaseBot(ctx, tx, tenant, row, epoch, sector, now)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, ErrEpoch
	}
	if note != "" {
		if _, err = queueBotText(ctx, tx, tenant, row, newEpoch, note, "internal", now); err != nil {
			return out, err
		}
	}
	id := uuid.New()
	tag, err := tx.Exec(ctx, `UPDATE bot_sessions SET state='handed_off', handoff_id=$4, sector_id=$5, reason=$6, note=NULLIF($7,''), notice='', updated_at=$8 WHERE tenant_id=$1 AND conversation_id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, tenant, row.ID, epoch, id, sector, reason, note, now)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() != 1 {
		return out, ErrEpoch
	}
	out = HandoffResult{HandoffID: id, Epoch: newEpoch, SectorID: sector, Controller: "menu", Status: "open", Notice: noticeHandoff}
	return out, nil
}

func authorizeBot(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, secret string) (conv, hookRow, error) {
	if err := lockTenant(ctx, tx, tenant, false); err != nil {
		return conv{}, hookRow{}, err
	}
	row, err := lockConversation(ctx, tx, tenant, conversation)
	if err != nil {
		return conv{}, hookRow{}, err
	}
	hook, err := lockHook(ctx, tx, tenant, row.InboxID, false)
	if err != nil {
		return conv{}, hookRow{}, err
	}
	if !auth.SecretEqual(hook.Hash, auth.HashSecret(secret)) {
		return conv{}, hookRow{}, ErrUnauthenticated
	}
	if !hook.Enabled {
		return conv{}, hookRow{}, ErrDisabled
	}
	return row, hook, nil
}

func queueBotText(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row conv, epoch int64, body, visibility string, now time.Time) (uuid.UUID, error) {
	if visibility == "public" && (row.Controller != "bot" || row.Epoch != epoch) {
		return uuid.Nil, ErrEpoch
	}
	id := uuid.New()
	local := any(nil)
	if visibility == "public" {
		local = "queued"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO messages (tenant_id, id, conversation_id, connector_id, direction, origin, visibility, body, local_state, controller, controller_epoch, ingested_at) VALUES ($1,$2,$3,$4,'outbound','bot',$5,$6,$7,'bot',$8,$9)`, tenant, id, row.ID, row.Connector, visibility, body, local, epoch, now); err != nil {
		return uuid.Nil, err
	}
	if visibility == "public" {
		if _, err := tx.Exec(ctx, `INSERT INTO outbound_attempts (tenant_id, id, message_id, conversation_id, attempt_no, state, controller, controller_epoch, origin) VALUES ($1,$2,$3,$4,1,'queued','bot',$5,'bot')`, tenant, uuid.New(), id, row.ID, epoch); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

func touchDeadline(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, epoch int64, now time.Time, seconds int) error {
	_, err := tx.Exec(ctx, `UPDATE bot_sessions SET deadline_at=$4, updated_at=$5 WHERE tenant_id=$1 AND conversation_id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, tenant, conversation, epoch, now.Add(time.Duration(seconds)*time.Second), now)
	return err
}

func releaseInbox(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID, now time.Time) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND inbox_id=$2 AND controller='bot' AND status<>'closed' ORDER BY id FOR UPDATE`, tenant, inbox)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var n int
	for _, id := range ids {
		row, err := lockConversation(ctx, tx, tenant, id)
		if err != nil {
			return 0, err
		}
		ok, _, err := releaseBot(ctx, tx, tenant, row, row.Epoch, uuid.Nil, now)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		n++
		if _, err = tx.Exec(ctx, `UPDATE bot_sessions SET state='switched_off', notice=$4, updated_at=$5 WHERE tenant_id=$1 AND conversation_id=$2 AND granted_epoch=$3 AND state IN ('pending','received')`, tenant, id, row.Epoch, noticeSwitched, now); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func releaseBot(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row conv, epoch int64, sector uuid.UUID, now time.Time) (bool, int64, error) {
	if row.Controller != "bot" || row.Epoch != epoch || row.Status == "closed" {
		return false, row.Epoch, nil
	}
	if err := cancelQueued(ctx, tx, tenant, row.ID, epoch); err != nil {
		return false, 0, err
	}
	var newEpoch int64
	err := tx.QueryRow(ctx, `UPDATE conversations SET controller='menu', controller_epoch=controller_epoch+1, assignee_id=NULL, status='open', sector_id=COALESCE($4, sector_id), version=version+1, updated_at=$5 WHERE tenant_id=$1 AND id=$2 AND controller='bot' AND controller_epoch=$3 AND status<>'closed' RETURNING controller_epoch`, tenant, row.ID, epoch, nilUUID(sector), now).Scan(&newEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, row.Epoch, nil
	}
	if err != nil {
		return false, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO conversation_events (tenant_id, id, conversation_id, cycle_number, kind, at, version, controller_epoch, controller) VALUES ($1,$2,$3,$4,'controller_changed',$5,$6,$7,'menu')`, tenant, uuid.New(), row.ID, row.Cycle, now, row.Version+1, newEpoch); err != nil {
		return false, 0, err
	}
	return true, newEpoch, nil
}

func cancelQueued(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, epoch int64) error {
	rows, err := tx.Query(ctx, `SELECT m.id FROM messages m JOIN outbound_attempts a ON a.tenant_id=m.tenant_id AND a.message_id=m.id WHERE m.tenant_id=$1 AND a.conversation_id=$2 AND a.state='queued' AND a.controller_epoch=$3 AND a.origin IN ('bot','menu') ORDER BY m.id FOR UPDATE OF m`, tenant, conversation, epoch)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE outbound_attempts SET state='cancelled', updated_at=now() WHERE tenant_id=$1 AND conversation_id=$2 AND state='queued' AND controller_epoch=$3 AND origin IN ('bot','menu')`, tenant, conversation, epoch); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE messages SET local_state='cancelled' WHERE tenant_id=$1 AND id = ANY($2::uuid[])`, tenant, ids)
	return err
}

type conv struct {
	ID, InboxID, Connector uuid.UUID
	Sector                 *uuid.UUID
	Status, Controller     string
	Epoch, Version         int64
	Cycle                  int
}

type hookRow struct {
	Hash            []byte
	Enabled         bool
	DeadlineSeconds int
	Version         int64
}

func lockConversation(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (conv, error) {
	var row conv
	err := tx.QueryRow(ctx, `SELECT c.id, c.inbox_id, i.connector_id, c.sector_id, c.status, c.controller, c.controller_epoch, c.version, c.cycle_number FROM conversations c JOIN inboxes i ON i.tenant_id=c.tenant_id AND i.id=c.inbox_id WHERE c.tenant_id=$1 AND c.id=$2 FOR UPDATE OF c`, tenant, id).Scan(&row.ID, &row.InboxID, &row.Connector, &row.Sector, &row.Status, &row.Controller, &row.Epoch, &row.Version, &row.Cycle)
	if errors.Is(err, pgx.ErrNoRows) {
		return conv{}, ErrNotFound
	}
	return row, err
}

func lockHook(ctx context.Context, tx pgx.Tx, tenant, inbox uuid.UUID, update bool) (hookRow, error) {
	clause := "FOR SHARE"
	if update {
		clause = "FOR UPDATE"
	}
	var row hookRow
	err := tx.QueryRow(ctx, `SELECT token_hash, enabled, deadline_seconds, version FROM bot_hooks WHERE tenant_id=$1 AND inbox_id=$2 `+clause, tenant, inbox).Scan(&row.Hash, &row.Enabled, &row.DeadlineSeconds, &row.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return hookRow{}, ErrNotFound
	}
	return row, err
}

func openSession(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) (Session, bool, error) {
	var session Session
	err := tx.QueryRow(ctx, `SELECT id, inbox_id, conversation_id, granted_epoch, deadline_at, state, COALESCE(receipt,''), notice FROM bot_sessions WHERE tenant_id=$1 AND conversation_id=$2 AND state IN ('pending','received')`, tenant, conversation).Scan(&session.ID, &session.InboxID, &session.ConversationID, &session.GrantedEpoch, &session.DeadlineAt, &session.State, &session.Receipt, &session.Notice)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	session.TenantID = tenant
	return session, err == nil, err
}

func loadSession(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Session, error) {
	var session Session
	err := tx.QueryRow(ctx, `SELECT id, inbox_id, conversation_id, granted_epoch, deadline_at, state, COALESCE(receipt,''), notice FROM bot_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&session.ID, &session.InboxID, &session.ConversationID, &session.GrantedEpoch, &session.DeadlineAt, &session.State, &session.Receipt, &session.Notice)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	session.TenantID = tenant
	return session, err
}

func (s Service) tx(ctx context.Context, tenant uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
	if s.DB == nil {
		return ErrUnavailable
	}
	return s.DB.WithTenantTx(ctx, tenant, fn)
}

func (p Principal) write() error {
	if p.IsSuperadmin || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil || p.Role != "admin" {
		return ErrForbidden
	}
	return nil
}

func (p Principal) read() error {
	if p.IsSuperadmin || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return ErrForbidden
	}
	switch p.Role {
	case "admin", "supervisor", "agent":
		return nil
	default:
		return ErrForbidden
	}
}

func (p Principal) sector() sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}

func lockTenant(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, exclusive bool) error {
	query := `SELECT pg_advisory_xact_lock_shared(hashtext('bothook-tenant'), hashtext($1::text))`
	if exclusive {
		query = `SELECT pg_advisory_xact_lock(hashtext('bothook-tenant'), hashtext($1::text))`
	}
	_, err := tx.Exec(ctx, query, tenant.String())
	return err
}

func lockCommand(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, operation string, request uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "bothook-command:"+tenant.String()+":"+scope.String()+":"+operation+":"+request.String())
	return err
}

func readCommand(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, operation string, request uuid.UUID, hash []byte, dest any) (bool, error) {
	var got, raw []byte
	err := tx.QueryRow(ctx, `SELECT request_hash, response FROM bot_hook_commands WHERE tenant_id=$1 AND scope_id=$2 AND operation=$3 AND request_id=$4`, tenant, scope, operation, request).Scan(&got, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !auth.SecretEqual(got, hash) {
		return false, ErrConflict
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, ErrUnavailable
	}
	return true, nil
}

func saveCommand(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, operation string, request uuid.UUID, hash []byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO bot_hook_commands (tenant_id, scope_id, operation, request_id, request_hash, response) VALUES ($1,$2,$3,$4,$5,$6)`, tenant, scope, operation, request, hash, raw)
	return mapWrite(err)
}

func digest(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

func mapWrite(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return err
}

func emptyNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nilUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
