-- +goose Up
-- Inbound media keeps the message even when the download fails.
-- Reactions are attached to an existing target and are not new messages.

ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages ADD CONSTRAINT messages_content_kind_chk CHECK (
    content_kind IN (
        'text', 'template', 'image', 'document', 'audio', 'voice', 'video',
        'sticker', 'location', 'contacts', 'interactive', 'button'
    )
);

ALTER TABLE media_items DROP CONSTRAINT media_items_kind_chk;
ALTER TABLE media_items ADD CONSTRAINT media_items_kind_chk CHECK (
    kind IN ('image', 'audio', 'voice', 'video', 'document', 'sticker')
);

CREATE TABLE message_reactions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    connector_id uuid NOT NULL,
    conversation_id uuid,
    target_message_id uuid,
    external_reaction_id text NOT NULL,
    target_external_id text NOT NULL,
    emoji text NOT NULL DEFAULT '',
    actor_external_id text NOT NULL DEFAULT '',
    origin_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT message_reactions_external_key UNIQUE (tenant_id, connector_id, external_reaction_id),
    CONSTRAINT message_reactions_actor_key UNIQUE (tenant_id, connector_id, target_external_id, actor_external_id),
    CONSTRAINT message_reactions_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT message_reactions_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT message_reactions_target_fkey
        FOREIGN KEY (tenant_id, target_message_id) REFERENCES messages (tenant_id, id),
    CONSTRAINT message_reactions_external_len CHECK (
        char_length(external_reaction_id) BETWEEN 1 AND 256
        AND char_length(target_external_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT message_reactions_emoji_len CHECK (char_length(emoji) <= 32)
);

CREATE INDEX message_reactions_target_idx
    ON message_reactions (tenant_id, target_message_id);

ALTER TABLE message_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_reactions FORCE ROW LEVEL SECURITY;

CREATE POLICY message_reactions_tenant ON message_reactions
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE message_reactions FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON message_reactions TO chatisp_app;

-- +goose Down
DROP TABLE message_reactions;
ALTER TABLE media_items DROP CONSTRAINT media_items_kind_chk;
ALTER TABLE media_items ADD CONSTRAINT media_items_kind_chk CHECK (
    kind IN ('image', 'audio', 'voice', 'video', 'document')
);
ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages ADD CONSTRAINT messages_content_kind_chk CHECK (content_kind IN ('text', 'template'));
