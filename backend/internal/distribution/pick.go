package distribution

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/presence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lock order inside the caller transaction:
//  1. shared tenant advisory, the same key used by presence and attendance
//  2. shared sector-auto advisory, then distribution_sector_auto when a row exists
//  3. distribution_settings share on Queue; Pick then takes the sector cursor
//  4. every queued conversation of that sector inbox, in conversation id order
//  5. a new read of priority and queue start, after those conversation locks
//  6. sector, inbox link and one candidate membership, then presence.Effective
//  7. assignment capacity advisory, then ApplyAssignment and the realtime cursor
//
// PutSectorAuto and PutWaitAlert take the exclusive tenant advisory first.
// PutSectorAuto then takes the exclusive sector-auto advisory before the sector share.
// Steps 6 and 7 run inside a savepoint. A candidate who is offline or full is
// rolled back so those locks are not kept while the ring tries the next person.
// The conversation locks stay outside the savepoint, before any membership share,
// which is the order already used by manual transfer.

// Pick chooses one available sector member under the vacancy limit, advances the
// sector cursor and reserves that vacancy. Nobody available, or nobody under the
// limit, returns a queued result and does not move the cursor.
func Pick(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Result, error) {
	var out Result
	now = normalize(now)
	if tx == nil || tenant == uuid.Nil || sector == uuid.Nil || inbox == uuid.Nil || now.IsZero() {
		return out, ErrInvalid
	}
	if err := pinScope(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return out, err
	}
	found, active, linked, err := lookupSector(ctx, tx, tenant, sector, inbox, false)
	if err != nil {
		return out, err
	}
	if !found || !active {
		return queued(ReasonSectorInactive), nil
	}
	if !linked {
		return queued(ReasonInboxUnlinked), nil
	}
	auto, err := readSectorAuto(ctx, tx, tenant, sector)
	if err != nil {
		return out, err
	}
	if !auto.Enabled {
		return queued(ReasonAutoDisabled), nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO distribution_cursors(tenant_id,sector_id,version) VALUES($1,$2,1) ON CONFLICT(tenant_id,sector_id) DO NOTHING`, tenant, sector); err != nil {
		return out, err
	}
	var missing bool
	var last uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT last_membership_id IS NULL, COALESCE(last_membership_id, '00000000-0000-0000-0000-000000000000'::uuid) FROM distribution_cursors WHERE tenant_id=$1 AND sector_id=$2 FOR UPDATE`, tenant, sector).Scan(&missing, &last); err != nil {
		return out, err
	}
	if missing {
		last = uuid.Nil
	}
	head, ok, err := lockHead(ctx, tx, tenant, sector, inbox)
	if err != nil {
		return out, err
	}
	if !ok {
		return queued(ReasonEmpty), nil
	}
	found, active, linked, err = lookupSector(ctx, tx, tenant, sector, inbox, true)
	if err != nil {
		return out, err
	}
	if !found || !active {
		return queued(ReasonSectorInactive), nil
	}
	if !linked {
		return queued(ReasonInboxUnlinked), nil
	}
	candidates, err := loadCandidates(ctx, tx, tenant, sector, inbox)
	if err != nil {
		return out, err
	}
	if len(candidates) == 0 {
		return queuedHead(ReasonNobodyAvailable, head), nil
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT distribution_try`); err != nil {
		return out, err
	}
	sawAssignable := false
	for _, cand := range rotate(candidates, last) {
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT distribution_try`); err != nil {
			return out, err
		}
		kept, err := lockCandidate(ctx, tx, tenant, sector, inbox, cand.Member)
		if err != nil {
			return out, err
		}
		if !kept {
			continue
		}
		snap, err := presence.Effective(ctx, tx, tenant, cand.User, now)
		if err != nil {
			return out, err
		}
		if !snap.Assignable || snap.MembershipID != cand.Member {
			continue
		}
		err = store.LockAssignmentCapacity(ctx, tx, tenant, cand.Member, head.ConversationID)
		if errors.Is(err, store.ErrCapacityFull) {
			sawAssignable = true
			continue
		}
		if err != nil {
			return out, err
		}
		sawAssignable = true
		if _, err = (store.Repo{}).ApplyAssignment(ctx, tx, tenant, head.ConversationID, snap.UserID, cand.Member, uuid.Nil, head.Version, "distributed", now); err != nil {
			return out, err
		}
		tag, err := tx.Exec(ctx, `UPDATE distribution_cursors SET last_membership_id=$3, version=version+1 WHERE tenant_id=$1 AND sector_id=$2`, tenant, sector, cand.Member)
		if err != nil {
			return out, err
		}
		if tag.RowsAffected() != 1 {
			return out, ErrConflict
		}
		return Result{
			Status:         StatusAssigned,
			Reason:         ReasonAssigned,
			ConversationID: head.ConversationID,
			MembershipID:   cand.Member,
			UserID:         snap.UserID,
			Priority:       head.Priority,
		}, nil
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT distribution_try`); err != nil {
		return out, err
	}
	if sawAssignable {
		return queuedHead(ReasonCapacityFull, head), nil
	}
	return queuedHead(ReasonNobodyAvailable, head), nil
}

// Queue lists the unassigned open set. Urgent rows come first. Inside one
// priority the order is the queue interval start, then the conversation id.
func Queue(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (QueueView, error) {
	var out QueueView
	now = normalize(now)
	if tx == nil || tenant == uuid.Nil || sector == uuid.Nil || inbox == uuid.Nil || now.IsZero() {
		return out, ErrInvalid
	}
	if err := pinScope(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return out, err
	}
	auto, err := readSectorAuto(ctx, tx, tenant, sector)
	if err != nil {
		return out, err
	}
	out.AutoAssign = auto.Enabled
	settings, err := loadSettings(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	out.Settings = settings
	found, active, linked, err := lookupSector(ctx, tx, tenant, sector, inbox, true)
	if err != nil {
		return out, err
	}
	if !found || !active {
		out.Reason = ReasonSectorInactive
		out.Items = []Item{}
		return out, nil
	}
	if !linked {
		out.Reason = ReasonInboxUnlinked
		out.Items = []Item{}
		return out, nil
	}
	rows, err := listQueue(ctx, tx, tenant, sector, inbox)
	if err != nil {
		return out, err
	}
	out.Reason = ReasonReady
	out.Items = make([]Item, 0, len(rows))
	limit := time.Duration(settings.WaitAlertSeconds) * time.Second
	for _, row := range rows {
		out.Items = append(out.Items, Item{
			ConversationID: row.ConversationID,
			Priority:       row.Priority,
			QueuedAt:       row.QueuedAt,
			Alert:          row.Priority == priorityNormal && !now.Before(row.QueuedAt.Add(limit)),
		})
	}
	return out, nil
}

// PutWaitAlert stores the normal-wait threshold. expectedVersion 0 inserts the first row.
// The caller supplies the tenant transaction. This function takes the exclusive tenant advisory
// before the settings row, and it does not authorize a session.
func PutWaitAlert(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, expectedVersion int64, seconds int) (Settings, error) {
	var out Settings
	if tx == nil || tenant == uuid.Nil || expectedVersion < 0 || seconds < 0 || seconds > MaxWaitAlertSeconds {
		return out, ErrInvalid
	}
	if err := pinExclusive(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return out, err
	}
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM distribution_settings WHERE tenant_id=$1 FOR UPDATE`, tenant).Scan(&version)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return out, err
	}
	if missing {
		if expectedVersion != 0 {
			return out, ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO distribution_settings(tenant_id,wait_alert_seconds,version) VALUES($1,$2,1)`, tenant, seconds); err != nil {
			return out, err
		}
		return Settings{Version: 1, Configured: true, WaitAlertSeconds: seconds}, nil
	}
	if version != expectedVersion {
		return out, ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE distribution_settings SET wait_alert_seconds=$3, version=version+1 WHERE tenant_id=$1 AND version=$2`, tenant, expectedVersion, seconds)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() != 1 {
		return out, ErrConflict
	}
	return Settings{Version: version + 1, Configured: true, WaitAlertSeconds: seconds}, nil
}

// PutSectorAuto stores whether one sector receives automatic assignment.
// expectedVersion 0 inserts the first row. No row leaves the sector enabled.
// The caller supplies the tenant transaction. This function takes the exclusive
// tenant advisory and the exclusive sector-auto advisory before the sector row.
// It does not authorize a session.
func PutSectorAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID, expectedVersion int64, enabled bool) (SectorAuto, error) {
	var out SectorAuto
	if tx == nil || tenant == uuid.Nil || sector == uuid.Nil || expectedVersion < 0 {
		return out, ErrInvalid
	}
	if err := pinExclusive(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := pinSectorAuto(ctx, tx, tenant, sector, true); err != nil {
		return out, err
	}
	var found uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM sectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenant, sector).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrInvalid
	}
	if err != nil {
		return out, err
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM distribution_sector_auto WHERE tenant_id=$1 AND sector_id=$2 FOR UPDATE`, tenant, sector).Scan(&version)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return out, err
	}
	if missing {
		if expectedVersion != 0 {
			return out, ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO distribution_sector_auto(tenant_id,sector_id,enabled,version) VALUES($1,$2,$3,1)`, tenant, sector, enabled); err != nil {
			return out, err
		}
		return SectorAuto{Enabled: enabled, Version: 1, Configured: true}, nil
	}
	if version != expectedVersion {
		return out, ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE distribution_sector_auto SET enabled=$4, version=version+1 WHERE tenant_id=$1 AND sector_id=$2 AND version=$3`, tenant, sector, expectedVersion, enabled)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() != 1 {
		return out, ErrConflict
	}
	return SectorAuto{Enabled: enabled, Version: version + 1, Configured: true}, nil
}

type candidate struct {
	User   uuid.UUID
	Member uuid.UUID
}

type headRow struct {
	ConversationID uuid.UUID
	Version        int64
	Priority       string
	QueuedAt       time.Time
}

func queued(reason string) Result {
	return Result{Status: StatusQueued, Reason: reason}
}

func queuedHead(reason string, head headRow) Result {
	return Result{Status: StatusQueued, Reason: reason, ConversationID: head.ConversationID, Priority: head.Priority}
}

func normalize(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Microsecond)
}

func readSectorAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID) (SectorAuto, error) {
	if err := pinSectorAuto(ctx, tx, tenant, sector, false); err != nil {
		return SectorAuto{}, err
	}
	return loadSectorAuto(ctx, tx, tenant, sector)
}

func pinSectorAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID, exclusive bool) error {
	query := `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-distribution-auto'), hashtext($1::text))`
	if exclusive {
		query = `SELECT pg_advisory_xact_lock(hashtext('chatisp-distribution-auto'), hashtext($1::text))`
	}
	_, err := tx.Exec(ctx, query, tenant.String()+":"+sector.String())
	return err
}

func loadSectorAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID) (SectorAuto, error) {
	var out SectorAuto
	err := tx.QueryRow(ctx, `SELECT enabled, version FROM distribution_sector_auto WHERE tenant_id=$1 AND sector_id=$2 FOR SHARE`, tenant, sector).Scan(&out.Enabled, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return SectorAuto{Enabled: true}, nil
	}
	if err != nil {
		return SectorAuto{}, err
	}
	out.Configured = true
	return out, nil
}

func pinScope(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var scope string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&scope); err != nil {
		return err
	}
	if scope != tenant.String() {
		return ErrForbidden
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String())
	return err
}

func pinExclusive(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var scope string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&scope); err != nil {
		return err
	}
	if scope != tenant.String() {
		return ErrForbidden
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String())
	return err
}

func tenantActive(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "active") {
		return ErrForbidden
	}
	return err
}

func lookupSector(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, lock bool) (bool, bool, bool, error) {
	query := `SELECT s.active, EXISTS(SELECT 1 FROM sector_inboxes si WHERE si.tenant_id=s.tenant_id AND si.sector_id=s.id AND si.inbox_id=$3) FROM sectors s WHERE s.tenant_id=$1 AND s.id=$2`
	if lock {
		query += ` FOR SHARE`
	}
	var active, linked bool
	err := tx.QueryRow(ctx, query, tenant, sector, inbox).Scan(&active, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, false, nil
	}
	if err != nil {
		return false, false, false, err
	}
	if lock && linked {
		if err = shareInbox(ctx, tx, tenant, sector, inbox); err != nil {
			return false, false, false, err
		}
	}
	return true, active, linked, nil
}

func shareInbox(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT 1 FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3 FOR SHARE`, tenant, sector, inbox)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func loadSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	out := Settings{WaitAlertSeconds: DefaultWaitAlertSeconds}
	err := tx.QueryRow(ctx, `SELECT version, wait_alert_seconds FROM distribution_settings WHERE tenant_id=$1 FOR SHARE`, tenant).Scan(&out.Version, &out.WaitAlertSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return Settings{}, err
	}
	out.Configured = true
	return out, nil
}

func loadCandidates(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) ([]candidate, error) {
	rows, err := tx.Query(ctx, `SELECT m.user_id, m.id FROM sector_memberships sm JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent') JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active WHERE sm.tenant_id=$1 AND sm.sector_id=$2 AND EXISTS (SELECT 1 FROM sector_memberships own JOIN sector_inboxes box ON box.tenant_id=own.tenant_id AND box.sector_id=own.sector_id AND box.inbox_id=$3 JOIN sectors os ON os.tenant_id=box.tenant_id AND os.id=box.sector_id AND os.active WHERE own.tenant_id=sm.tenant_id AND own.membership_id=sm.membership_id) ORDER BY m.user_id`, tenant, sector, inbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.User, &item.Member); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Member[:], out[j].Member[:]) < 0
	})
	return out, nil
}

func rotate(in []candidate, last uuid.UUID) []candidate {
	if len(in) == 0 {
		return nil
	}
	start := 0
	if last != uuid.Nil {
		start = len(in)
		for i, item := range in {
			if bytes.Compare(item.Member[:], last[:]) > 0 {
				start = i
				break
			}
		}
		if start == len(in) {
			start = 0
		}
	}
	out := make([]candidate, 0, len(in))
	for i := 0; i < len(in); i++ {
		out = append(out, in[(start+i)%len(in)])
	}
	return out
}

func lockCandidate(ctx context.Context, tx pgx.Tx, tenant, sector, inbox, member uuid.UUID) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT 1 FROM sector_memberships sm JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent') JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active WHERE sm.tenant_id=$1 AND sm.sector_id=$2 AND sm.membership_id=$3 AND EXISTS (SELECT 1 FROM sector_memberships own JOIN sector_inboxes box ON box.tenant_id=own.tenant_id AND box.sector_id=own.sector_id AND box.inbox_id=$4 JOIN sectors os ON os.tenant_id=box.tenant_id AND os.id=box.sector_id AND os.active WHERE own.tenant_id=sm.tenant_id AND own.membership_id=sm.membership_id) FOR SHARE OF sm, m, s`, tenant, sector, member, inbox)
	if err != nil {
		return false, err
	}
	kept := rows.Next()
	err = rows.Err()
	rows.Close()
	return kept, err
}

func lockHead(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) (headRow, bool, error) {
	var held []uuid.UUID
	for attempt := 0; attempt < 32; attempt++ {
		visible, err := listQueuedIDs(ctx, tx, tenant, sector, inbox)
		if err != nil {
			return headRow{}, false, err
		}
		for _, id := range visible {
			if containsID(held, id) {
				continue
			}
			if len(held) > 0 && bytes.Compare(id[:], held[len(held)-1][:]) < 0 {
				return headRow{}, false, ErrConflict
			}
			kept, err := lockQueued(ctx, tx, tenant, sector, inbox, id)
			if err != nil {
				return headRow{}, false, err
			}
			if kept {
				held = append(held, id)
			}
		}
		if len(held) == 0 {
			return headRow{}, false, nil
		}
		rows, err := readLockedQueue(ctx, tx, tenant, held)
		if err != nil {
			return headRow{}, false, err
		}
		if len(rows) == 0 {
			return headRow{}, false, nil
		}
		sortQueue(rows)
		fresh, err := listQueue(ctx, tx, tenant, sector, inbox)
		if err != nil {
			return headRow{}, false, err
		}
		if len(fresh) > 0 && fresh[0].ConversationID == rows[0].ConversationID && fresh[0].Priority == rows[0].Priority && fresh[0].QueuedAt.Equal(rows[0].QueuedAt) {
			return rows[0], true, nil
		}
	}
	return headRow{}, false, ErrConflict
}

func listQueuedIDs(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT c.id FROM conversations c WHERE c.tenant_id=$1 AND c.sector_id=$2 AND c.inbox_id=$3 AND c.assignee_id IS NULL AND c.status IN ('open','waiting_customer') AND EXISTS (SELECT 1 FROM assignment_queue_intervals q WHERE q.tenant_id=c.tenant_id AND q.conversation_id=c.id AND q.ended_at IS NULL) ORDER BY c.id`, tenant, sector, inbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func lockQueued(ctx context.Context, tx pgx.Tx, tenant, sector, inbox, id uuid.UUID) (bool, error) {
	var found uuid.UUID
	err := tx.QueryRow(ctx, `SELECT c.id FROM conversations c WHERE c.tenant_id=$1 AND c.id=$2 AND c.sector_id=$3 AND c.inbox_id=$4 AND c.assignee_id IS NULL AND c.status IN ('open','waiting_customer') FOR UPDATE`, tenant, id, sector, inbox).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func readLockedQueue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []uuid.UUID) ([]headRow, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.version, COALESCE(a.priority,'normal'), q.started_at FROM conversations c JOIN assignment_queue_intervals q ON q.tenant_id=c.tenant_id AND q.conversation_id=c.id AND q.ended_at IS NULL LEFT JOIN conversation_annotations a ON a.tenant_id=c.tenant_id AND a.conversation_id=c.id WHERE c.tenant_id=$1 AND c.id = ANY($2::uuid[]) AND c.assignee_id IS NULL AND c.status IN ('open','waiting_customer')`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectQueue(rows)
}

func sortQueue(rows []headRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rank(rows[i].Priority) != rank(rows[j].Priority) {
			return rank(rows[i].Priority) < rank(rows[j].Priority)
		}
		if !rows[i].QueuedAt.Equal(rows[j].QueuedAt) {
			return rows[i].QueuedAt.Before(rows[j].QueuedAt)
		}
		return bytes.Compare(rows[i].ConversationID[:], rows[j].ConversationID[:]) < 0
	})
}

func rank(priority string) int {
	if priority == priorityUrgent {
		return 0
	}
	return 1
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func listQueue(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID) ([]headRow, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.version, COALESCE(a.priority,'normal'), q.started_at FROM conversations c JOIN assignment_queue_intervals q ON q.tenant_id=c.tenant_id AND q.conversation_id=c.id AND q.ended_at IS NULL LEFT JOIN conversation_annotations a ON a.tenant_id=c.tenant_id AND a.conversation_id=c.id WHERE c.tenant_id=$1 AND c.sector_id=$2 AND c.inbox_id=$3 AND c.assignee_id IS NULL AND c.status IN ('open','waiting_customer') ORDER BY CASE WHEN COALESCE(a.priority,'normal')='urgent' THEN 0 ELSE 1 END, q.started_at, c.id`, tenant, sector, inbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectQueue(rows)
}

func collectQueue(rows pgx.Rows) ([]headRow, error) {
	var out []headRow
	for rows.Next() {
		var row headRow
		if err := rows.Scan(&row.ConversationID, &row.Version, &row.Priority, &row.QueuedAt); err != nil {
			return nil, err
		}
		row.QueuedAt = row.QueuedAt.UTC()
		if row.Priority != priorityUrgent {
			row.Priority = priorityNormal
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
