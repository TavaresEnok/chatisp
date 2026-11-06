-- +goose Up
-- Existing records predate proof that ingress guards were committed alongside
-- the erasure. Only a new erasure transaction may set this flag to true.
ALTER TABLE retention_deletion_records
    ADD COLUMN ingress_guarded boolean NOT NULL DEFAULT false;
ALTER TABLE contacts ADD COLUMN privacy_legacy_cutoff timestamptz;
-- A previous transaction's now() may predate events accepted before its
-- commit. Activation time conservatively covers that ambiguity without
-- pretending to reconstruct an identity or exact erasure time.
ALTER TABLE contacts NO FORCE ROW LEVEL SECURITY;
UPDATE contacts SET privacy_legacy_cutoff = clock_timestamp()
    WHERE privacy_erased_at IS NOT NULL;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
CREATE INDEX contacts_privacy_legacy_cutoff_idx ON contacts (tenant_id, privacy_legacy_cutoff)
    WHERE privacy_legacy_cutoff IS NOT NULL;
CREATE INDEX retention_guarded_contacts_idx ON retention_deletion_records (tenant_id, target_id)
    WHERE class = 'content' AND target_type = 'contact' AND ingress_guarded;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria a proveniência da proteção de ingresso';
END $$;
-- +goose StatementEnd
