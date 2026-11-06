-- +goose Up
-- Keep the object association at the same commit that erases its attachment.
-- Periodic maps alone lose objects created and erased between exports. Legacy
-- tombstones remain null; no guessed key is reconstructed for them.
ALTER TABLE deletion_records
 ADD COLUMN object_key text,
 ADD COLUMN object_version text,
 ADD COLUMN object_bytes bigint,
 ADD CONSTRAINT deletion_records_object_chk CHECK (
  (object_key IS NULL AND object_version IS NULL AND object_bytes IS NULL)
  OR (subject='attachment' AND object_key IS NOT NULL AND length(object_key) BETWEEN 1 AND 200
      AND object_version IS NOT NULL AND object_bytes IS NOT NULL AND object_bytes>0)
 );

-- Redact event content in place: never delete the sequence/cursor or alter
-- event identity during subject erasure. All original tenant RLS stays forced.
GRANT UPDATE(payload) ON realtime_events TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback descartaria a associação durável de objetos excluídos'; END $$;
-- +goose StatementEnd
