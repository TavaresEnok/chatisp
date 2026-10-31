-- +goose Up
-- Assignment event kinds carry their origin and destination in the existing
-- timeline's kind field, so existing History readers can see the full event.
ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
 kind IN ('assumed','controller_changed','closed','reopened','wait_started','wait_ended','customer_message','outbound_queued')
 OR kind ~ '^(transferred_member|transferred_sector|unassigned):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
);
CREATE TABLE assignment_capacities (
 tenant_id uuid NOT NULL, membership_id uuid NOT NULL,
 capacity integer NOT NULL DEFAULT 20 CHECK (capacity >= 1),
 PRIMARY KEY (tenant_id,membership_id),
 FOREIGN KEY (tenant_id,membership_id) REFERENCES memberships(tenant_id,id)
);

CREATE TABLE assignment_queue_intervals (
 tenant_id uuid NOT NULL, id uuid NOT NULL, conversation_id uuid NOT NULL,
 sector_id uuid NOT NULL, started_at timestamptz NOT NULL, ended_at timestamptz,
 historical_estimate boolean NOT NULL DEFAULT false,
 PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,sector_id) REFERENCES sectors(tenant_id,id),
 CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX assignment_queue_one_open ON assignment_queue_intervals(tenant_id,conversation_id) WHERE ended_at IS NULL;
CREATE INDEX assignment_queue_sector ON assignment_queue_intervals(tenant_id,sector_id,started_at);

CREATE TABLE assignment_requests (
 tenant_id uuid NOT NULL, conversation_id uuid NOT NULL, principal_id uuid NOT NULL,
 client_request_id text NOT NULL, payload jsonb NOT NULL, result jsonb NOT NULL,
 PRIMARY KEY (tenant_id,conversation_id,principal_id,client_request_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,principal_id) REFERENCES memberships(tenant_id,id)
);

ALTER TABLE conversations NO FORCE ROW LEVEL SECURITY;
INSERT INTO assignment_queue_intervals (tenant_id,id,conversation_id,sector_id,started_at,historical_estimate)
 SELECT tenant_id,gen_random_uuid(),id,sector_id,created_at,true FROM conversations
 WHERE inbox_id IS NOT NULL AND sector_id IS NOT NULL AND assignee_id IS NULL AND status IN ('open','waiting_customer');
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['assignment_capacities','assignment_queue_intervals','assignment_requests'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',tab);
  EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %I TO chatisp_app',tab);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION assignment_track_queue() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE queued boolean; previous_sector uuid; stamp timestamptz;
BEGIN
 queued := NEW.inbox_id IS NOT NULL AND NEW.sector_id IS NOT NULL AND NEW.assignee_id IS NULL AND NEW.status IN ('open','waiting_customer');
 IF TG_OP = 'UPDATE' THEN previous_sector := OLD.sector_id; END IF;
 stamp := COALESCE(NEW.updated_at,now());
 IF TG_OP = 'INSERT' THEN stamp := COALESCE(NEW.created_at,stamp); END IF;
 IF NOT queued OR (TG_OP = 'UPDATE' AND previous_sector IS DISTINCT FROM NEW.sector_id) THEN
  UPDATE public.assignment_queue_intervals SET ended_at=GREATEST(started_at,stamp) WHERE tenant_id=NEW.tenant_id AND conversation_id=NEW.id AND ended_at IS NULL;
 END IF;
 IF queued THEN
  INSERT INTO public.assignment_queue_intervals(tenant_id,id,conversation_id,sector_id,started_at)
  VALUES(NEW.tenant_id,gen_random_uuid(),NEW.id,NEW.sector_id,stamp)
  ON CONFLICT (tenant_id,conversation_id) WHERE ended_at IS NULL DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER assignment_track_queue AFTER INSERT OR UPDATE OF status,assignee_id,sector_id ON conversations FOR EACH ROW EXECUTE FUNCTION assignment_track_queue();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback de atribuição descartaria histórico operacional'; END $$;
-- +goose StatementEnd
