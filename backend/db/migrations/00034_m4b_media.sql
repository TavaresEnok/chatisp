-- +goose Up
-- M4B guarda metadados de mídia e citação por id interno.
-- O objeto continua no anexo validado; esta migração não cria storage novo.

ALTER TABLE messages
    ADD COLUMN reply_to_message_id uuid;

ALTER TABLE messages
    ADD CONSTRAINT messages_reply_to_fkey
    FOREIGN KEY (tenant_id, reply_to_message_id) REFERENCES messages (tenant_id, id);

-- +goose StatementBegin
CREATE FUNCTION messages_guard_reply()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_conversation uuid;
BEGIN
    IF NEW.reply_to_message_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NEW.reply_to_message_id = NEW.id THEN
        RAISE EXCEPTION 'citação inválida'
            USING ERRCODE = '23514';
    END IF;
    SELECT conversation_id INTO v_conversation
      FROM public.messages
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.reply_to_message_id;
    IF NOT FOUND OR v_conversation IS DISTINCT FROM NEW.conversation_id THEN
        RAISE EXCEPTION 'citação fora da conversa'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER messages_guard_reply
    BEFORE INSERT OR UPDATE OF reply_to_message_id ON messages
    FOR EACH ROW
    EXECUTE FUNCTION messages_guard_reply();

REVOKE ALL ON FUNCTION messages_guard_reply() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION messages_guard_reply() TO chatisp_app;

CREATE TABLE media_items (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    message_id uuid,
    reply_to_message_id uuid,
    attachment_id uuid NOT NULL,
    output_attachment_id uuid,
    direction text NOT NULL,
    kind text NOT NULL,
    media_type text NOT NULL,
    size_bytes bigint NOT NULL,
    duration_ms bigint,
    duration_source text NOT NULL DEFAULT 'absent',
    conversion_state text NOT NULL DEFAULT 'original',
    conversion_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, attachment_id),
    CONSTRAINT media_items_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT media_items_message_fkey
        FOREIGN KEY (tenant_id, message_id) REFERENCES messages (tenant_id, id),
    CONSTRAINT media_items_reply_fkey
        FOREIGN KEY (tenant_id, reply_to_message_id) REFERENCES messages (tenant_id, id),
    CONSTRAINT media_items_attachment_fkey
        FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id),
    CONSTRAINT media_items_output_fkey
        FOREIGN KEY (tenant_id, output_attachment_id) REFERENCES attachments (tenant_id, id),
    CONSTRAINT media_items_direction_chk CHECK (direction IN ('incoming', 'outgoing')),
    CONSTRAINT media_items_kind_chk CHECK (kind IN ('image', 'audio', 'voice', 'video', 'document')),
    CONSTRAINT media_items_size_chk CHECK (size_bytes > 0),
    CONSTRAINT media_items_duration_chk CHECK (
        (duration_source = 'container' AND duration_ms IS NOT NULL AND duration_ms >= 0)
        OR (duration_source = 'absent' AND duration_ms IS NULL)
    ),
    CONSTRAINT media_items_voice_chk CHECK (
        kind <> 'voice' OR media_type LIKE 'audio/%' OR media_type = 'application/ogg'
    ),
    CONSTRAINT media_items_output_distinct_chk CHECK (
        output_attachment_id IS NULL OR output_attachment_id <> attachment_id
    ),
    CONSTRAINT media_items_conversion_chk CHECK (
        (
            conversion_state = 'original'
            AND output_attachment_id IS NULL
            AND conversion_error = ''
        ) OR (
            conversion_state = 'refused'
            AND output_attachment_id IS NULL
            AND char_length(conversion_error) BETWEEN 1 AND 300
        ) OR (
            conversion_state = 'converted'
            AND output_attachment_id IS NOT NULL
            AND conversion_error = ''
        )
    ),
    CONSTRAINT media_items_error_len CHECK (char_length(conversion_error) <= 300)
);

CREATE INDEX media_items_conversation_idx
    ON media_items (tenant_id, conversation_id, created_at, id);

ALTER TABLE media_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE media_items FORCE ROW LEVEL SECURITY;

CREATE POLICY media_items_tenant ON media_items
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE media_items FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON media_items TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION media_items_guard_reply()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_conversation uuid;
BEGIN
    IF NEW.reply_to_message_id IS NOT NULL THEN
        IF NEW.message_id IS NOT NULL AND NEW.reply_to_message_id = NEW.message_id THEN
            RAISE EXCEPTION 'citação inválida'
                USING ERRCODE = '23514';
        END IF;
        SELECT conversation_id INTO v_conversation
          FROM public.messages
         WHERE tenant_id = NEW.tenant_id
           AND id = NEW.reply_to_message_id;
        IF NOT FOUND OR v_conversation IS DISTINCT FROM NEW.conversation_id THEN
            RAISE EXCEPTION 'citação fora da conversa'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF NEW.message_id IS NOT NULL THEN
        SELECT conversation_id INTO v_conversation
          FROM public.messages
         WHERE tenant_id = NEW.tenant_id
           AND id = NEW.message_id;
        IF NOT FOUND OR v_conversation IS DISTINCT FROM NEW.conversation_id THEN
            RAISE EXCEPTION 'mensagem fora da conversa'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER media_items_guard_reply
    BEFORE INSERT OR UPDATE OF message_id, reply_to_message_id, conversation_id ON media_items
    FOR EACH ROW
    EXECUTE FUNCTION media_items_guard_reply();

REVOKE ALL ON FUNCTION media_items_guard_reply() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION media_items_guard_reply() TO chatisp_app;

-- +goose Down
DROP TRIGGER media_items_guard_reply ON media_items;
DROP FUNCTION media_items_guard_reply();
DROP TABLE media_items;
DROP TRIGGER messages_guard_reply ON messages;
DROP FUNCTION messages_guard_reply();
ALTER TABLE messages DROP CONSTRAINT messages_reply_to_fkey;
ALTER TABLE messages DROP COLUMN reply_to_message_id;
