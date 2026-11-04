-- +goose Up
-- M4B outbound: a mensagem pode sair como mídia já validada.
-- O objeto continua no anexo; esta migração só alarga o tipo gravado.

ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages
    ADD CONSTRAINT messages_content_kind_chk
    CHECK (content_kind IN ('text', 'template', 'image', 'document', 'audio', 'video'));

-- +goose Down
ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages
    ADD CONSTRAINT messages_content_kind_chk
    CHECK (content_kind IN ('text', 'template'));
