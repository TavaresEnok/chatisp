-- +goose Up
-- Optional contact document. The CPF/CNPJ never sits in a plaintext column.
-- Search stores only a tenant-peppered HMAC. The recoverable value is an
-- AES-GCM ciphertext bound to tenant, contact and field. RLS is forced for the
-- table owner as well; chatisp_app is the only runtime role with a policy.

CREATE TABLE contact_document_peppers (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    key_version text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contact_document_peppers_version_chk CHECK (
        char_length(key_version) BETWEEN 1 AND 32
        AND key_version = btrim(key_version)
    ),
    CONSTRAINT contact_document_peppers_ciphertext_chk CHECK (octet_length(ciphertext) BETWEEN 28 AND 128)
);

-- A cleared document stays as a row. Kind, ciphertext and HMAC become NULL
-- and version increments, so a later set cannot recycle version 1.
CREATE TABLE contact_documents (
    tenant_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    kind text,
    key_version text,
    ciphertext bytea,
    document_hmac bytea,
    version bigint NOT NULL,
    updated_by_membership_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, contact_id),
    CONSTRAINT contact_documents_hmac_key UNIQUE (tenant_id, document_hmac),
    CONSTRAINT contact_documents_contact_fkey
        FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id),
    CONSTRAINT contact_documents_membership_fkey
        FOREIGN KEY (tenant_id, updated_by_membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT contact_documents_kind_chk CHECK (kind IN ('cpf', 'cnpj')),
    CONSTRAINT contact_documents_version_chk CHECK (
        char_length(key_version) BETWEEN 1 AND 32
        AND key_version = btrim(key_version)
    ),
    CONSTRAINT contact_documents_ciphertext_chk CHECK (octet_length(ciphertext) BETWEEN 28 AND 256),
    CONSTRAINT contact_documents_hmac_chk CHECK (octet_length(document_hmac) = 32),
    CONSTRAINT contact_documents_row_version_chk CHECK (version >= 1),
    CONSTRAINT contact_documents_presence_chk CHECK (
        (
            kind IS NOT NULL
            AND key_version IS NOT NULL
            AND ciphertext IS NOT NULL
            AND document_hmac IS NOT NULL
        )
        OR (
            kind IS NULL
            AND key_version IS NULL
            AND ciphertext IS NULL
            AND document_hmac IS NULL
        )
    )
);

CREATE TABLE contact_document_commands (
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    request_id uuid NOT NULL,
    request_hmac bytea NOT NULL,
    response jsonb NOT NULL,
    PRIMARY KEY (tenant_id, membership_id, request_id),
    CONSTRAINT contact_document_commands_membership_fkey
        FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT contact_document_commands_hmac_chk CHECK (octet_length(request_hmac) = 32),
    CONSTRAINT contact_document_commands_response_chk CHECK (
        jsonb_typeof(response) = 'object'
        AND NOT (response ? 'document')
    )
);

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
  FOREACH tab IN ARRAY ARRAY['contact_document_peppers','contact_documents','contact_document_commands'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tab);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tab);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)$p$, tab);
    EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC, chatisp_platform, chatisp_ingress', tab);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON contact_document_peppers TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON contact_documents TO chatisp_app;
GRANT SELECT, INSERT ON contact_document_commands TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'rollback descartaria documento cifrado e trilha de comandos';
END $$;
-- +goose StatementEnd
