package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AnnotationTag struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}
type AnnotationNote struct {
	ID        uuid.UUID `json:"id"`
	AuthorID  uuid.UUID `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type AnnotationState struct {
	Priority    string            `json:"priority"`
	Version     int64             `json:"version"`
	Tags        []AnnotationTag   `json:"tags"`
	Notes       []AnnotationNote  `json:"notes"`
	Watermark   int64             `json:"watermark"`
	ReadVersion int64             `json:"read_version"`
	Unread      bool              `json:"unread"`
	UnreadCount int64             `json:"unread_count"`
	Events      []AnnotationEvent `json:"events"`
}
type AnnotationEvent struct {
	ID        uuid.UUID  `json:"id"`
	ActorID   uuid.UUID  `json:"actor_id"`
	Kind      string     `json:"kind"`
	SubjectID *uuid.UUID `json:"subject_id"`
	Version   int64      `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
}

// AnnotationCatalog returns the tenant's flat tag catalogue.
func AnnotationCatalog(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]AnnotationTag, error) {
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,name FROM annotation_tags WHERE tenant_id=$1 ORDER BY name,id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnnotationTag{}
	for rows.Next() {
		var t AnnotationTag
		if err = rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func CreateAnnotationTag(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) (AnnotationTag, error) {
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return AnnotationTag{}, err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 40 {
		return AnnotationTag{}, ErrInvalid
	}
	t := AnnotationTag{ID: uuid.New(), Name: name}
	err := tx.QueryRow(ctx, `INSERT INTO annotation_tags(tenant_id,id,name) VALUES($1,$2,$3) ON CONFLICT (tenant_id,name) DO UPDATE SET name=excluded.name RETURNING id`, tenant, t.ID, name).Scan(&t.ID)
	return t, err
}

// ManageAnnotationTag updates or removes a tag and records affected conversation events.
func ManageAnnotationTag(ctx context.Context, tx pgx.Tx, tenant, actor, id uuid.UUID, name string, remove bool) (AnnotationTag, error) {
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return AnnotationTag{}, err
	}
	// Serialize catalogue writers while discovering and locking their potentially
	// overlapping conversation sets. This is distinct from the tenant gate.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chatisp-annotation-catalog'), hashtext($1::text))`, tenant.String()); err != nil {
		return AnnotationTag{}, err
	}
	ids, err := annotationTagConversations(ctx, tx, tenant, id)
	if err != nil {
		return AnnotationTag{}, err
	}
	if err = lockAnnotationConversations(ctx, tx, tenant, ids); err != nil {
		return AnnotationTag{}, err
	}
	var tag AnnotationTag
	err = tx.QueryRow(ctx, `SELECT id,name FROM annotation_tags WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&tag.ID, &tag.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return tag, ErrNotFound
	}
	if err != nil {
		return tag, err
	}
	name = strings.TrimSpace(name)
	if !remove && (len([]rune(name)) < 1 || len([]rune(name)) > 40) {
		return tag, ErrInvalid
	}
	if !remove && name == tag.Name {
		return tag, nil
	}
	// An association may have committed during the first scan or while waiting
	// for the tag. The tag lock now excludes additions; re-read and pin every
	// referenced conversation before metadata or the realtime cursor is locked.
	ids, err = annotationTagConversations(ctx, tx, tenant, id)
	if err != nil {
		return tag, err
	}
	if err = lockAnnotationConversations(ctx, tx, tenant, ids); err != nil {
		return tag, err
	}
	// All conversation FK locks precede all aggregate locks and publication.
	for _, c := range ids {
		if _, _, err = lockAnnotation(ctx, tx, tenant, c); err != nil {
			return tag, err
		}
	}
	for _, c := range ids {
		kind := "tag_rename"
		if remove {
			kind = "tag_remove"
		}
		if _, err = AnnotationChange(ctx, tx, tenant, c, actor, kind, id, "", ""); err != nil {
			return tag, err
		}
	}
	if remove {
		_, err = tx.Exec(ctx, `DELETE FROM annotation_tags WHERE tenant_id=$1 AND id=$2`, tenant, id)
	} else {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM annotation_tags WHERE tenant_id=$1 AND name=$2 AND id<>$3)`, tenant, name, id).Scan(&exists); err != nil {
			return tag, err
		}
		if exists {
			return tag, ErrIdempotencyConflict
		}
		_, err = tx.Exec(ctx, `UPDATE annotation_tags SET name=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, name)
		tag.Name = name
	}
	return tag, err
}

func annotationTagConversations(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT conversation_id FROM conversation_annotation_tags WHERE tenant_id=$1 AND tag_id=$2 ORDER BY conversation_id`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var c uuid.UUID
		if err = rows.Scan(&c); err != nil {
			return nil, err
		}
		ids = append(ids, c)
	}
	return ids, rows.Err()
}

func lockAnnotationConversations(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []uuid.UUID) error {
	for _, c := range ids {
		var found uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE`, tenant, c).Scan(&found); err != nil {
			return err
		}
	}
	return nil
}
func AnnotationRead(ctx context.Context, tx pgx.Tx, tenant, user, conversation uuid.UUID) (AnnotationState, error) {
	out := AnnotationState{Priority: "normal", Version: 1, Tags: []AnnotationTag{}, Notes: []AnnotationNote{}, Events: []AnnotationEvent{}}
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return out, err
	}
	// Even a previously untouched conversation needs a lockable aggregate.
	// Hold it throughout the multi-statement read, so all metadata and its
	// timeline represent the same committed version.
	var err error
	out.Version, out.Priority, err = lockAnnotation(ctx, tx, tenant, conversation)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.name FROM annotation_tags t JOIN conversation_annotation_tags a ON a.tenant_id=t.tenant_id AND a.tag_id=t.id WHERE a.tenant_id=$1 AND a.conversation_id=$2 ORDER BY t.name,t.id`, tenant, conversation)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t AnnotationTag
		if err = rows.Scan(&t.ID, &t.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.Tags = append(out.Tags, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT id,author_id,body,created_at FROM conversation_notes WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at,id`, tenant, conversation)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var n AnnotationNote
		if err = rows.Scan(&n.ID, &n.AuthorID, &n.Body, &n.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Notes = append(out.Notes, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT coalesce(r.watermark,0),coalesce(r.unread,false),coalesce(r.version,0), (SELECT count(*) FROM annotation_message_cursors m WHERE m.tenant_id=$1 AND m.conversation_id=$3 AND (m.seq>coalesce(r.watermark,0) OR (r.user_id IS NULL AND m.seq=0))) FROM (SELECT 1) x LEFT JOIN conversation_read_markers r ON r.tenant_id=$1 AND r.user_id=$2 AND r.conversation_id=$3`, tenant, user, conversation).Scan(&out.Watermark, &out.Unread, &out.ReadVersion, &out.UnreadCount)
	if err != nil {
		return out, err
	}
	out.Unread = out.Unread || out.UnreadCount > 0
	rows, err = tx.Query(ctx, `SELECT id,actor_id,kind,subject_id,version,created_at FROM conversation_annotation_events WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY version`, tenant, conversation)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e AnnotationEvent
		if err = rows.Scan(&e.ID, &e.ActorID, &e.Kind, &e.SubjectID, &e.Version, &e.CreatedAt); err != nil {
			return out, err
		}
		out.Events = append(out.Events, e)
	}
	return out, rows.Err()
}

func lockAnnotation(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID) (int64, string, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO conversation_annotations(tenant_id,conversation_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, tenant, conversation); err != nil {
		return 0, "", err
	}
	var version int64
	var priority string
	err := tx.QueryRow(ctx, `SELECT version,priority FROM conversation_annotations WHERE tenant_id=$1 AND conversation_id=$2 FOR UPDATE`, tenant, conversation).Scan(&version, &priority)
	return version, priority, err
}

// AnnotationChange serializes metadata changes independently of conversation version.
func AnnotationChange(ctx context.Context, tx pgx.Tx, tenant, conversation, actor uuid.UUID, kind string, subject uuid.UUID, value, request string) (int64, error) {
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return 0, err
	}
	// Catalogue management locks the tag before its aggregates. Match that
	// order for association inserts, whose FK also locks the tag row.
	if kind == "tag_add" && subject != uuid.Nil {
		var found uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM annotation_tags WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE`, tenant, subject).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, err
		}
	}
	version, priority, err := lockAnnotation(ctx, tx, tenant, conversation)
	if err != nil {
		return 0, err
	}
	changed := false
	switch kind {
	case "note":
		if strings.TrimSpace(value) == "" || len([]rune(value)) > 16000 || strings.TrimSpace(request) != request || len(request) < 1 || len(request) > 128 {
			return 0, ErrInvalid
		}
		subject = uuid.New()
		var id, existingConv uuid.UUID
		var body string
		err := tx.QueryRow(ctx, `INSERT INTO conversation_notes(tenant_id,id,conversation_id,author_id,request_id,body) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id,author_id,request_id) DO NOTHING RETURNING id`, tenant, subject, conversation, actor, request, value).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT id,conversation_id,body FROM conversation_notes WHERE tenant_id=$1 AND author_id=$2 AND request_id=$3`, tenant, actor, request).Scan(&id, &existingConv, &body)
			if err != nil {
				return 0, err
			}
			if existingConv != conversation || body != value {
				return 0, ErrIdempotencyConflict
			}
			return version, nil
		}
		if err != nil {
			return 0, err
		}
		subject = id
		changed = true
	case "tag_add":
		if subject == uuid.Nil {
			return 0, ErrInvalid
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM annotation_tags WHERE tenant_id=$1 AND id=$2)`, tenant, subject).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return 0, ErrNotFound
		}
		tag, err := tx.Exec(ctx, `INSERT INTO conversation_annotation_tags VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, conversation, subject)
		if err != nil {
			return 0, err
		}
		changed = tag.RowsAffected() > 0
	case "tag_remove":
		if subject == uuid.Nil {
			return 0, ErrInvalid
		}
		tag, err := tx.Exec(ctx, `DELETE FROM conversation_annotation_tags WHERE tenant_id=$1 AND conversation_id=$2 AND tag_id=$3`, tenant, conversation, subject)
		if err != nil {
			return 0, err
		}
		changed = tag.RowsAffected() > 0
	case "tag_rename":
		if subject == uuid.Nil {
			return 0, ErrInvalid
		}
		changed = true
	case "priority":
		if value != "normal" && value != "urgent" {
			return 0, ErrInvalid
		}
		changed = value != priority
		if changed {
			if _, err := tx.Exec(ctx, `UPDATE conversation_annotations SET priority=$3 WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conversation, value); err != nil {
				return 0, err
			}
		}
	default:
		return 0, ErrInvalid
	}
	if !changed {
		return version, nil
	}
	version++
	if _, err := tx.Exec(ctx, `UPDATE conversation_annotations SET version=$3 WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conversation, version); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO conversation_annotation_events(tenant_id,id,conversation_id,actor_id,kind,subject_id,version) VALUES($1,$2,$3,$4,$5,$6,$7)`, tenant, uuid.New(), conversation, actor, kind, subject, version); err != nil {
		return 0, err
	}
	if err := publish(ctx, queries(tx), tenant, "conversation.annotation", "conversation", conversation, version, map[string]any{"conversation_id": conversation, "subject_id": subject}); err != nil {
		return 0, err
	}
	return version, nil
}
func AnnotationMarkRead(ctx context.Context, tx pgx.Tx, tenant, user, conversation uuid.UUID, watermark int64, unread bool, version int64) error {
	if err := requireTenant(ctx, tx, tenant); err != nil {
		return err
	}
	if watermark < 0 || version < 0 {
		return ErrInvalid
	}
	ceiling, err := CursorCeiling(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if watermark > ceiling {
		return ErrInvalid
	}
	tag, err := tx.Exec(ctx, `INSERT INTO conversation_read_markers(tenant_id,user_id,conversation_id,watermark,unread) SELECT $1,$2,$3,$4,$5 WHERE $6=0 ON CONFLICT (tenant_id,user_id,conversation_id) DO NOTHING`, tenant, user, conversation, watermark, unread, version)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	tag, err = tx.Exec(ctx, `UPDATE conversation_read_markers SET watermark=greatest(watermark,$4), unread=$5, version=version+1 WHERE tenant_id=$1 AND user_id=$2 AND conversation_id=$3 AND version=$6`, tenant, user, conversation, watermark, unread, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIdempotencyConflict
	}
	return nil
}
