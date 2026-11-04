package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attachments"
	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/media"
)

// SendMedia enqueues one human media message and its outbox row.
// The idempotent replay is resolved before any private read, conversion or
// new object. A new attempt then takes the shared dispatch order — phone and
// connector, contact, then conversation — before authorization and before
// conversion, so it cannot hold the conversation while waiting for the
// connector. A citation locks the quoted message before the attachment budget
// and before the exclusive conversation lock. commitStored already holds that
// message, then the budget, and its attachment insert asks for KEY SHARE on
// the conversation. Recorded audio locks the shared budget after the citation
// and before the exclusive conversation lock. Image and document sends skip
// the budget. Authorization still finishes before any private read, conversion
// or derived object. A refusal deletes that object. Replay returns the accepted
// attempt and does not convert again.
func (s Service) SendMedia(ctx context.Context, p Principal, in MediaSendInput) (SendResult, error) {
	if s.DB == nil || in.AttachmentID == uuid.Nil || in.ConversationID == uuid.Nil {
		return SendResult{}, store.ErrInvalid
	}
	var out SendResult
	var createdKey, createdVersion string
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		requestID := stringsTrimPolicy(in.ClientRequestID)
		if requestID == "" || in.At.IsZero() || in.ExpectedVersion < 1 {
			return store.ErrInvalid
		}
		if err := lockMediaSend(ctx, tx, p, requestID); err != nil {
			return err
		}
		replay, err := queuedOperation(ctx, queries(tx), p, OpSendMedia, requestID)
		if err != nil {
			return err
		}
		if err = s.authorizePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if !replay {
			// Same statements as text enqueue: policy locks configuration and
			// the contact, then the conversation, before this command takes the
			// exclusive conversation lock or reads storage.
			var contactID uuid.UUID
			err = tx.QueryRow(ctx, `
				SELECT contact_id FROM conversations
				 WHERE tenant_id = $1 AND id = $2 AND inbox_id = $3`,
				p.TenantID, in.ConversationID, in.InboxID).Scan(&contactID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			if _, err = s.admit(ctx, tx, p, contactID, in.InboxID, in.ConversationID, "requested", "freeform", purposeService, "", ""); err != nil {
				return err
			}
			// Quoted message, then budget, then the exclusive conversation lock.
			// commitStored holds the message first. Its insert then takes KEY
			// SHARE on the conversation. Holding that exclusive lock, or the
			// budget, while waiting for the message deadlocks with 40P01.
			if in.ReplyToMessageID != nil {
				if err = lockCitedMessage(ctx, tx, p.TenantID, in.ConversationID, *in.ReplyToMessageID); err != nil {
					return err
				}
			}
			if err = lockRecordedBudget(ctx, tx, p.TenantID, in.AttachmentID); err != nil {
				return err
			}
		}
		kind, mime, name, sendID := "", "", "", in.AttachmentID
		if replay {
			kind, mime, name, err = mediaMeta(ctx, tx, p.TenantID, in.ConversationID, in.AttachmentID)
			if err != nil {
				return err
			}
		} else {
			if err = s.authorizeMediaCommand(ctx, tx, p, in); err != nil {
				return err
			}
			stored := stringsTrimPolicy(in.Body)
			if stored != "" {
				stored, err = s.signHuman(ctx, tx, p, OpSendMedia, requestID, stored)
				if err != nil {
					return err
				}
			}
			if err = whatsapp.ValidMediaCaption(stored); err != nil {
				return store.ErrInvalid
			}
			kind, mime, name, sendID, createdKey, createdVersion, err = s.prepareAuthorizedMedia(ctx, tx, p.TenantID, in.ConversationID, in.AttachmentID)
			if err != nil {
				return err
			}
		}
		return s.enqueueInTx(ctx, tx, p, publicIntent{
			InboxID: in.InboxID, ConversationID: in.ConversationID, ExpectedVersion: in.ExpectedVersion,
			ClientRequestID: requestID, Body: stringsTrimPolicy(in.Body), At: in.At,
			Operation: OpSendMedia, Mode: "requested", Kind: "freeform", Purpose: purposeService,
			ContentKind: kind, AttachmentID: in.AttachmentID, SendAttachmentID: sendID,
			ReplyTo: in.ReplyToMessageID, MIME: mime, FileName: name, TrackConsent: true,
		}, &out)
	})
	if err != nil && createdKey != "" && s.Files != nil {
		dropCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Files.Delete(dropCtx, createdKey, createdVersion)
		cancel()
	}
	return out, err
}

func lockMediaSend(ctx context.Context, tx pgx.Tx, p Principal, requestID string) error {
	key := p.TenantID.String() + "\x1f" + p.MembershipID.String() + "\x1f" + OpSendMedia
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, key, requestID)
	return err
}

// lockCitedMessage takes FOR KEY SHARE on the quoted row before the budget
// and before authorizeMediaCommand upgrades the conversation. The later
// reply foreign key asks for the same mode. A quote outside this conversation
// fails here, before either of those locks. replay and sends without a quote
// do not call this.
func lockCitedMessage(ctx context.Context, tx pgx.Tx, tenant, conv, reply uuid.UUID) error {
	if reply == uuid.Nil {
		return store.ErrInvalid
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM messages
		 WHERE tenant_id = $1 AND id = $2 AND conversation_id = $3
		 FOR KEY SHARE /* send-media-reply */`, tenant, reply, conv).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrInvalid
	}
	return err
}

// lockRecordedBudget takes attachment_budgets after a citation lock and before
// authorizeMediaCommand upgrades the conversation to FOR UPDATE. Image and
// document sends do not reserve here. A missing row is left to authorization.
// The same transaction locks this row again inside PublishReserved.
func lockRecordedBudget(ctx context.Context, tx pgx.Tx, tenant, attachment uuid.UUID) error {
	var mime string
	err := tx.QueryRow(ctx, `SELECT media_type FROM attachments WHERE tenant_id=$1 AND id=$2`, tenant, attachment).Scan(&mime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !needsOpus(mime) {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO attachment_budgets(tenant_id,namespace) VALUES($1,$2) ON CONFLICT DO NOTHING /* send-media-budget */`, tenant, uuid.New()); err != nil {
		return err
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM attachment_budgets WHERE tenant_id=$1 FOR UPDATE /* send-media-budget */`, tenant).Scan(&locked)
	return err
}

func (s Service) authorizeMediaCommand(ctx context.Context, tx pgx.Tx, p Principal, in MediaSendInput) error {
	if err := s.authorizePrincipal(ctx, tx, p); err != nil {
		return err
	}
	conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
	if err != nil {
		return err
	}
	if conv.InboxID != in.InboxID {
		return store.ErrNotFound
	}
	if conv.Version != in.ExpectedVersion {
		return store.ErrStaleVersion
	}
	if conv.Status == store.StatusClosed {
		return store.ErrClosed
	}
	if conv.Controller != store.ControllerHuman || conv.AssigneeID != p.MembershipID {
		return ErrForbidden
	}
	if in.ReplyToMessageID != nil {
		if err := replyBelongs(ctx, tx, p.TenantID, conv.ID, *in.ReplyToMessageID); err != nil {
			return err
		}
	}
	var rowConv uuid.UUID
	var state, direction, mime string
	err = tx.QueryRow(ctx, `
		SELECT conversation_id, state, direction, media_type
		  FROM attachments
		 WHERE tenant_id = $1 AND id = $2`, p.TenantID, in.AttachmentID).Scan(&rowConv, &state, &direction, &mime)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrInvalid
	}
	if err != nil {
		return err
	}
	kind, ok := mediaKindOf(mime)
	if rowConv != conv.ID || state != "validated" || direction != "outgoing" || !ok || kind == "" {
		return store.ErrInvalid
	}
	return nil
}

func replyBelongs(ctx context.Context, tx pgx.Tx, tenant, conv, reply uuid.UUID) error {
	if reply == uuid.Nil {
		return store.ErrInvalid
	}
	var row uuid.UUID
	err := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, reply).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row != conv) {
		return store.ErrInvalid
	}
	return err
}

func mediaMeta(ctx context.Context, tx pgx.Tx, tenant, conv, id uuid.UUID) (string, string, string, error) {
	var rowConv uuid.UUID
	var state, direction, mime, name string
	err := tx.QueryRow(ctx, `
		SELECT conversation_id, state, direction, media_type, name
		  FROM attachments
		 WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&rowConv, &state, &direction, &mime, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", store.ErrInvalid
	}
	if err != nil {
		return "", "", "", err
	}
	kind, ok := mediaKindOf(mime)
	if rowConv != conv || state != "validated" || direction != "outgoing" || !ok {
		return "", "", "", store.ErrInvalid
	}
	return kind, mime, name, nil
}

func (s Service) prepareAuthorizedMedia(ctx context.Context, tx pgx.Tx, tenant, conv, id uuid.UUID) (string, string, string, uuid.UUID, string, string, error) {
	var rowConv uuid.UUID
	var state, direction, mime, name, key, version string
	err := tx.QueryRow(ctx, `
		SELECT conversation_id, state, direction, media_type, name, object_key, object_version
		  FROM attachments
		 WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&rowConv, &state, &direction, &mime, &name, &key, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", uuid.Nil, "", "", store.ErrInvalid
	}
	if err != nil {
		return "", "", "", uuid.Nil, "", "", err
	}
	kind, ok := mediaKindOf(mime)
	if rowConv != conv || state != "validated" || direction != "outgoing" || !ok {
		return "", "", "", uuid.Nil, "", "", store.ErrInvalid
	}
	if !needsOpus(mime) {
		return kind, mime, name, id, "", "", nil
	}
	if _, lookErr := media.LookBinary(); lookErr != nil {
		return "", "", "", uuid.Nil, "", "", media.ErrFFmpegMissing
	}
	if s.Files == nil || version == "" {
		return "", "", "", uuid.Nil, "", "", media.ErrStorage
	}
	if s.AttachmentLimits == nil {
		return "", "", "", uuid.Nil, "", "", attachments.ErrLimit
	}
	reader, err := s.Files.Read(ctx, key, version)
	if err != nil {
		return "", "", "", uuid.Nil, "", "", media.ErrStorage
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
	if err != nil {
		return "", "", "", uuid.Nil, "", "", media.ErrStorage
	}
	produced, err := media.TranscodeRecorded(ctx, mime, raw)
	if err != nil {
		return "", "", "", uuid.Nil, "", "", err
	}
	if len(produced) < 4 || string(produced[:4]) != "OggS" {
		return "", "", "", uuid.Nil, "", "", media.ErrConvert
	}
	publisher := &attachments.Service{DB: s.DB, Storage: s.Files, Limits: s.AttachmentLimits}
	sendID, objectKey, objectVersion, err := publisher.PublishReserved(ctx, tx, tenant, conv, "audio.ogg", "application/ogg", produced, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		return "", "", "", uuid.Nil, "", "", err
	}
	return "audio", "application/ogg", "audio.ogg", sendID, objectKey, objectVersion, nil
}

func mediaKindOf(mime string) (string, bool) {
	switch {
	case strings.HasPrefix(mime, "image/") && mime != "image/svg+xml":
		return "image", true
	case mime == "application/pdf":
		return "document", true
	case mime == "video/mp4":
		return "video", true
	case mime == "audio/webm" || mime == "video/webm" || mime == "audio/ogg" || mime == "application/ogg" || mime == "video/ogg" || strings.HasPrefix(mime, "audio/"):
		return "audio", true
	default:
		return "", false
	}
}

func needsOpus(mime string) bool {
	switch mime {
	case "audio/webm", "video/webm", "audio/ogg", "application/ogg", "video/ogg":
		return true
	default:
		return false
	}
}

func bindOutboundMedia(ctx context.Context, tx pgx.Tx, tenant, conv, original, send uuid.UUID, kind string) error {
	if send == uuid.Nil {
		send = original
	}
	ids := []uuid.UUID{original}
	if send != original {
		ids = append(ids, send)
	}
	for _, id := range ids {
		var rowConv uuid.UUID
		var state, direction, mime string
		err := tx.QueryRow(ctx, `
			SELECT conversation_id, state, direction, media_type
			  FROM attachments
			 WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&rowConv, &state, &direction, &mime)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrInvalid
		}
		if err != nil {
			return err
		}
		got, ok := mediaKindOf(mime)
		if rowConv != conv || state != "validated" || direction != "outgoing" || !ok || got != kind {
			return store.ErrInvalid
		}
	}
	return nil
}

func bindReply(ctx context.Context, tx pgx.Tx, tenant, conv, message, reply uuid.UUID) error {
	if reply == message {
		return store.ErrInvalid
	}
	var rowConv uuid.UUID
	err := tx.QueryRow(ctx, `SELECT conversation_id FROM messages WHERE tenant_id=$1 AND id=$2`, tenant, reply).Scan(&rowConv)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && rowConv != conv) {
		return store.ErrInvalid
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE messages
		   SET reply_to_message_id = $3
		 WHERE tenant_id = $1 AND id = $2`, tenant, message, reply)
	return err
}
