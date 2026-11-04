-- +goose Up
-- Durable binding of automatic output to its source, without impersonating a user.
CREATE TABLE automation_dispatches (
 tenant_id uuid NOT NULL,
 id uuid NOT NULL,
 conversation_id uuid NOT NULL,
 source_id uuid NOT NULL,
 action_index integer NOT NULL,
 reason text NOT NULL DEFAULT '',
 kind text NOT NULL CHECK (kind IN ('triage','inactivity')),
 generation bigint NOT NULL DEFAULT 0,
 attempt_id uuid,
 PRIMARY KEY (tenant_id,id),
 UNIQUE (tenant_id,conversation_id,kind,source_id,action_index),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,attempt_id) REFERENCES outbound_attempts(tenant_id,id)
);
ALTER TABLE automation_dispatches ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_dispatches FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON automation_dispatches TO chatisp_app
 USING (tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT, INSERT, UPDATE ON automation_dispatches TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback descartaria vínculos de automação'; END $$;
-- +goose StatementEnd
