package lifecycle

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/presence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OnCustomerMessage drops an active snooze when the message is current.
// A message older than the snooze is ignored. The conversation is not closed.
// Composition calls this in the same transaction that stored the customer message.
func OnCustomerMessage(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, at time.Time) (Release, error) {
	at = normalize(at)
	if conversation == uuid.Nil || at.IsZero() {
		return Release{}, ErrInvalid
	}
	if err := pinTenant(ctx, tx, tenant); err != nil {
		return Release{}, err
	}
	return releaseSnooze(ctx, tx, tenant, conversation, at, true)
}

// ReturnDue releases at most one snooze whose deadline is due at now: the
// lowest conversation id still active. The capacity advisory taken for that
// return lives only until this transaction commits, so the caller must open
// a fresh transaction for the next conversation. Holding the advisory while
// locking another conversation deadlocks with a message or assignment that
// already locked that conversation and is waiting for the same capacity.
// An eligible former assignee is reserved with presence.Effective and
// LockAssignmentCapacity. Otherwise the conversation goes back to the queue.
// Nothing in this function closes an attendance.
// An empty slice means nothing due remains. A single uncleared release means
// the candidate lost a race and the caller should try again.
func ReturnDue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) ([]Release, error) {
	now = normalize(now)
	if now.IsZero() {
		return nil, ErrInvalid
	}
	if err := pinTenant(ctx, tx, tenant); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT conversation_id FROM conversation_snoozes WHERE tenant_id=$1 AND state='active' AND snoozed_until<=$2 ORDER BY conversation_id LIMIT 1`, tenant, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return []Release{}, nil
	}
	if err != nil {
		return nil, err
	}
	rel, err := releaseSnooze(ctx, tx, tenant, id, now, false)
	if err != nil {
		return nil, err
	}
	if rel.Cleared {
		return []Release{rel}, nil
	}
	var still bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active' AND snoozed_until<=$3)`, tenant, id, now).Scan(&still); err != nil {
		return nil, err
	}
	if still {
		return nil, ErrConflict
	}
	rel.ConversationID = id
	return []Release{rel}, nil
}

// RunInactivity arms watches and writes one durable warning request per generation.
// It does not confirm delivery. Close runs only after ConfirmWarning stored
// confirmed for that same generation and the close deadline has passed.
// State, possession, snooze and a newer customer message are read again under
// the conversation lock. A pending or uncertain warning does not close.
func RunInactivity(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) ([]Effect, error) {
	now = normalize(now)
	if now.IsZero() {
		return nil, ErrInvalid
	}
	if err := pinTenant(ctx, tx, tenant); err != nil {
		return nil, err
	}
	settings, err := loadSettings(ctx, tx, tenant, false)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM (
		SELECT c.id FROM conversations c
		WHERE c.tenant_id=$1 AND c.status='waiting_customer' AND c.controller='human' AND c.assignee_id IS NOT NULL
		  AND c.last_eligible_human_reply_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM conversation_snoozes s WHERE s.tenant_id=c.tenant_id AND s.conversation_id=c.id AND s.state='active')
		UNION
		SELECT w.conversation_id FROM inactivity_watches w
		WHERE w.tenant_id=$1 AND w.closed_at IS NULL AND w.warning_state IN ('waiting','sent','uncertain')
	) AS due ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := []Effect{}
	for _, id := range ids {
		effect, err := runOne(ctx, tx, tenant, id, now, settings)
		if err != nil {
			return nil, err
		}
		if effect.Action != "" {
			out = append(out, effect)
		}
	}
	return out, nil
}

func releaseSnooze(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, at time.Time, customer bool) (Release, error) {
	rel := Release{ConversationID: conversation}
	// Exclusive conversation lock before the snooze row and any membership share.
	conv, err := lockConversation(ctx, tx, tenant, conversation)
	if err != nil {
		return rel, err
	}
	rel.Status = conv.Status
	snooze, err := lockActiveSnooze(ctx, tx, tenant, conversation, true)
	if err != nil {
		return rel, err
	}
	if snooze.ID == uuid.Nil {
		return rel, nil
	}
	if customer && normalize(at).Before(normalize(snooze.Created)) {
		return rel, nil
	}
	if !customer && normalize(at).Before(normalize(snooze.Until)) {
		return rel, nil
	}
	if conv.Status == "closed" {
		if err = finishSnooze(ctx, tx, tenant, snooze.ID, stateReturned, resolutionClosed, at); err != nil {
			return rel, err
		}
		rel.Cleared = true
		rel.Resolution = resolutionClosed
		return rel, nil
	}
	if customer {
		if _, err = tx.Exec(ctx, `UPDATE conversations SET last_customer_message_at=CASE WHEN last_customer_message_at IS NULL OR $3>last_customer_message_at THEN $3 ELSE last_customer_message_at END, status=CASE WHEN status='waiting_customer' THEN 'open' ELSE status END, waiting_since=CASE WHEN status='waiting_customer' THEN NULL ELSE waiting_since END, version=version+1, updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND status<>'closed' AND (status='waiting_customer' OR last_customer_message_at IS NULL OR last_customer_message_at<$3)`, tenant, conversation, at); err != nil {
			return rel, err
		}
		conv, err = lockConversation(ctx, tx, tenant, conversation)
		if err != nil {
			return rel, err
		}
	}
	assigned := false
	var user uuid.UUID
	if conv.Assignee == uuid.Nil && conv.Sector != uuid.Nil && conv.Inbox != uuid.Nil {
		assigned, user, err = tryPreferred(ctx, tx, tenant, conversation, conv.Sector, conv.Inbox, snooze.Destination, conv.Version, at)
		if err != nil {
			return rel, err
		}
	}
	state, resolution := stateQueued, resolutionQueued
	if customer {
		state = stateClearedByCustomer
	}
	if assigned {
		resolution = resolutionAssigned
		if !customer {
			state = stateReturned
		}
	} else if conv.Assignee != uuid.Nil {
		resolution = resolutionAlready
		state = stateReturned
		if customer {
			state = stateClearedByCustomer
		}
	}
	if err = finishSnooze(ctx, tx, tenant, snooze.ID, state, resolution, at); err != nil {
		return rel, err
	}
	if !assigned && conv.Assignee == uuid.Nil {
		if _, err = tx.Exec(ctx, `UPDATE conversations SET status=status, updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenant, conversation, at); err != nil {
			return rel, err
		}
	}
	conv, err = lockConversation(ctx, tx, tenant, conversation)
	if err != nil {
		return rel, err
	}
	if err = insertEvent(ctx, tx, tenant, conversation, conv.Cycle, "snooze_cleared", user, at, conv.Version, conv.Epoch, conv.Controller, conv.Assignee); err != nil {
		return rel, err
	}
	if err = cancelWatch(ctx, tx, tenant, conversation); err != nil {
		return rel, err
	}
	rel.Cleared = true
	rel.Assigned = assigned
	if assigned {
		rel.MembershipID = snooze.Destination
	}
	rel.Resolution = resolution
	rel.Status = conv.Status
	return rel, nil
}

func finishSnooze(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, state, resolution string, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE conversation_snoozes SET state=$3, resolution=$4, resolved_at=$5 WHERE tenant_id=$1 AND id=$2 AND state='active'`, tenant, id, state, resolution, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func tryPreferred(ctx context.Context, tx pgx.Tx, tenant, conversation, sector, inbox, member uuid.UUID, version int64, now time.Time) (bool, uuid.UUID, error) {
	var user uuid.UUID
	err := tx.QueryRow(ctx, `SELECT user_id FROM memberships WHERE tenant_id=$1 AND id=$2 AND status='active' AND role IN ('admin','supervisor','agent')`, tenant, member).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, uuid.Nil, nil
	}
	if err != nil {
		return false, uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT lifecycle_try`); err != nil {
		return false, uuid.Nil, err
	}
	ok, err := assignPreferred(ctx, tx, tenant, conversation, sector, inbox, member, user, version, now)
	if err != nil || !ok {
		if _, rb := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT lifecycle_try`); rb != nil && err == nil {
			err = rb
		}
		if _, rel := tx.Exec(ctx, `RELEASE SAVEPOINT lifecycle_try`); rel != nil && err == nil {
			err = rel
		}
		return false, uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `RELEASE SAVEPOINT lifecycle_try`); err != nil {
		return false, uuid.Nil, err
	}
	return true, user, nil
}

func assignPreferred(ctx context.Context, tx pgx.Tx, tenant, conversation, sector, inbox, member, user uuid.UUID, version int64, now time.Time) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT 1 FROM sector_memberships sm JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent') JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active WHERE sm.tenant_id=$1 AND sm.sector_id=$2 AND sm.membership_id=$3 AND EXISTS (SELECT 1 FROM sector_memberships own JOIN sector_inboxes box ON box.tenant_id=own.tenant_id AND box.sector_id=own.sector_id AND box.inbox_id=$4 JOIN sectors os ON os.tenant_id=box.tenant_id AND os.id=box.sector_id AND os.active WHERE own.tenant_id=sm.tenant_id AND own.membership_id=sm.membership_id) FOR SHARE OF sm, m, s`, tenant, sector, member, inbox)
	if err != nil {
		return false, err
	}
	kept := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil || !kept {
		return false, err
	}
	snap, err := presence.Effective(ctx, tx, tenant, user, now)
	if err != nil {
		return false, err
	}
	if !snap.Assignable || snap.MembershipID != member {
		return false, nil
	}
	if err = store.LockAssignmentCapacity(ctx, tx, tenant, member, conversation); err != nil {
		if errors.Is(err, store.ErrCapacityFull) {
			return false, nil
		}
		return false, err
	}
	_, err = (store.Repo{}).ApplyAssignment(ctx, tx, tenant, conversation, user, member, uuid.Nil, version, "snooze_return", now)
	if err != nil {
		return false, err
	}
	return true, nil
}

type watchRow struct {
	Anchor     time.Time
	Assignee   uuid.UUID
	Generation int64
	WarningDue time.Time
	CloseDue   time.Time
	State      string
	HasClose   bool
	Closed     bool
}

func lockWatch(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) (watchRow, bool, error) {
	var row watchRow
	var closeDue, closed *time.Time
	err := tx.QueryRow(ctx, `SELECT anchor_reply_at, assignee_id, generation, warning_due_at, close_due_at, warning_state, closed_at FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2 FOR UPDATE`, tenant, conversation).Scan(&row.Anchor, &row.Assignee, &row.Generation, &row.WarningDue, &closeDue, &row.State, &closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	if closeDue != nil {
		row.CloseDue = closeDue.UTC()
		row.HasClose = true
	}
	row.Closed = closed != nil
	row.Anchor = row.Anchor.UTC()
	row.WarningDue = row.WarningDue.UTC()
	return row, true, nil
}

func runOne(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, now time.Time, settings InactivitySettings) (Effect, error) {
	effect := Effect{ConversationID: conversation}
	// Exclusive conversation lock before the snooze row and the watch.
	conv, err := lockConversation(ctx, tx, tenant, conversation)
	if errors.Is(err, store.ErrNotFound) {
		return effect, nil
	}
	if err != nil {
		return effect, err
	}
	snooze, err := lockActiveSnooze(ctx, tx, tenant, conversation, true)
	if err != nil {
		return effect, err
	}
	watch, found, err := lockWatch(ctx, tx, tenant, conversation)
	if err != nil {
		return effect, err
	}
	memberOK := false
	if conv.Assignee != uuid.Nil {
		var status string
		err = tx.QueryRow(ctx, `SELECT status FROM memberships WHERE tenant_id=$1 AND id=$2`, tenant, conv.Assignee).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return effect, err
		}
		memberOK = err == nil && status == "active"
	}
	eligible := humanEligible(conv, snooze, memberOK)
	if !eligible {
		if found && !watch.Closed && watch.State != watchCancelled {
			if err = cancelWatch(ctx, tx, tenant, conversation); err != nil {
				return effect, err
			}
			effect.Action = "cancelled"
			effect.Generation = watch.Generation + 1
		}
		return effect, nil
	}
	anchor := normalize(conv.Anchor)
	warningDue := anchor.Add(time.Duration(settings.WarningAfterSeconds) * time.Second)
	same := found && !watch.Closed && sameTime(watch.Anchor, anchor) && watch.Assignee == conv.Assignee && watch.State != watchCancelled
	if !same {
		var generation int64
		err = tx.QueryRow(ctx, `INSERT INTO inactivity_watches(tenant_id,conversation_id,anchor_reply_at,assignee_id,generation,warning_due_at,warning_state) VALUES($1,$2,$3,$4,1,$5,'waiting') ON CONFLICT(tenant_id,conversation_id) DO UPDATE SET anchor_reply_at=EXCLUDED.anchor_reply_at, assignee_id=EXCLUDED.assignee_id, generation=inactivity_watches.generation+1, warning_due_at=EXCLUDED.warning_due_at, close_due_at=NULL, warning_state='waiting', warning_at=NULL, closed_at=NULL RETURNING generation`, tenant, conversation, anchor, conv.Assignee, warningDue).Scan(&generation)
		if err != nil {
			return effect, err
		}
		watch = watchRow{Anchor: anchor, Assignee: conv.Assignee, Generation: generation, WarningDue: warningDue, State: watchWaiting}
	}
	effect.Generation = watch.Generation
	if watch.State == watchUncertain {
		effect.Action = "uncertain"
		return effect, nil
	}
	if watch.State == watchWaiting {
		if now.Before(watch.WarningDue) {
			effect.Action = "armed"
			return effect, nil
		}
		if err = requestWarning(ctx, tx, tenant, conversation, watch, now); err != nil {
			return effect, err
		}
		effect.Action = "requested"
		return effect, nil
	}
	if watch.State == watchRequested {
		effect.Action = "requested"
		return effect, nil
	}
	if watch.State != watchSent || !watch.HasClose || now.Before(watch.CloseDue) {
		effect.Action = "armed"
		return effect, nil
	}
	var confirmed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_warning_requests WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 AND state='confirmed')`, tenant, conversation, watch.Generation).Scan(&confirmed); err != nil {
		return effect, err
	}
	if !confirmed {
		effect.Action = "uncertain"
		return effect, nil
	}
	if _, _, err = (store.Repo{}).Close(ctx, tx, store.CloseInput{
		TenantID: tenant, ConversationID: conversation, MembershipID: conv.Assignee,
		ExpectedVersion: conv.Version, Reason: closeReason, Result: store.ResultNoResponse,
		Summary: closeSummary, AuthorKind: store.ControllerHuman, At: now,
	}); err != nil {
		return effect, err
	}
	if _, err = tx.Exec(ctx, `UPDATE inactivity_watches SET closed_at=$3 WHERE tenant_id=$1 AND conversation_id=$2 AND closed_at IS NULL`, tenant, conversation, now); err != nil {
		return effect, err
	}
	effect.Action = "closed"
	return effect, nil
}

func humanEligible(conv convRow, snooze snoozeRow, memberOK bool) bool {
	return memberOK && snooze.ID == uuid.Nil && conv.Status == "waiting_customer" && conv.Controller == "human" && conv.HasAnchor && !(conv.HasCustomer && normalize(conv.CustomerAt).After(normalize(conv.Anchor)))
}

func requestWarning(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, watch watchRow, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO lifecycle_warning_requests(tenant_id,conversation_id,generation,anchor_reply_at,assignee_id,body,state,requested_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7) ON CONFLICT(tenant_id,conversation_id,generation) DO NOTHING`, tenant, conversation, watch.Generation, watch.Anchor, watch.Assignee, WarningBody, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE inactivity_watches SET warning_state='requested' WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 AND warning_state='waiting'`, tenant, conversation, watch.Generation)
	return err
}

// ConfirmWarning binds a delivery result to the warning generation that was requested.
// confirmed arms the close deadline only when the conversation is still the same
// waiting human possession, with no active snooze and no newer customer message.
// uncertain, a stale generation, or a failed revalidation never arms that deadline.
// receipt_outcome stores the received result. state stores the arming decision.
// Repeating the received result returns that decision and does not move the deadline.
func ConfirmWarning(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, receipt WarningReceipt, now time.Time) (Effect, error) {
	now = normalize(now)
	effect := Effect{ConversationID: receipt.ConversationID, Generation: receipt.Generation}
	if receipt.ConversationID == uuid.Nil || receipt.Generation < 1 || now.IsZero() || (receipt.Outcome != WarningConfirmed && receipt.Outcome != WarningUncertain) {
		return effect, ErrInvalid
	}
	if err := pinTenant(ctx, tx, tenant); err != nil {
		return effect, err
	}
	settings, err := loadSettings(ctx, tx, tenant, false)
	if err != nil {
		return effect, err
	}
	// Exclusive conversation lock before the snooze row, the watch and the warning request.
	conv, err := lockConversation(ctx, tx, tenant, receipt.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return effect, ErrNotFound
	}
	if err != nil {
		return effect, err
	}
	snooze, err := lockActiveSnooze(ctx, tx, tenant, receipt.ConversationID, true)
	if err != nil {
		return effect, err
	}
	watch, found, err := lockWatch(ctx, tx, tenant, receipt.ConversationID)
	if err != nil {
		return effect, err
	}
	var reqState string
	var storedReceipt *string
	err = tx.QueryRow(ctx, `SELECT state, receipt_outcome FROM lifecycle_warning_requests WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 FOR UPDATE`, tenant, receipt.ConversationID, receipt.Generation).Scan(&reqState, &storedReceipt)
	if errors.Is(err, pgx.ErrNoRows) {
		return effect, ErrNotFound
	}
	if err != nil {
		return effect, err
	}
	if reqState != requestPending {
		// receipt_outcome is the result that was received. state is the decision
		// to arm or withhold the deadline. Repeating the received result returns
		// that decision; a different result conflicts.
		if storedReceipt == nil || *storedReceipt != receipt.Outcome {
			return effect, ErrConflict
		}
		effect.Action = "uncertain"
		if reqState == requestConfirmed {
			effect.Action = "confirmed"
		}
		return effect, nil
	}
	memberOK := false
	if conv.Assignee != uuid.Nil {
		var status string
		err = tx.QueryRow(ctx, `SELECT status FROM memberships WHERE tenant_id=$1 AND id=$2`, tenant, conv.Assignee).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return effect, err
		}
		memberOK = err == nil && status == "active"
	}
	live := found && !watch.Closed && watch.Generation == receipt.Generation && watch.Assignee == conv.Assignee && sameTime(watch.Anchor, conv.Anchor) && (watch.State == watchRequested || watch.State == watchWaiting)
	accept := receipt.Outcome == WarningConfirmed && live && humanEligible(conv, snooze, memberOK)
	if accept {
		closeDue := now.Add(time.Duration(settings.CloseAfterSeconds) * time.Second)
		tag, err := tx.Exec(ctx, `UPDATE lifecycle_warning_requests SET state='confirmed', receipt_outcome='confirmed', resolved_at=$4 WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 AND state='pending'`, tenant, receipt.ConversationID, receipt.Generation, now)
		if err != nil {
			return effect, err
		}
		if tag.RowsAffected() != 1 {
			return effect, ErrConflict
		}
		tag, err = tx.Exec(ctx, `UPDATE inactivity_watches SET warning_state='sent', warning_at=$3, close_due_at=$4 WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$5 AND warning_state IN ('waiting','requested') AND closed_at IS NULL`, tenant, receipt.ConversationID, now, closeDue, receipt.Generation)
		if err != nil {
			return effect, err
		}
		if tag.RowsAffected() != 1 {
			return effect, ErrConflict
		}
		effect.Action = "confirmed"
		return effect, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE lifecycle_warning_requests SET state='uncertain', receipt_outcome=$5, resolved_at=$4 WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3 AND state='pending'`, tenant, receipt.ConversationID, receipt.Generation, now, receipt.Outcome)
	if err != nil {
		return effect, err
	}
	if tag.RowsAffected() != 1 {
		return effect, ErrConflict
	}
	if live {
		if _, err = tx.Exec(ctx, `UPDATE inactivity_watches SET warning_state='uncertain', warning_at=$3, close_due_at=NULL WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$4 AND closed_at IS NULL AND warning_state IN ('waiting','requested')`, tenant, receipt.ConversationID, now, receipt.Generation); err != nil {
			return effect, err
		}
	}
	effect.Action = "uncertain"
	return effect, nil
}
