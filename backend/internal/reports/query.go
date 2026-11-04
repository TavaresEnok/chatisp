package reports

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	_ "time/tzdata"

	attendanceStore "chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrForbidden means the principal cannot see the requested sector or inbox.
	ErrForbidden = errors.New("acesso negado")
	// ErrInvalid means the period or the grouping cannot be read.
	ErrInvalid = errors.New("período inválido")
)

// Window converts inclusive civil dates in loc into a half-open instant range.
func Window(from, to string, loc *time.Location) (time.Time, time.Time, error) {
	if loc == nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	start, err := time.ParseInLocation("2006-01-02", from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	last, err := time.ParseInLocation("2006-01-02", to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	end := last.AddDate(0, 0, 1)
	if !end.After(start) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	days := 0
	for cursor := start; cursor.Before(end); cursor = cursor.AddDate(0, 0, 1) {
		days++
		if days > MaxDays {
			return time.Time{}, time.Time{}, ErrInvalid
		}
	}
	return start, end, nil
}

// Query loads persisted rows the principal may see and computes the report.
// The caller owns the tenant transaction.
func Query(ctx context.Context, tx pgx.Tx, p Principal, f Filter) (Report, error) {
	if tx == nil || p.TenantID == uuid.Nil || f.Now.IsZero() {
		return Report{}, ErrInvalid
	}
	group := f.Group
	if group == "" {
		group = "day"
	}
	switch group {
	case "day", "week", "month":
	default:
		return Report{}, ErrInvalid
	}
	if err := sectors.ValidatePrincipal(ctx, tx, sectors.Principal{
		TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role,
	}); err != nil {
		if errors.Is(err, sectors.ErrForbidden) {
			return Report{}, ErrForbidden
		}
		return Report{}, err
	}
	var timezone string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM tenants WHERE id=$1`, p.TenantID).Scan(&timezone); err != nil {
		return Report{}, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return Report{}, ErrInvalid
	}
	from, to, err := Window(f.FromDate, f.ToDate, loc)
	if err != nil {
		return Report{}, err
	}
	sectorIDs, inboxIDs, err := visibleScope(ctx, tx, p)
	if err != nil {
		return Report{}, err
	}
	if f.SectorID != uuid.Nil && !contains(sectorIDs, f.SectorID) {
		return Report{}, ErrForbidden
	}
	if f.InboxID != uuid.Nil && !contains(inboxIDs, f.InboxID) {
		return Report{}, ErrForbidden
	}
	if f.SectorID != uuid.Nil {
		sectorIDs = []uuid.UUID{f.SectorID}
	}
	if f.InboxID != uuid.Nil {
		inboxIDs = []uuid.UUID{f.InboxID}
	}
	alert, err := waitAlert(ctx, tx, p.TenantID)
	if err != nil {
		return Report{}, err
	}
	facts := Facts{
		Location: loc, Timezone: timezone, From: from, To: to, Now: f.Now, Group: group,
		WaitAlertSeconds: alert,
		Names:            Names{Sectors: map[uuid.UUID]string{}, Inboxes: map[uuid.UUID]string{}, Members: map[uuid.UUID]string{}},
	}
	for _, id := range sectorIDs {
		if facts.SectorAllow == nil {
			facts.SectorAllow = map[uuid.UUID]bool{}
		}
		facts.SectorAllow[id] = true
	}
	if len(sectorIDs) == 0 || len(inboxIDs) == 0 {
		return Compute(facts), nil
	}
	if err := loadFacts(ctx, tx, p, sectorIDs, inboxIDs, &facts); err != nil {
		return Report{}, err
	}
	return Compute(facts), nil
}

func visibleScope(ctx context.Context, tx pgx.Tx, p Principal) ([]uuid.UUID, []uuid.UUID, error) {
	var sectorQuery, inboxQuery string
	var args []any
	switch p.Role {
	case "admin", "supervisor":
		sectorQuery = `SELECT id FROM sectors WHERE tenant_id=$1 ORDER BY id`
		inboxQuery = `SELECT id FROM inboxes WHERE tenant_id=$1 ORDER BY id`
		args = []any{p.TenantID}
	case "agent":
		sectorQuery = `SELECT sm.sector_id FROM sector_memberships sm JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active WHERE sm.tenant_id=$1 AND sm.membership_id=$2 ORDER BY sm.sector_id`
		inboxQuery = `SELECT DISTINCT si.inbox_id FROM sector_inboxes si JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active JOIN sector_memberships sm ON sm.tenant_id=si.tenant_id AND sm.sector_id=si.sector_id AND sm.membership_id=$2 WHERE si.tenant_id=$1 ORDER BY si.inbox_id`
		args = []any{p.TenantID, p.MembershipID}
	default:
		return nil, nil, ErrForbidden
	}
	sectors, err := queryIDs(ctx, tx, sectorQuery, args...)
	if err != nil {
		return nil, nil, err
	}
	inboxes, err := queryIDs(ctx, tx, inboxQuery, args...)
	return sectors, inboxes, err
}

func queryIDs(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func waitAlert(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	var seconds int
	err := tx.QueryRow(ctx, `SELECT wait_alert_seconds FROM distribution_settings WHERE tenant_id=$1`, tenant).Scan(&seconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultWaitAlertSeconds, nil
	}
	return seconds, err
}

func loadFacts(ctx context.Context, tx pgx.Tx, p Principal, sectorIDs, inboxIDs []uuid.UUID, facts *Facts) error {
	if err := loadNames(ctx, tx, p, inboxIDs, facts); err != nil {
		return err
	}
	cycles, convIDs, err := loadCycles(ctx, tx, p.TenantID, inboxIDs, sectorIDs, facts.From, facts.To)
	if err != nil {
		return err
	}
	facts.Cycles = cycles
	if len(convIDs) > 0 {
		if facts.Events, err = loadEvents(ctx, tx, p.TenantID, convIDs, facts.To); err != nil {
			return err
		}
		if facts.Messages, err = loadMessages(ctx, tx, p.TenantID, convIDs, facts.To); err != nil {
			return err
		}
		if facts.Snoozes, err = loadSnoozes(ctx, tx, p.TenantID, convIDs, facts.Now); err != nil {
			return err
		}
		if facts.Queues, err = loadQueues(ctx, tx, p.TenantID, convIDs, false); err != nil {
			return err
		}
		if facts.Tags, err = loadTags(ctx, tx, p.TenantID, convIDs); err != nil {
			return err
		}
		reasons, err := loadTriageReasons(ctx, tx, p.TenantID, convIDs)
		if err != nil {
			return err
		}
		for i := range facts.Cycles {
			if facts.Cycles[i].CloseReason == "" {
				facts.Cycles[i].TriageReason = reasons[facts.Cycles[i].ConversationID]
			}
		}
	}
	if facts.LiveConversations, err = loadLive(ctx, tx, p.TenantID, inboxIDs, sectorIDs); err != nil {
		return err
	}
	facts.OpenQueues, err = loadQueues(ctx, tx, p.TenantID, nil, true, inboxIDs, sectorIDs)
	if err != nil {
		return err
	}
	return tx.QueryRow(ctx, `SELECT count(DISTINCT pc.membership_id) FROM presence_connections pc JOIN sector_memberships sm ON sm.tenant_id=pc.tenant_id AND sm.membership_id=pc.membership_id AND sm.sector_id = ANY($2::uuid[]) WHERE pc.tenant_id=$1 AND pc.expires_at>$3`, p.TenantID, sectorIDs, facts.Now).Scan(&facts.OnlineAttendants)
}

func loadNames(ctx context.Context, tx pgx.Tx, p Principal, inboxIDs []uuid.UUID, facts *Facts) error {
	rows, err := tx.Query(ctx, `SELECT id, name FROM sectors WHERE tenant_id=$1`, p.TenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		facts.Names.Sectors[id] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, name FROM inboxes WHERE tenant_id=$1 AND id = ANY($2::uuid[])`, p.TenantID, inboxIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		facts.Names.Inboxes[id] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, inbox := range inboxIDs {
		var after *uuid.UUID
		for {
			page, err := attendanceStore.OperatorDirectory(ctx, tx, p.TenantID, p.UserID, p.MembershipID, inbox, after, 200)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			for _, entry := range page {
				if entry.Label != "" {
					facts.Names.Members[entry.MembershipID] = entry.Label
				}
			}
			last := page[len(page)-1].MembershipID
			after = &last
			if len(page) < 200 {
				break
			}
		}
	}
	return nil
}

func loadCycles(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, inboxes, sectors []uuid.UUID, from, to time.Time) ([]Cycle, []uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT cy.conversation_id, cy.cycle_number, cy.opened_at, cy.closed_at, COALESCE(cy.close_reason,''), COALESCE(cy.close_result,''), COALESCE(cy.close_author_kind,''), cy.close_membership_id, c.inbox_id, c.sector_id FROM conversation_cycles cy JOIN conversations c ON c.tenant_id=cy.tenant_id AND c.id=cy.conversation_id WHERE cy.tenant_id=$1 AND c.inbox_id = ANY($2::uuid[]) AND c.sector_id = ANY($3::uuid[]) AND cy.opened_at < $5 AND (cy.closed_at IS NULL OR cy.closed_at >= $4)`, tenant, inboxes, sectors, from, to)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var cycles []Cycle
	seen := map[uuid.UUID]struct{}{}
	var ids []uuid.UUID
	for rows.Next() {
		var cycle Cycle
		var member *uuid.UUID
		if err := rows.Scan(&cycle.ConversationID, &cycle.CycleNumber, &cycle.Opened, &cycle.Closed, &cycle.CloseReason, &cycle.CloseResult, &cycle.CloseAuthorKind, &member, &cycle.InboxID, &cycle.SectorID); err != nil {
			return nil, nil, err
		}
		if member != nil {
			cycle.CloseMembership = *member
		}
		cycles = append(cycles, cycle)
		if _, ok := seen[cycle.ConversationID]; !ok {
			seen[cycle.ConversationID] = struct{}{}
			ids = append(ids, cycle.ConversationID)
		}
	}
	return cycles, ids, rows.Err()
}

func loadEvents(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID, until time.Time) ([]Event, error) {
	rows, err := tx.Query(ctx, `SELECT id, conversation_id, cycle_number, kind, at, assignee_id FROM conversation_events WHERE tenant_id=$1 AND conversation_id = ANY($2::uuid[]) AND at < $3 ORDER BY at, id`, tenant, convs, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.ConversationID, &event.CycleNumber, &event.Kind, &event.At, &event.Assignee); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func loadMessages(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID, until time.Time) ([]Message, error) {
	rows, err := tx.Query(ctx, `SELECT m.id, m.conversation_id, c.inbox_id, COALESCE(m.trusted_at, m.ingested_at), m.direction, m.origin, m.visibility, COALESCE(m.local_state,''), m.author_membership_id, m.historical FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id WHERE m.tenant_id=$1 AND m.conversation_id = ANY($2::uuid[]) AND COALESCE(m.trusted_at, m.ingested_at) < $3`, tenant, convs, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var message Message
		var author *uuid.UUID
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.InboxID, &message.At, &message.Direction, &message.Origin, &message.Visibility, &message.State, &author, &message.Historical); err != nil {
			return nil, err
		}
		if author != nil {
			message.Author = *author
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func loadSnoozes(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID, now time.Time) ([]Snooze, error) {
	rows, err := tx.Query(ctx, `SELECT conversation_id, created_at, COALESCE(resolved_at, $3) FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id = ANY($2::uuid[]) AND created_at < $3`, tenant, convs, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snooze
	for rows.Next() {
		var snooze Snooze
		if err := rows.Scan(&snooze.ConversationID, &snooze.Start, &snooze.End); err != nil {
			return nil, err
		}
		out = append(out, snooze)
	}
	return out, rows.Err()
}

func loadQueues(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID, openOnly bool, scope ...[]uuid.UUID) ([]Queue, error) {
	var rows pgx.Rows
	var err error
	if openOnly {
		rows, err = tx.Query(ctx, `SELECT q.conversation_id, q.sector_id, q.started_at, q.ended_at, COALESCE(an.priority,'normal') FROM assignment_queue_intervals q JOIN conversations c ON c.tenant_id=q.tenant_id AND c.id=q.conversation_id LEFT JOIN conversation_annotations an ON an.tenant_id=q.tenant_id AND an.conversation_id=q.conversation_id WHERE q.tenant_id=$1 AND q.ended_at IS NULL AND c.inbox_id = ANY($2::uuid[]) AND q.sector_id = ANY($3::uuid[])`, tenant, scope[0], scope[1])
	} else {
		rows, err = tx.Query(ctx, `SELECT q.conversation_id, q.sector_id, q.started_at, q.ended_at, COALESCE(an.priority,'normal') FROM assignment_queue_intervals q LEFT JOIN conversation_annotations an ON an.tenant_id=q.tenant_id AND an.conversation_id=q.conversation_id WHERE q.tenant_id=$1 AND q.conversation_id = ANY($2::uuid[])`, tenant, convs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Queue
	for rows.Next() {
		var queue Queue
		if err := rows.Scan(&queue.ConversationID, &queue.SectorID, &queue.Start, &queue.End, &queue.Priority); err != nil {
			return nil, err
		}
		out = append(out, queue)
	}
	return out, rows.Err()
}

func loadTags(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID) ([]Tag, error) {
	rows, err := tx.Query(ctx, `SELECT t.conversation_id, g.name FROM conversation_annotation_tags t JOIN annotation_tags g ON g.tenant_id=t.tenant_id AND g.id=t.tag_id WHERE t.tenant_id=$1 AND t.conversation_id = ANY($2::uuid[])`, tenant, convs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ConversationID, &tag.Name); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

func loadTriageReasons(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, convs []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `SELECT conversation_id, detail FROM triage_events WHERE tenant_id=$1 AND conversation_id = ANY($2::uuid[]) ORDER BY created_at, id`, tenant, convs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var detail []byte
		if err := rows.Scan(&id, &detail); err != nil {
			return nil, err
		}
		if reason := triageReason(detail); reason != "" {
			out[id] = reason
		}
	}
	return out, rows.Err()
}

func triageReason(detail []byte) string {
	var outcome struct {
		Actions []struct {
			Kind   string `json:"kind"`
			Reason string `json:"reason"`
		} `json:"actions"`
	}
	if json.Unmarshal(detail, &outcome) != nil {
		return ""
	}
	var reason string
	for _, action := range outcome.Actions {
		if action.Kind == triage.KindRecordReason && action.Reason != "" {
			reason = action.Reason
		}
	}
	return reason
}

func loadLive(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, inboxes, sectors []uuid.UUID) ([]LiveConversation, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.status, c.assignee_id, c.sector_id, EXISTS(SELECT 1 FROM conversation_snoozes s WHERE s.tenant_id=c.tenant_id AND s.conversation_id=c.id AND s.state='active'), COALESCE(an.priority,'normal') FROM conversations c LEFT JOIN conversation_annotations an ON an.tenant_id=c.tenant_id AND an.conversation_id=c.id WHERE c.tenant_id=$1 AND c.status <> 'closed' AND c.inbox_id = ANY($2::uuid[]) AND c.sector_id = ANY($3::uuid[])`, tenant, inboxes, sectors)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveConversation
	for rows.Next() {
		var row LiveConversation
		var assignee *uuid.UUID
		if err := rows.Scan(&row.ID, &row.Status, &assignee, &row.SectorID, &row.Snoozed, &row.Priority); err != nil {
			return nil, err
		}
		if assignee != nil {
			row.Assignee = *assignee
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
