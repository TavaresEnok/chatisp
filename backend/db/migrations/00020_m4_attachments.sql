-- +goose Up
CREATE TABLE attachment_budgets (
 tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
 namespace uuid NOT NULL UNIQUE,
 reserved_bytes bigint NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0)
);
CREATE TABLE attachments (
 tenant_id uuid NOT NULL, id uuid NOT NULL, conversation_id uuid NOT NULL,
 request_id uuid NOT NULL, direction text NOT NULL CHECK (direction IN ('incoming','outgoing')),
 name text NOT NULL, expected_bytes bigint NOT NULL CHECK (expected_bytes > 0),
 object_key text NOT NULL UNIQUE, object_version text NOT NULL DEFAULT '',
 state text NOT NULL DEFAULT 'upload' CHECK (state IN ('upload','quarantine','validated','failed')),
 media_type text NOT NULL DEFAULT '', sha256 text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,conversation_id,request_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id)
);
CREATE INDEX attachment_cleanup ON attachments(tenant_id,expires_at) WHERE state <> 'validated';
ALTER TABLE attachment_budgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachment_budgets FORCE ROW LEVEL SECURITY;
ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY attachment_budget_tenant ON attachment_budgets TO chatisp_app
 USING (tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid);
CREATE POLICY attachment_tenant ON attachments TO chatisp_app
 USING (tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE,DELETE ON attachments,attachment_budgets TO chatisp_app;

-- +goose Down
DROP TABLE attachments;
DROP TABLE attachment_budgets;
