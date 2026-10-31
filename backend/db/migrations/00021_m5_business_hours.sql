-- +goose Up
CREATE TABLE inbox_business_hours (
 tenant_id uuid NOT NULL, inbox_id uuid NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 configured boolean NOT NULL,
 config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
 PRIMARY KEY (tenant_id,inbox_id),
 FOREIGN KEY (tenant_id,inbox_id) REFERENCES inboxes(tenant_id,id)
);
CREATE TABLE business_hours_commands (
 tenant_id uuid NOT NULL, membership_id uuid NOT NULL, request_id uuid NOT NULL,
 request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
 response jsonb NOT NULL,
 PRIMARY KEY (tenant_id,membership_id,request_id),
 FOREIGN KEY (tenant_id,membership_id) REFERENCES memberships(tenant_id,id)
);
-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['inbox_business_hours','business_hours_commands'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',tab);
 END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT, INSERT, UPDATE ON inbox_business_hours TO chatisp_app;
GRANT SELECT, INSERT ON business_hours_commands TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'rollback descartaria configuração e histórico de comandos';
END $$;
-- +goose StatementEnd
