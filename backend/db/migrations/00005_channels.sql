-- +goose Up
-- Tenant channel catalog: connectors, inboxes, contacts and external identities.
-- Composite keys keep every reference inside one tenant. Credential material
-- stays outside this schema; credential_ref is only an opaque pointer.
-- Evolution rows stay disabled. An active connection cannot change driver.

CREATE TABLE channel_connectors (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    connector_type text NOT NULL,
    remote_origin text NOT NULL,
    version text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending',
    enabled boolean NOT NULL DEFAULT false,
    credential_ref text,
    channel text NOT NULL DEFAULT 'whatsapp',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT channel_connectors_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT channel_connectors_type_chk CHECK (connector_type IN ('meta_cloud', 'evolution')),
    CONSTRAINT channel_connectors_channel_chk CHECK (channel = 'whatsapp'),
    CONSTRAINT channel_connectors_status_chk CHECK (status IN ('pending', 'connected', 'degraded', 'disconnected')),
    CONSTRAINT channel_connectors_origin_chk CHECK (
        char_length(remote_origin) BETWEEN 1 AND 256
        AND remote_origin = btrim(remote_origin)
    ),
    CONSTRAINT channel_connectors_version_len CHECK (char_length(version) <= 64),
    CONSTRAINT channel_connectors_credential_ref_chk CHECK (
        credential_ref IS NULL
        OR (
            char_length(credential_ref) BETWEEN 1 AND 200
            AND credential_ref = btrim(credential_ref)
            AND credential_ref !~ '[[:space:]]'
        )
    ),
    CONSTRAINT channel_connectors_evolution_disabled CHECK (
        connector_type <> 'evolution' OR enabled = false
    )
);

-- One live remote resource belongs to one tenant. Disconnecting releases it.
CREATE UNIQUE INDEX channel_connectors_active_remote_origin_key
    ON channel_connectors (remote_origin)
    WHERE status <> 'disconnected';

CREATE TABLE inboxes (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    name text NOT NULL,
    presented_number text,
    hours jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT inboxes_connector_key UNIQUE (tenant_id, connector_id),
    CONSTRAINT inboxes_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT inboxes_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT inboxes_name_chk CHECK (
        char_length(name) BETWEEN 1 AND 120
        AND btrim(name) <> ''
    ),
    CONSTRAINT inboxes_presented_number_chk CHECK (
        presented_number IS NULL OR presented_number ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT inboxes_hours_obj CHECK (jsonb_typeof(hours) = 'object')
);

CREATE INDEX inboxes_connector_idx ON inboxes (tenant_id, connector_id);

CREATE TABLE contacts (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL DEFAULT '',
    phone text,
    email text,
    notes text NOT NULL DEFAULT '',
    blocked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT contacts_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT contacts_name_len CHECK (char_length(name) <= 200),
    CONSTRAINT contacts_phone_chk CHECK (
        phone IS NULL OR phone ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT contacts_email_chk CHECK (
        email IS NULL
        OR (
            char_length(email) BETWEEN 3 AND 320
            AND email = lower(email)
            AND email = btrim(email)
            AND email ~ '^[^@[:space:]]+@[^@[:space:]]+$'
        )
    ),
    CONSTRAINT contacts_notes_len CHECK (char_length(notes) <= 4000)
);

CREATE INDEX contacts_phone_idx ON contacts (tenant_id, phone) WHERE phone IS NOT NULL;

CREATE TABLE contact_channel_identities (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    external_id text NOT NULL,
    evidence text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT contact_channel_identities_connection_key UNIQUE (tenant_id, connector_id, external_id),
    CONSTRAINT contact_channel_identities_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT contact_channel_identities_contact_fkey
        FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id),
    CONSTRAINT contact_channel_identities_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT contact_channel_identities_external_id_chk CHECK (
        char_length(external_id) BETWEEN 1 AND 256
        AND external_id = btrim(external_id)
    ),
    CONSTRAINT contact_channel_identities_evidence_len CHECK (char_length(evidence) <= 2000)
);

CREATE INDEX contact_channel_identities_contact_idx
    ON contact_channel_identities (tenant_id, contact_id);

ALTER TABLE channel_connectors ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_connectors FORCE ROW LEVEL SECURITY;
ALTER TABLE inboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE inboxes FORCE ROW LEVEL SECURITY;
ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
ALTER TABLE contact_channel_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_channel_identities FORCE ROW LEVEL SECURITY;

CREATE POLICY channel_connectors_app_isolation ON channel_connectors
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY inboxes_app_isolation ON inboxes
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY contacts_app_isolation ON contacts
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY contact_channel_identities_app_isolation ON contact_channel_identities
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE channel_connectors FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE inboxes FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE contacts FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE contact_channel_identities FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

GRANT SELECT, INSERT, UPDATE, DELETE ON channel_connectors TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON inboxes TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON contacts TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON contact_channel_identities TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION channel_connectors_guard_type()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW.connector_type IS DISTINCT FROM OLD.connector_type
       AND OLD.status IN ('connected', 'degraded') THEN
        RAISE EXCEPTION 'tipo de conector imutável com conexão ativa'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER channel_connectors_guard_type
    BEFORE UPDATE OF connector_type ON channel_connectors
    FOR EACH ROW
    EXECUTE FUNCTION channel_connectors_guard_type();

REVOKE ALL ON FUNCTION channel_connectors_guard_type() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION channel_connectors_guard_type() TO chatisp_app;

-- +goose Down
DROP TRIGGER IF EXISTS channel_connectors_guard_type ON channel_connectors;
DROP FUNCTION IF EXISTS channel_connectors_guard_type();
DROP TABLE IF EXISTS contact_channel_identities;
DROP TABLE IF EXISTS inboxes;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS channel_connectors;
