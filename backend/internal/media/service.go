// Package media records received and sent media on top of the existing
// private attachment storage. It does not replace upload, validation or
// signed download.
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// FFmpegMissingDetail is the explicit refusal stored and returned when
	// the converter binary is absent. The original object is left in place.
	FFmpegMissingDetail = "ffmpeg não encontrado; conversão recusada; arquivo original preservado"
	// FFmpegFailedDetail is stored when a converter runs and does not produce
	// a publishable file. The original object stays the stored media.
	FFmpegFailedDetail = "ffmpeg falhou; conversão recusada; arquivo original preservado"
)

var (
	ErrInvalid       = errors.New("mídia inválida")
	ErrForbidden     = errors.New("acesso negado à mídia")
	ErrConflict      = errors.New("estado de mídia incompatível")
	ErrFFmpegMissing = errors.New(FFmpegMissingDetail)
	ErrConvert       = errors.New(FFmpegFailedDetail)
	ErrStorage       = errors.New("armazenamento de mídia indisponível")
)

// Service reads validated attachments and writes media metadata.
type Service struct {
	DB          *db.Store
	Attachments *attachments.Service
	Now         func() time.Time
	LookFFmpeg  func() (string, error)
	RunFFmpeg   func(context.Context, string, []string) error
}

// AcceptInput cites an existing internal message. Direction and bytes come
// from the validated attachment, never from the client.
type AcceptInput struct {
	AttachmentID     uuid.UUID
	MessageID        *uuid.UUID
	ReplyToMessageID *uuid.UUID
	Voice            bool
}

// MediaItem is the panel view. Object keys and hashes stay internal.
type MediaItem struct {
	ID                 uuid.UUID  `json:"id"`
	AttachmentID       uuid.UUID  `json:"attachment_id"`
	MessageID          *uuid.UUID `json:"message_id,omitempty"`
	ReplyToMessageID   *uuid.UUID `json:"reply_to_message_id,omitempty"`
	Direction          string     `json:"direction"`
	Kind               string     `json:"kind"`
	MediaType          string     `json:"media_type"`
	SizeBytes          int64      `json:"size_bytes"`
	DurationMS         *int64     `json:"duration_ms,omitempty"`
	DurationSource     string     `json:"duration_source"`
	ConversionState    string     `json:"conversion_state"`
	ConversionError    string     `json:"conversion_error,omitempty"`
	OutputAttachmentID *uuid.UUID `json:"output_attachment_id,omitempty"`
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) run(ctx context.Context, p auth.Principal, inbox, conv uuid.UUID, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.DB == nil || s.Attachments == nil || s.Attachments.Storage == nil || p.SessionID == uuid.Nil || p.IsSuperadmin || p.EnrollmentOnly || inbox == uuid.Nil || conv == uuid.Nil {
		return ErrForbidden
	}
	if gate, ok := s.Attachments.Storage.(interface{ Available() bool }); ok && !gate.Available() {
		return ErrStorage
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return s.DB.WithTenantTx(ctx, p.TenantID, func(c context.Context, tx pgx.Tx) error {
		if e := sectors.RequireConversation(c, tx, sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, inbox, conv); e != nil {
			return e
		}
		var live bool
		if e := tx.QueryRow(c, `SELECT collision_live_session($1,$2,$3,$4)`, p.SessionID, p.TenantID, p.MembershipID, s.now()).Scan(&live); e != nil {
			return e
		}
		if !live {
			return ErrForbidden
		}
		return fn(c, tx)
	})
}

// Accept records type, size and container duration for a validated attachment.
// ReplyToMessageID must already be an internal message of the same conversation.
func (s *Service) Accept(ctx context.Context, p auth.Principal, inbox, conv uuid.UUID, in AcceptInput) (MediaItem, error) {
	var out MediaItem
	if in.AttachmentID == uuid.Nil {
		return out, ErrInvalid
	}
	if in.MessageID != nil && in.ReplyToMessageID != nil && *in.MessageID == *in.ReplyToMessageID {
		return out, ErrInvalid
	}
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		existing, e := loadByAttachment(c, tx, p.TenantID, conv, in.AttachmentID)
		if e == nil {
			if !sameCitation(existing, in) {
				return ErrConflict
			}
			out = existing
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		row, e := loadAttachment(c, tx, p.TenantID, conv, in.AttachmentID)
		if e != nil {
			return e
		}
		if row.state != "validated" || row.version == "" || row.mediaType == "" {
			return ErrConflict
		}
		if e = cite(c, tx, p.TenantID, conv, in.MessageID, in.ReplyToMessageID); e != nil {
			return e
		}
		body, e := readExact(c, s.Attachments.Storage, row)
		if e != nil {
			return e
		}
		kind, e := kindOf(row.mediaType, in.Voice, row.direction)
		if e != nil {
			return e
		}
		source := "absent"
		var duration *int64
		if ms, ok := ContainerDuration(row.mediaType, body); ok {
			source = "container"
			duration = &ms
		}
		out = MediaItem{
			ID: uuid.New(), AttachmentID: in.AttachmentID, MessageID: in.MessageID, ReplyToMessageID: in.ReplyToMessageID,
			Direction: row.direction, Kind: kind, MediaType: row.mediaType, SizeBytes: int64(len(body)),
			DurationMS: duration, DurationSource: source, ConversionState: "original",
		}
		_, e = tx.Exec(c, `
			INSERT INTO media_items(
				tenant_id,id,conversation_id,message_id,reply_to_message_id,attachment_id,
				direction,kind,media_type,size_bytes,duration_ms,duration_source
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			p.TenantID, out.ID, conv, in.MessageID, in.ReplyToMessageID, in.AttachmentID,
			out.Direction, out.Kind, out.MediaType, out.SizeBytes, duration, source)
		return e
	})
	return out, e
}

// Get returns metadata for one media item in the authorized conversation.
func (s *Service) Get(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID) (MediaItem, error) {
	var out MediaItem
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		var e error
		out, e = loadItem(c, tx, p.TenantID, conv, id)
		return e
	})
	return out, e
}

type storedAttachment struct {
	id, key, version, direction, state, mediaType, digest, name string
	bytes                                                       int64
	attachmentID                                                uuid.UUID
}

func loadAttachment(ctx context.Context, tx pgx.Tx, tenant, conv, id uuid.UUID) (storedAttachment, error) {
	var row storedAttachment
	e := tx.QueryRow(ctx, `
		SELECT id::text, direction, state, media_type, expected_bytes, sha256, object_key, object_version, name
		FROM attachments
		WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3
		FOR UPDATE`, tenant, conv, id).Scan(&row.id, &row.direction, &row.state, &row.mediaType, &row.bytes, &row.digest, &row.key, &row.version, &row.name)
	if errors.Is(e, pgx.ErrNoRows) {
		return row, ErrForbidden
	}
	row.attachmentID = id
	return row, e
}

func readExact(ctx context.Context, storage attachments.Storage, row storedAttachment) ([]byte, error) {
	reader, e := storage.Read(ctx, row.key, row.version)
	if e != nil {
		return nil, ErrStorage
	}
	defer reader.Close()
	b, e := io.ReadAll(io.LimitReader(reader, row.bytes+1))
	if e != nil {
		return nil, ErrStorage
	}
	if int64(len(b)) != row.bytes {
		return nil, ErrConflict
	}
	sum := sha256.Sum256(b)
	if row.digest == "" || !strings.EqualFold(row.digest, hex.EncodeToString(sum[:])) {
		return nil, ErrStorage
	}
	return b, nil
}

func kindOf(mediaType string, voice bool, direction string) (string, error) {
	audio := strings.HasPrefix(mediaType, "audio/") || mediaType == "application/ogg"
	switch {
	case voice && audio && direction == "outgoing":
		return "voice", nil
	case voice:
		return "", ErrInvalid
	case strings.HasPrefix(mediaType, "image/"):
		return "image", nil
	case audio:
		return "audio", nil
	case strings.HasPrefix(mediaType, "video/"):
		return "video", nil
	case mediaType == "application/pdf":
		return "document", nil
	default:
		return "", ErrInvalid
	}
}

func cite(ctx context.Context, tx pgx.Tx, tenant, conv uuid.UUID, messageID, replyID *uuid.UUID) error {
	if replyID != nil {
		var target uuid.UUID
		e := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, *replyID).Scan(&target)
		if errors.Is(e, pgx.ErrNoRows) || (e == nil && target != conv) {
			return ErrInvalid
		}
		if e != nil {
			return e
		}
	}
	if messageID == nil {
		return nil
	}
	var owner uuid.UUID
	e := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, *messageID).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) || (e == nil && owner != conv) {
		return ErrInvalid
	}
	if e != nil {
		return e
	}
	if replyID == nil {
		return nil
	}
	tag, e := tx.Exec(ctx, `
		UPDATE messages
		SET reply_to_message_id=$4
		WHERE tenant_id=$1 AND id=$2 AND conversation_id=$3
		  AND (reply_to_message_id IS NULL OR reply_to_message_id=$4)`,
		tenant, *messageID, conv, *replyID)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func sameCitation(item MediaItem, in AcceptInput) bool {
	if item.AttachmentID != in.AttachmentID {
		return false
	}
	if !sameUUID(item.MessageID, in.MessageID) || !sameUUID(item.ReplyToMessageID, in.ReplyToMessageID) {
		return false
	}
	if in.Voice {
		return item.Kind == "voice"
	}
	return item.Kind != "voice"
}

func sameUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func loadByAttachment(ctx context.Context, tx pgx.Tx, tenant, conv, attachment uuid.UUID) (MediaItem, error) {
	var id uuid.UUID
	e := tx.QueryRow(ctx, `SELECT id FROM media_items WHERE tenant_id=$1 AND conversation_id=$2 AND attachment_id=$3`, tenant, conv, attachment).Scan(&id)
	if e != nil {
		return MediaItem{}, e
	}
	return loadItem(ctx, tx, tenant, conv, id)
}

func loadItem(ctx context.Context, tx pgx.Tx, tenant, conv, id uuid.UUID) (MediaItem, error) {
	var item MediaItem
	var message, reply, output *uuid.UUID
	var duration *int64
	e := tx.QueryRow(ctx, `
		SELECT id, attachment_id, message_id, reply_to_message_id, direction, kind, media_type,
		       size_bytes, duration_ms, duration_source, conversion_state, conversion_error, output_attachment_id
		FROM media_items
		WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3
		FOR UPDATE`, tenant, conv, id).Scan(
		&item.ID, &item.AttachmentID, &message, &reply, &item.Direction, &item.Kind, &item.MediaType,
		&item.SizeBytes, &duration, &item.DurationSource, &item.ConversionState, &item.ConversionError, &output)
	if errors.Is(e, pgx.ErrNoRows) {
		return item, ErrForbidden
	}
	item.MessageID = message
	item.ReplyToMessageID = reply
	item.OutputAttachmentID = output
	item.DurationMS = duration
	return item, e
}

func bytesReader(b []byte) io.ReadSeeker { return bytes.NewReader(b) }
