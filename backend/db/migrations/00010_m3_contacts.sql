-- +goose Up
ALTER TABLE contacts ADD COLUMN created_by_membership_id uuid,
    ADD COLUMN block_reason text NOT NULL DEFAULT '',
    ADD COLUMN avatar_reference text;
ALTER TABLE contacts ADD CONSTRAINT contacts_creator_fkey FOREIGN KEY (tenant_id, created_by_membership_id) REFERENCES memberships (tenant_id, id),
    ADD CONSTRAINT contacts_block_reason_len CHECK (char_length(block_reason) <= 1000),
    ADD CONSTRAINT contacts_avatar_reference_len CHECK (avatar_reference IS NULL OR char_length(avatar_reference) <= 1024);
CREATE INDEX conversations_contact_history_idx ON conversations (tenant_id, contact_id, created_at DESC, id DESC) WHERE contact_id IS NOT NULL;

-- +goose Down
DROP INDEX conversations_contact_history_idx;
ALTER TABLE contacts DROP COLUMN avatar_reference, DROP COLUMN block_reason, DROP COLUMN created_by_membership_id;
