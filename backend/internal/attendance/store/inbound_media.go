package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func stampInbound(ctx context.Context, tx pgx.Tx, in InboundInput, messageID uuid.UUID) error {
	if in.ContentKind == "" && len(in.Content) == 0 && in.ReplyTo == uuid.Nil {
		return nil
	}
	kind := in.ContentKind
	if kind == "" {
		kind = "text"
	}
	raw := in.Content
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var reply any
	if in.ReplyTo != uuid.Nil {
		reply = in.ReplyTo
	}
	_, err := tx.Exec(ctx, `
		UPDATE messages
		   SET content_kind = $3,
		       content_context = $4::jsonb,
		       reply_to_message_id = $5
		 WHERE tenant_id = $1
		   AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND id = $2
	`, in.TenantID, messageID, kind, raw, reply)
	return err
}

// PublishConversationEvent appends one realtime fact for the panel.
func (Repo) PublishConversationEvent(ctx context.Context, tx pgx.Tx, tenantID, conversationID uuid.UUID, version int64, eventType string, fields map[string]any) error {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if conversationID == uuid.Nil || version < 1 || eventType == "" {
		return ErrInvalid
	}
	return publish(ctx, queries(tx), tenantID, eventType, "conversation", conversationID, version, fields)
}

func enrichInbound(ctx context.Context, tx pgx.Tx, tenantID, conversationID uuid.UUID, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	index := map[uuid.UUID]int{}
	for i := range messages {
		index[messages[i].ID] = i
	}
	rows, err := tx.Query(ctx, `
		SELECT id, content_kind, content_context, reply_to_message_id
		  FROM messages
		 WHERE tenant_id = $1
		   AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND conversation_id = $2
	`, tenantID, conversationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var kind string
		var content []byte
		var reply *uuid.UUID
		if err := rows.Scan(&id, &kind, &content, &reply); err != nil {
			return err
		}
		i, ok := index[id]
		if !ok {
			continue
		}
		messages[i].ContentKind = kind
		if len(content) > 0 && string(content) != "{}" {
			messages[i].Content = json.RawMessage(content)
		}
		if reply != nil {
			messages[i].ReplyToMessageID = *reply
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	reactions, err := tx.Query(ctx, `
		SELECT target_message_id, emoji, external_reaction_id, actor_external_id
		  FROM message_reactions
		 WHERE tenant_id = $1
		   AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND conversation_id = $2
		   AND target_message_id IS NOT NULL
		   AND emoji <> ''
		 ORDER BY updated_at, id
	`, tenantID, conversationID)
	if err != nil {
		return err
	}
	defer reactions.Close()
	for reactions.Next() {
		var target uuid.UUID
		var emoji, external, actor string
		if err := reactions.Scan(&target, &emoji, &external, &actor); err != nil {
			return err
		}
		i, ok := index[target]
		if !ok {
			continue
		}
		messages[i].Reactions = append(messages[i].Reactions, MessageReaction{Emoji: emoji, ExternalID: external, Actor: actor})
	}
	if err := reactions.Err(); err != nil {
		return err
	}
	files, err := tx.Query(ctx, `
		SELECT mi.message_id, a.id, a.name, a.media_type, a.state, a.expected_bytes
		  FROM media_items mi
		  JOIN attachments a ON a.tenant_id = mi.tenant_id AND a.id = mi.attachment_id
		 WHERE mi.tenant_id = $1
		   AND mi.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
		   AND mi.conversation_id = $2
		   AND mi.message_id IS NOT NULL
	`, tenantID, conversationID)
	if err != nil {
		return err
	}
	defer files.Close()
	for files.Next() {
		var messageID uuid.UUID
		var file MessageAttachment
		if err := files.Scan(&messageID, &file.ID, &file.Name, &file.MediaType, &file.State, &file.Bytes); err != nil {
			return err
		}
		i, ok := index[messageID]
		if !ok {
			continue
		}
		copy := file
		messages[i].Attachment = &copy
	}
	return files.Err()
}
