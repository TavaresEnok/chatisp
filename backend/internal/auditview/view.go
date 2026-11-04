package auditview

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// MaxDays is the longest inclusive civil range.
	MaxDays = 366
	// DefaultLimit is the page size when the caller omits limit.
	DefaultLimit = 50
	// MaxLimit is the largest page the route returns.
	MaxLimit = 100
)

var (
	// ErrForbidden means the actor is not the provider administrator.
	ErrForbidden = errors.New("acesso negado")
	// ErrInvalid means the date range, page size or cursor cannot be read.
	ErrInvalid = errors.New("período inválido")
)

// Actor is the provider member asking for the list.
type Actor struct {
	TenantID uuid.UUID
	Role     string
}

// Filter is an inclusive civil date range in the provider timezone.
type Filter struct {
	From   string
	To     string
	Limit  int
	Cursor string
}

// Event is one audit row safe to show. It has no target id, actor, message or document.
type Event struct {
	CreatedAt  time.Time `json:"created_at"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	Result     string    `json:"result"`
}

// Page is one stable slice of the tenant audit, newest first.
type Page struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// List reads audit_logs for the actor's tenant. The caller owns the tenant transaction.
func List(ctx context.Context, tx pgx.Tx, actor Actor, filter Filter) (Page, error) {
	if tx == nil || actor.TenantID == uuid.Nil || actor.Role != "admin" {
		return Page{}, ErrForbidden
	}
	limit := filter.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, ErrInvalid
	}
	var timezone string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM tenants WHERE id=$1`, actor.TenantID).Scan(&timezone); err != nil {
		return Page{}, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return Page{}, ErrInvalid
	}
	start, end, err := window(filter.From, filter.To, loc)
	if err != nil {
		return Page{}, err
	}
	var cursorAt *time.Time
	var cursorID *uuid.UUID
	if strings.TrimSpace(filter.Cursor) != "" {
		at, id, err := decodeCursor(filter.Cursor)
		if err != nil {
			return Page{}, ErrInvalid
		}
		cursorAt = &at
		cursorID = &id
	}
	rows, err := tx.Query(ctx, `
		SELECT id, action, target_type, result, created_at
		FROM audit_logs
		WHERE tenant_id = $1
		  AND created_at >= $2
		  AND created_at < $3
		  AND (
		    $4::timestamptz IS NULL
		    OR (created_at, id) < ($4::timestamptz, $5::uuid)
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $6
	`, actor.TenantID, start, end, cursorAt, cursorID, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		var event Event
		if err := rows.Scan(&id, &event.Action, &event.TargetType, &event.Result, &event.CreatedAt); err != nil {
			return Page{}, err
		}
		event.CreatedAt = event.CreatedAt.UTC()
		events = append(events, event)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{From: filter.From, To: filter.To, Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		page.NextCursor = encodeCursor(page.Events[limit-1].CreatedAt, ids[limit-1])
	}
	return page, nil
}

func window(from, to string, loc *time.Location) (time.Time, time.Time, error) {
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

func encodeCursor(at time.Time, id uuid.UUID) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (time.Time, uuid.UUID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalid
	}
	stamp, idText, ok := strings.Cut(string(decoded), "|")
	if !ok || strings.Contains(idText, "|") {
		return time.Time{}, uuid.Nil, ErrInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalid
	}
	id, err := uuid.Parse(idText)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, ErrInvalid
	}
	return at.UTC(), id, nil
}
