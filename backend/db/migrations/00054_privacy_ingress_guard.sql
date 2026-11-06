-- +goose Up
CREATE TABLE privacy_ingress_guards (
    tenant_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    key_version text NOT NULL CHECK (length(key_version) BETWEEN 1 AND 128),
    identity_mac bytea NOT NULL CHECK (octet_length(identity_mac) = 32),
    cutoff timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, connector_id, key_version, identity_mac),
    FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id)
);
CREATE INDEX privacy_ingress_guards_cutoff_idx ON privacy_ingress_guards (tenant_id, connector_id, cutoff, key_version);
ALTER TABLE privacy_ingress_guards ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_ingress_guards FORCE ROW LEVEL SECURITY;
CREATE POLICY privacy_ingress_guards_tenant ON privacy_ingress_guards TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
REVOKE ALL ON privacy_ingress_guards FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON privacy_ingress_guards TO chatisp_app;

ALTER TABLE webhook_items ADD COLUMN ingress_received_at timestamptz;
-- The migration owner must backfill all tenants despite FORCE RLS. This runs
-- inside the migration transaction, which holds the table lock until commit.
ALTER TABLE webhook_items NO FORCE ROW LEVEL SECURITY;
UPDATE webhook_items w SET ingress_received_at=d.received_at FROM ingress_deliveries d WHERE d.id=w.delivery_id;
ALTER TABLE webhook_items ALTER COLUMN ingress_received_at SET NOT NULL;
ALTER TABLE webhook_items FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
CREATE FUNCTION webhook_item_ingress_received_at() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
    SELECT received_at INTO STRICT NEW.ingress_received_at FROM public.ingress_deliveries WHERE id=NEW.delivery_id;
    RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION webhook_item_ingress_received_at() FROM PUBLIC;
CREATE TRIGGER webhook_item_ingress_time BEFORE INSERT ON webhook_items
FOR EACH ROW EXECUTE FUNCTION webhook_item_ingress_received_at();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria proteção contra reprocessamento de dados excluídos';
END $$;
-- +goose StatementEnd
