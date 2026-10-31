package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type QueueInput struct {
	TenantID, UserID, MembershipID       uuid.UUID
	Role, Tab, Order, Status, Search     string
	SectorID, AssigneeID, InboxID, TagID uuid.UUID
	From, To                             *time.Time
	Limit                                int
	Cursor                               string
}
type QueueItem struct {
	ID            uuid.UUID  `json:"id"`
	InboxID       uuid.UUID  `json:"inbox_id"`
	SectorID      uuid.UUID  `json:"sector_id"`
	AssigneeID    *uuid.UUID `json:"assignee_id"`
	ContactID     uuid.UUID  `json:"contact_id"`
	ContactLabel  string     `json:"contact_label"`
	Protocol      string     `json:"protocol"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	Unread        bool       `json:"unread"`
	UnreadCount   int64      `json:"unread_count"`
	WaitSince     time.Time  `json:"wait_since"`
	LastMessageAt time.Time  `json:"last_message_at"`
}
type QueueCounts struct {
	Mine       int64 `json:"mine"`
	Unassigned int64 `json:"unassigned"`
	All        int64 `json:"all"`
}
type QueuePage struct {
	Items           []QueueItem `json:"items"`
	Counts          QueueCounts `json:"counts"`
	PageCursor      string      `json:"page_cursor"`
	EventNextCursor int64       `json:"event_next_cursor"`
}
type queueCursor struct {
	Scope       string    `json:"s"`
	Fingerprint string    `json:"f"`
	At          time.Time `json:"t"`
	ID          uuid.UUID `json:"i"`
	Ceiling     int64     `json:"e"`
}

// Queue pins the authorizing rows before evaluating the page in one statement
// snapshot. A changed result set invalidates continuation.
func Queue(ctx context.Context, tx pgx.Tx, in QueueInput) (QueuePage, error) {
	var out QueuePage
	if err := requireTenant(ctx, tx, in.TenantID); err != nil {
		return out, err
	}
	if in.Tab == "" {
		in.Tab = "all"
	}
	if in.Order == "" {
		in.Order = "wait"
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if (in.Tab != "all" && in.Tab != "mine" && in.Tab != "unassigned") || (in.Order != "wait" && in.Order != "lastMessage") || in.Limit < 1 || in.Limit > 100 || len(in.Search) > 200 || (in.Status != "" && in.Status != "open" && in.Status != "waiting_customer" && in.Status != "closed" && in.Status != "triage") || (in.From != nil && in.To != nil && !in.From.Before(*in.To)) {
		return out, ErrInvalid
	}
	in.Search = strings.TrimSpace(in.Search)
	cursorText := in.Cursor
	in.Cursor = ""
	scopeJSON, _ := json.Marshal(in)
	sum := sha256.Sum256(scopeJSON)
	scope := hex.EncodeToString(sum[:])
	var cur queueCursor
	if cursorText != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursorText)
		if e != nil || len(b) > 1024 || json.Unmarshal(b, &cur) != nil || cur.Scope != scope || cur.Fingerprint == "" || cur.At.IsZero() || cur.ID == uuid.Nil || cur.Ceiling < 0 {
			return out, ErrInvalid
		}
	}
	var at any
	var id any
	if cursorText != "" {
		at = cur.At
		id = cur.ID
	}
	// Capture the event ceiling before selecting candidates: events committed
	// during authorization are then available for replay even if their new row
	// was absent from the candidate snapshot.
	var initialCeiling int64
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT seq FROM realtime_tenant_cursors WHERE tenant_id=$1),0)`, in.TenantID).Scan(&initialCeiling); err != nil {
		return out, err
	}
	// Lock all visible candidates, not just a limited page. A second statement
	// obtains a fresh READ COMMITTED snapshot after any concurrent move or
	// revocation that won the lock race. Only rows actually pinned participate.
	lockSQL := `SELECT c.id FROM conversations c JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id WHERE c.tenant_id=$1 ORDER BY c.id FOR SHARE OF c,si`
	args := []any{in.TenantID}
	if in.Role != "admin" && in.Role != "supervisor" {
		lockSQL = `SELECT c.id FROM conversations c JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id JOIN sectors s ON s.tenant_id=si.tenant_id AND s.id=si.sector_id AND s.active JOIN sector_memberships sm ON sm.tenant_id=c.tenant_id AND sm.sector_id=c.sector_id AND sm.membership_id=$2 WHERE c.tenant_id=$1 ORDER BY c.id FOR SHARE OF c,si,s,sm`
		args = append(args, in.MembershipID)
	}
	rows, err := tx.Query(ctx, lockSQL, args...)
	if err != nil {
		return out, err
	}
	locked := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		locked = append(locked, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var raw []byte
	var fingerprint string
	var ceiling int64
	err = tx.QueryRow(ctx, queueSQL, in.TenantID, in.UserID, in.MembershipID, in.Role, in.Tab, in.Order, in.Status, nullableQueueID(in.SectorID), nullableQueueID(in.AssigneeID), nullableQueueID(in.InboxID), nullableQueueID(in.TagID), in.From, in.To, in.Search, in.Limit, at, id, locked).Scan(&raw, &fingerprint, &ceiling)
	if err != nil {
		return out, err
	}
	if cursorText != "" && cur.Fingerprint != fingerprint {
		return out, ErrResync
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if cursorText != "" {
		out.EventNextCursor = cur.Ceiling
	} else {
		out.EventNextCursor = initialCeiling
	}
	if len(out.Items) > in.Limit {
		out.Items = out.Items[:in.Limit]
		last := out.Items[len(out.Items)-1]
		sortAt := last.WaitSince
		if in.Order == "lastMessage" {
			sortAt = last.LastMessageAt
		}
		b, _ := json.Marshal(queueCursor{Scope: scope, Fingerprint: fingerprint, At: sortAt, ID: last.ID, Ceiling: out.EventNextCursor})
		out.PageCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}
func nullableQueueID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

const queueSQL = `WITH authorized AS MATERIALIZED (
 SELECT c.*,coalesce(ct.name,'') AS contact_name,coalesce(ct.phone,'') AS phone,
 coalesce(a.priority,'normal') AS priority,coalesce(r.unread,false) AS manual_unread,
 (SELECT count(*) FROM annotation_message_cursors mc WHERE mc.tenant_id=c.tenant_id AND mc.conversation_id=c.id AND (mc.seq>coalesce(r.watermark,0) OR (r.user_id IS NULL AND mc.seq=0))) AS unread_count,
 coalesce(q.started_at,c.created_at) AS wait_since,
 coalesce((SELECT max(m.ingested_at) FROM messages m WHERE m.tenant_id=c.tenant_id AND m.conversation_id=c.id AND m.visibility='public' AND NOT m.historical),c.created_at) AS last_message_at
 FROM conversations c
 JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id
 LEFT JOIN contacts ct ON ct.tenant_id=c.tenant_id AND ct.id=c.contact_id
 LEFT JOIN conversation_annotations a ON a.tenant_id=c.tenant_id AND a.conversation_id=c.id
 LEFT JOIN conversation_read_markers r ON r.tenant_id=c.tenant_id AND r.conversation_id=c.id AND r.user_id=$2
 LEFT JOIN assignment_queue_intervals q ON q.tenant_id=c.tenant_id AND q.conversation_id=c.id AND q.ended_at IS NULL
 WHERE c.tenant_id=$1 AND c.id=ANY($18::uuid[]) AND ($4 IN ('admin','supervisor') OR EXISTS (SELECT 1 FROM sectors s JOIN sector_memberships sm ON sm.tenant_id=s.tenant_id AND sm.sector_id=s.id WHERE s.tenant_id=c.tenant_id AND s.id=c.sector_id AND s.active AND sm.membership_id=$3))
 AND ($7='' OR c.status=$7) AND ($8::uuid IS NULL OR c.sector_id=$8) AND ($9::uuid IS NULL OR c.assignee_id=$9) AND ($10::uuid IS NULL OR c.inbox_id=$10)
 AND ($11::uuid IS NULL OR EXISTS(SELECT 1 FROM conversation_annotation_tags t WHERE t.tenant_id=c.tenant_id AND t.conversation_id=c.id AND t.tag_id=$11))
 AND ($12::timestamptz IS NULL OR c.created_at >= $12) AND ($13::timestamptz IS NULL OR c.created_at < $13)
 AND ($14='' OR m3_search_normalize(ct.name) LIKE '%' || replace(replace(replace(m3_search_normalize($14),'\','\\'),'%','\%'),'_','\_') || '%' OR coalesce(ct.phone,'') LIKE '%' || replace(replace(replace($14,'\','\\'),'%','\%'),'_','\_') || '%' OR coalesce(c.protocol,'') LIKE '%' || replace(replace(replace($14,'\','\\'),'%','\%'),'_','\_') || '%' OR EXISTS (SELECT 1 FROM messages m WHERE m.tenant_id=c.tenant_id AND m.conversation_id=c.id AND m.visibility='public' AND to_tsvector('portuguese',m3_search_normalize(m.body)) @@ websearch_to_tsquery('portuguese',m3_search_normalize($14))))
), shaped AS MATERIALIZED (
 SELECT id,inbox_id,sector_id,assignee_id,contact_id,coalesce(nullif(contact_name,''),nullif(phone,''),'Contato') AS contact_label,coalesce(protocol,'') AS protocol,status,priority,(manual_unread OR unread_count>0) AS unread,unread_count,wait_since,last_message_at FROM authorized
), selected AS MATERIALIZED (
 SELECT *,CASE WHEN $6='wait' THEN wait_since ELSE last_message_at END AS sort_at FROM shaped WHERE $5='all' OR ($5='mine' AND assignee_id=$3) OR ($5='unassigned' AND assignee_id IS NULL)
), page AS (
 SELECT * FROM selected WHERE $16::timestamptz IS NULL OR ($6='wait' AND (sort_at,id)>($16,$17::uuid)) OR ($6='lastMessage' AND (sort_at,id)<($16,$17::uuid))
 ORDER BY CASE WHEN $6='wait' THEN sort_at END ASC,CASE WHEN $6='wait' THEN id END ASC,CASE WHEN $6='lastMessage' THEN sort_at END DESC,CASE WHEN $6='lastMessage' THEN id END DESC LIMIT $15+1
)
 SELECT jsonb_build_object('items',coalesce((SELECT jsonb_agg(to_jsonb(p)-'sort_at' ORDER BY CASE WHEN $6='wait' THEN sort_at END ASC,CASE WHEN $6='wait' THEN id END ASC,CASE WHEN $6='lastMessage' THEN sort_at END DESC,CASE WHEN $6='lastMessage' THEN id END DESC) FROM page p),'[]'::jsonb), 'counts',jsonb_build_object('mine',(SELECT count(*) FROM shaped WHERE assignee_id=$3),'unassigned',(SELECT count(*) FROM shaped WHERE assignee_id IS NULL),'all',(SELECT count(*) FROM shaped))),
 coalesce((SELECT md5(string_agg(row_to_json(s)::text,',' ORDER BY id)) FROM shaped s),md5('')),
 coalesce((SELECT seq FROM realtime_tenant_cursors WHERE tenant_id=$1),0)`
