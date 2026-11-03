package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/db"
	"chatisp/internal/presence"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Service is the panel entry. Jobs are the package functions that take a clock.
type Service struct {
	DB  *db.Store
	Now func() time.Time
}

// New binds the application store. The clock is optional.
func New(database *db.Store) Service { return Service{DB: database} }

func (s Service) clock() time.Time {
	if s.Now != nil {
		return normalize(s.Now())
	}
	return normalize(time.Now())
}

func normalize(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Microsecond)
}

func sameTime(a, b time.Time) bool {
	return normalize(a).Equal(normalize(b))
}

// Snooze defers one human conversation. It does not close and it is not a status.
// The current assignee is released so the vacancy is free. The membership is kept
// only as the preferred return destination.
func (s Service) Snooze(ctx context.Context, p Principal, conversationID uuid.UUID, cmd SnoozeCommand) (Snooze, error) {
	var out Snooze
	if s.DB == nil {
		return out, ErrUnavailable
	}
	now := s.clock()
	reason, until, err := normalizeSnooze(cmd)
	if err != nil {
		return out, err
	}
	cmd.Reason = reason
	cmd.Until = until
	raw, err := json.Marshal(struct {
		Conversation uuid.UUID
		Command      SnoozeCommand
	}{conversationID, cmd})
	if err != nil {
		return out, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	err = s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// The command key is taken before any conversation lock. Two identical
		// retries then cannot both miss the row and later collide on the version.
		if err := lockCommand(ctx, tx, p, operationSnooze, cmd.RequestID); err != nil {
			return err
		}
		// Exclusive row lock before RequireConversation shares the conversation.
		// Different request keys do not share an advisory, so a share held by both
		// cannot be promoted to FOR UPDATE without a 40P01 deadlock.
		row, err := lockPanelConversation(ctx, tx, p, cmd.InboxID, conversationID)
		if err != nil {
			return err
		}
		if replay, ok, err := readCommand(ctx, tx, p, operationSnooze, cmd.RequestID, hash[:], &out); err != nil || ok {
			return errOr(err, replay)
		}
		if err = validateSnoozeDeadline(until, now); err != nil {
			return err
		}
		if replay, ok, err := readCommand(ctx, tx, p, operationSnooze, cmd.RequestID, hash[:], &out); err != nil || ok {
			return errOr(err, replay)
		}
		if row.Version != cmd.ExpectedVersion {
			return ErrConflict
		}
		if row.Cycle < 1 || (row.Status != "open" && row.Status != "waiting_customer") || row.Controller != "human" || row.Assignee == uuid.Nil {
			return ErrConflict
		}
		if p.Role == "agent" && row.Assignee != p.MembershipID {
			return ErrForbidden
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active')`, p.TenantID, conversationID).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
		out = Snooze{
			ID: uuid.New(), ConversationID: conversationID, Until: until, Reason: reason,
			DestinationMembershipID: row.Assignee, DestinationSectorID: row.Sector,
			State: stateActive, ConversationStatus: row.Status, Version: row.Version,
		}
		_, err = tx.Exec(ctx, `INSERT INTO conversation_snoozes(tenant_id,id,conversation_id,snoozed_until,reason,destination_membership_id,destination_sector_id,state,created_by_membership_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8,$9)`,
			p.TenantID, out.ID, conversationID, until, reason, row.Assignee, row.Sector, p.MembershipID, now)
		if err != nil {
			return err
		}
		// The snooze row is visible to the queue trigger before the assignee is cleared.
		updated, err := (store.Repo{}).ApplyAssignment(ctx, tx, p.TenantID, conversationID, p.UserID, uuid.Nil, uuid.Nil, row.Version, "unassigned", now)
		if err != nil {
			return err
		}
		out.Version = updated.Version
		out.ConversationStatus = updated.Status
		out.AssigneeID = nil
		if err = insertEvent(ctx, tx, p.TenantID, conversationID, updated.CycleNumber, "snoozed", p.UserID, now, updated.Version, updated.ControllerEpoch, updated.Controller, uuid.Nil); err != nil {
			return err
		}
		if err = cancelWatch(ctx, tx, p.TenantID, conversationID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,'conversation.snooze','conversation',$5,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, conversationID); err != nil {
			return err
		}
		return writeCommand(ctx, tx, p, operationSnooze, cmd.RequestID, hash[:], out)
	})
	return out, mapErr(err)
}

// Get returns the active snooze. A cleared deferral is not found.
func (s Service) Get(ctx context.Context, p Principal, inboxID, conversationID uuid.UUID) (Snooze, error) {
	var out Snooze
	if s.DB == nil {
		return out, ErrUnavailable
	}
	if inboxID == uuid.Nil || conversationID == uuid.Nil {
		return out, ErrInvalid
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		conv, err := lockPanelConversation(ctx, tx, p, inboxID, conversationID)
		if err != nil {
			return err
		}
		row, err := lockActiveSnooze(ctx, tx, p.TenantID, conversationID, false)
		if err != nil {
			return err
		}
		if row.ID == uuid.Nil {
			return ErrNotFound
		}
		out = snoozeView(conversationID, row, conv)
		return nil
	})
	return out, mapErr(err)
}

// GetInactivity returns the stored delays or the defaults at version 0.
func (s Service) GetInactivity(ctx context.Context, p Principal) (InactivitySettings, error) {
	var out InactivitySettings
	if s.DB == nil {
		return out, ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, p); err != nil {
			return err
		}
		var err error
		out, err = loadSettings(ctx, tx, p.TenantID, false)
		return err
	})
	return out, mapErr(err)
}

// PutInactivity replaces the tenant delays. Only an admin may write.
func (s Service) PutInactivity(ctx context.Context, p Principal, cmd InactivityCommand) (InactivitySettings, error) {
	var out InactivitySettings
	if s.DB == nil {
		return out, ErrUnavailable
	}
	if cmd.RequestID == uuid.Nil || cmd.ExpectedVersion < 0 {
		return out, ErrInvalid
	}
	warn := time.Duration(cmd.WarningAfterSeconds) * time.Second
	closer := time.Duration(cmd.CloseAfterSeconds) * time.Second
	if warn < MinDelay || warn > MaxDelay || closer < MinDelay || closer > MaxDelay {
		return out, ErrInvalid
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		return out, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	err = s.DB.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.DB.LockTenant(ctx); err != nil {
			return err
		}
		if err := sectors.ValidatePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if p.Role != "admin" {
			return ErrForbidden
		}
		if replay, ok, err := readCommand(ctx, tx, p, operationInactivity, cmd.RequestID, hash[:], &out); err != nil || ok {
			return errOr(err, replay)
		}
		current, err := loadSettings(ctx, tx, p.TenantID, true)
		if err != nil {
			return err
		}
		if current.Version != cmd.ExpectedVersion {
			return ErrConflict
		}
		out = InactivitySettings{Version: current.Version + 1, Configured: true, WarningAfterSeconds: cmd.WarningAfterSeconds, CloseAfterSeconds: cmd.CloseAfterSeconds}
		_, err = tx.Exec(ctx, `INSERT INTO lifecycle_inactivity_settings(tenant_id,warning_after_seconds,close_after_seconds,version) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id) DO UPDATE SET warning_after_seconds=EXCLUDED.warning_after_seconds, close_after_seconds=EXCLUDED.close_after_seconds, version=EXCLUDED.version`, p.TenantID, cmd.WarningAfterSeconds, cmd.CloseAfterSeconds, out.Version)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,'lifecycle.inactivity','tenant',$5,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, p.TenantID); err != nil {
			return err
		}
		return writeCommand(ctx, tx, p, operationInactivity, cmd.RequestID, hash[:], out)
	})
	return out, mapErr(err)
}

// ReturnDue releases every active snooze whose deadline is at or before now.
// Each conversation runs in its own transaction so a capacity advisory taken
// for one return is committed before the next conversation is locked.
// It never closes the conversation.
func (s Service) ReturnDue(ctx context.Context, tenant uuid.UUID, now time.Time) ([]Release, error) {
	if s.DB == nil {
		return nil, ErrUnavailable
	}
	var out []Release
	for {
		var batch []Release
		err := s.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			batch, err = ReturnDue(ctx, tx, tenant, now)
			return err
		})
		if err != nil {
			return out, mapErr(err)
		}
		if len(batch) == 0 {
			return out, nil
		}
		if !batch[0].Cleared {
			continue
		}
		out = append(out, batch[0])
	}
}

// RunInactivity arms watches and records warning requests inside one tenant transaction.
// It closes only a generation whose public warning was confirmed earlier.
func (s Service) RunInactivity(ctx context.Context, tenant uuid.UUID, now time.Time) ([]Effect, error) {
	var out []Effect
	if s.DB == nil {
		return nil, ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = RunInactivity(ctx, tx, tenant, now)
		return err
	})
	return out, mapErr(err)
}

// ConfirmWarning stores the delivery result of one warning generation.
// The receipt is idempotent. A different outcome for the same generation conflicts.
func (s Service) ConfirmWarning(ctx context.Context, tenant uuid.UUID, receipt WarningReceipt, now time.Time) (Effect, error) {
	var out Effect
	if s.DB == nil {
		return out, ErrUnavailable
	}
	err := s.DB.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = ConfirmWarning(ctx, tx, tenant, receipt, now)
		return err
	})
	return out, mapErr(err)
}

func errOr(err error, replay bool) error {
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	return nil
}

func normalizeSnooze(cmd SnoozeCommand) (string, time.Time, error) {
	if cmd.RequestID == uuid.Nil || cmd.InboxID == uuid.Nil || cmd.ExpectedVersion < 1 {
		return "", time.Time{}, ErrInvalid
	}
	reason := strings.TrimSpace(cmd.Reason)
	if reason == "" || len(reason) > 500 || strings.ContainsRune(reason, 0) {
		return "", time.Time{}, ErrInvalid
	}
	until := normalize(cmd.Until)
	if until.IsZero() {
		return "", time.Time{}, ErrInvalid
	}
	return reason, until, nil
}

func validateSnoozeDeadline(until, now time.Time) error {
	if now.IsZero() || !until.After(now) || until.After(now.Add(366*24*time.Hour)) {
		return ErrInvalid
	}
	return nil
}

type convRow struct {
	Status      string
	Controller  string
	Assignee    uuid.UUID
	Sector      uuid.UUID
	Inbox       uuid.UUID
	Version     int64
	Epoch       int64
	Cycle       int32
	Anchor      time.Time
	CustomerAt  time.Time
	HasAnchor   bool
	HasCustomer bool
}

type snoozeRow struct {
	ID          uuid.UUID
	Until       time.Time
	Reason      string
	Destination uuid.UUID
	Sector      uuid.UUID
	Created     time.Time
	State       string
}

// lockPanelConversation takes the tenant gate and the exclusive conversation
// row before RequireConversation shares that row. A missing row still goes
// through authorization, which denies it, so the caller does not learn that
// an invisible conversation exists.
func lockPanelConversation(ctx context.Context, tx pgx.Tx, p Principal, inboxID, conversationID uuid.UUID) (convRow, error) {
	if err := sectors.ValidatePrincipal(ctx, tx, p); err != nil {
		return convRow{}, err
	}
	row, err := lockConversation(ctx, tx, p.TenantID, conversationID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return convRow{}, err
	}
	if authErr := sectors.RequireConversation(ctx, tx, p, inboxID, conversationID); authErr != nil {
		return convRow{}, authErr
	}
	return row, err
}

// lockConversation is the exclusive conversation lock. Every path in this
// package takes it before a FOR SHARE of the same row: panel commands do that
// inside lockPanelConversation, and snooze return and inactivity call it
// before the snooze row, the watch and any membership share.
func lockConversation(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (convRow, error) {
	var row convRow
	var assignee, sector, inbox *uuid.UUID
	var anchor, customer *time.Time
	err := tx.QueryRow(ctx, `SELECT status, controller, assignee_id, sector_id, inbox_id, version, controller_epoch, cycle_number, last_eligible_human_reply_at, last_customer_message_at FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-conv */`, tenant, id).Scan(&row.Status, &row.Controller, &assignee, &sector, &inbox, &row.Version, &row.Epoch, &row.Cycle, &anchor, &customer)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, store.ErrNotFound
	}
	if err != nil {
		return row, err
	}
	if assignee != nil {
		row.Assignee = *assignee
	}
	if sector != nil {
		row.Sector = *sector
	}
	if inbox != nil {
		row.Inbox = *inbox
	}
	if anchor != nil {
		row.Anchor = anchor.UTC()
		row.HasAnchor = true
	}
	if customer != nil {
		row.CustomerAt = customer.UTC()
		row.HasCustomer = true
	}
	return row, nil
}

func lockActiveSnooze(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, update bool) (snoozeRow, error) {
	query := `SELECT id, snoozed_until, reason, destination_membership_id, destination_sector_id, created_at, state FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active'`
	if update {
		query += ` FOR UPDATE`
	}
	var row snoozeRow
	err := tx.QueryRow(ctx, query, tenant, conversation).Scan(&row.ID, &row.Until, &row.Reason, &row.Destination, &row.Sector, &row.Created, &row.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return snoozeRow{}, nil
	}
	return row, err
}

func snoozeView(conversation uuid.UUID, row snoozeRow, conv convRow) Snooze {
	view := Snooze{
		ID: row.ID, ConversationID: conversation, Until: row.Until.UTC(), Reason: row.Reason,
		DestinationMembershipID: row.Destination, DestinationSectorID: row.Sector,
		State: row.State, ConversationStatus: conv.Status, Version: conv.Version,
	}
	if conv.Assignee != uuid.Nil {
		assignee := conv.Assignee
		view.AssigneeID = &assignee
	}
	return view
}

func pinTenant(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	if tx == nil || tenant == uuid.Nil {
		return ErrInvalid
	}
	var scope string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&scope); err != nil {
		return err
	}
	if scope != tenant.String() {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String()); err != nil {
		return err
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "active") {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return ErrForbidden
	}
	return err
}

func loadSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, update bool) (InactivitySettings, error) {
	out := InactivitySettings{WarningAfterSeconds: int(DefaultWarningAfter / time.Second), CloseAfterSeconds: int(DefaultCloseAfter / time.Second)}
	query := `SELECT version, warning_after_seconds, close_after_seconds FROM lifecycle_inactivity_settings WHERE tenant_id=$1`
	if update {
		query += ` FOR UPDATE`
	} else {
		query += ` FOR SHARE`
	}
	err := tx.QueryRow(ctx, query, tenant).Scan(&out.Version, &out.WarningAfterSeconds, &out.CloseAfterSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return InactivitySettings{}, err
	}
	out.Configured = true
	return out, nil
}

func lockCommand(ctx context.Context, tx pgx.Tx, p Principal, operation string, request uuid.UUID) error {
	key := p.TenantID.String() + ":" + p.MembershipID.String() + ":" + operation + ":" + request.String()
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('lifecycle-command:' || $1, 0))`, key)
	return err
}

func readCommand(ctx context.Context, tx pgx.Tx, p Principal, operation string, request uuid.UUID, hash []byte, dest any) (bool, bool, error) {
	var previous, response []byte
	err := tx.QueryRow(ctx, `SELECT request_hash, response FROM lifecycle_commands WHERE tenant_id=$1 AND membership_id=$2 AND operation=$3 AND request_id=$4`, p.TenantID, p.MembershipID, operation, request).Scan(&previous, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !bytes.Equal(previous, hash) {
		return false, false, ErrConflict
	}
	if err = json.Unmarshal(response, dest); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func writeCommand(ctx context.Context, tx pgx.Tx, p Principal, operation string, request uuid.UUID, hash []byte, body any) error {
	response, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO lifecycle_commands(tenant_id,membership_id,operation,request_id,request_hash,response) VALUES($1,$2,$3,$4,$5,$6)`, p.TenantID, p.MembershipID, operation, request, hash, response)
	return err
}

func insertEvent(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, cycle int32, kind string, actor uuid.UUID, at time.Time, version, epoch int64, controller string, assignee uuid.UUID) error {
	if cycle < 1 {
		return ErrConflict
	}
	_, err := tx.Exec(ctx, `INSERT INTO conversation_events(tenant_id,id,conversation_id,cycle_number,kind,actor_id,at,version,controller_epoch,controller,assignee_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		tenant, uuid.New(), conversation, cycle, kind, uuidArg(actor), at, version, epoch, controller, uuidArg(assignee))
	return err
}

func uuidArg(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func cancelWatch(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inactivity_watches SET warning_state='cancelled', generation=generation+1, close_due_at=NULL, closed_at=NULL WHERE tenant_id=$1 AND conversation_id=$2 AND warning_state<>'cancelled' AND closed_at IS NULL`, tenant, conversation)
	return err
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, sectors.ErrForbidden), errors.Is(err, presence.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, sectors.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrConflict), errors.Is(err, store.ErrStaleVersion), errors.Is(err, store.ErrClosed), errors.Is(err, store.ErrIdempotencyConflict):
		return ErrConflict
	case errors.Is(err, ErrInvalid), errors.Is(err, store.ErrInvalid), errors.Is(err, presence.ErrInvalid):
		return ErrInvalid
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23514") {
		return ErrConflict
	}
	return err
}
