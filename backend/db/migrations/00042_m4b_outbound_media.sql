-- +goose Up
-- M4B outbound: a mensagem pode sair como mídia já validada.
-- O objeto continua no anexo. 00041 já gravou image, document, audio, video,
-- voice, sticker, location, contacts, interactive e button. O check composto
-- repete a união para não estreitar a entrada ao aplicar esta migração.

ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages
    ADD CONSTRAINT messages_content_kind_chk
    CHECK (content_kind IN (
        'text', 'template', 'image', 'document', 'audio', 'voice', 'video',
        'sticker', 'location', 'contacts', 'interactive', 'button'
    ));

-- +goose Down
-- Volta ao check de 00041. Os tipos de entrada permanecem.
ALTER TABLE messages DROP CONSTRAINT messages_content_kind_chk;
ALTER TABLE messages
    ADD CONSTRAINT messages_content_kind_chk
    CHECK (content_kind IN (
        'text', 'template', 'image', 'document', 'audio', 'voice', 'video',
        'sticker', 'location', 'contacts', 'interactive', 'button'
    ));
