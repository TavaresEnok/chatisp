-- +goose Up
CREATE TABLE annotation_tags (
 tenant_id uuid NOT NULL REFERENCES tenants(id), id uuid NOT NULL,
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40 AND name = btrim(name)),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,name)
);
CREATE TABLE conversation_annotations (
 tenant_id uuid NOT NULL, conversation_id uuid NOT NULL,
 priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','urgent')),
 version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
 PRIMARY KEY (tenant_id,conversation_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id)
);
CREATE TABLE conversation_annotation_tags (
 tenant_id uuid NOT NULL, conversation_id uuid NOT NULL, tag_id uuid NOT NULL,
 PRIMARY KEY (tenant_id,conversation_id,tag_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,tag_id) REFERENCES annotation_tags(tenant_id,id)
);
CREATE TABLE conversation_notes (
 tenant_id uuid NOT NULL, id uuid NOT NULL, conversation_id uuid NOT NULL,
 author_id uuid NOT NULL, request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 128),
 body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 16000), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,author_id,request_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,author_id) REFERENCES memberships(tenant_id,id)
);
CREATE TABLE conversation_annotation_events (
 tenant_id uuid NOT NULL, id uuid NOT NULL, conversation_id uuid NOT NULL,
 actor_id uuid NOT NULL, kind text NOT NULL, subject_id uuid, version bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,actor_id) REFERENCES memberships(tenant_id,id)
);
CREATE TABLE conversation_read_markers (
 tenant_id uuid NOT NULL, user_id uuid NOT NULL, conversation_id uuid NOT NULL,
 watermark bigint NOT NULL DEFAULT 0 CHECK (watermark >= 0), unread boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
 PRIMARY KEY (tenant_id,user_id,conversation_id),
 FOREIGN KEY (tenant_id,user_id) REFERENCES memberships(tenant_id,user_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id)
);
-- Preserve message cursors independently of realtime retention.
CREATE TABLE annotation_message_cursors (
 tenant_id uuid NOT NULL, conversation_id uuid NOT NULL, message_id uuid NOT NULL, seq bigint NOT NULL,
 PRIMARY KEY (tenant_id,message_id),
 FOREIGN KEY (tenant_id,conversation_id) REFERENCES conversations(tenant_id,id),
 FOREIGN KEY (tenant_id,message_id) REFERENCES messages(tenant_id,id)
);
-- The migration owner must see all tenants while copying pre-existing rows.
ALTER TABLE messages NO FORCE ROW LEVEL SECURITY;
ALTER TABLE realtime_events NO FORCE ROW LEVEL SECURITY;
INSERT INTO annotation_message_cursors
 SELECT e.tenant_id,e.aggregate_id,m.id,e.seq FROM realtime_events e
 JOIN messages m ON m.tenant_id=e.tenant_id AND m.id::text=e.payload->>'message_id'
 WHERE e.event_type='conversation.message' AND e.aggregate_type='conversation' AND m.direction='inbound' AND NOT m.historical
 ON CONFLICT DO NOTHING;
-- A pre-migration message whose realtime event has expired predates all future cursors.
INSERT INTO annotation_message_cursors
 SELECT tenant_id,conversation_id,id,0 FROM messages WHERE direction='inbound' AND NOT historical
 ON CONFLICT DO NOTHING;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
ALTER TABLE realtime_events FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
CREATE FUNCTION annotation_capture_message_cursor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.event_type='conversation.message' AND NEW.aggregate_type='conversation' THEN
  INSERT INTO annotation_message_cursors(tenant_id,conversation_id,message_id,seq)
   SELECT NEW.tenant_id,NEW.aggregate_id,m.id,NEW.seq FROM messages m
   WHERE m.tenant_id=NEW.tenant_id AND m.id::text=NEW.payload->>'message_id'
    AND m.conversation_id=NEW.aggregate_id AND m.direction='inbound' AND NOT m.historical
   ON CONFLICT DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER annotation_message_cursor AFTER INSERT ON realtime_events FOR EACH ROW EXECUTE FUNCTION annotation_capture_message_cursor();
-- +goose StatementBegin
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['annotation_tags','conversation_annotations','conversation_annotation_tags','conversation_notes','conversation_annotation_events','conversation_read_markers','annotation_message_cursors'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY tenant_scope ON %I TO chatisp_app USING (tenant_id = nullif(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'',true),'''')::uuid)',t);
  EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO chatisp_app',t);
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER annotation_message_cursor ON realtime_events;
DROP FUNCTION annotation_capture_message_cursor();
DROP TABLE annotation_message_cursors, conversation_read_markers, conversation_annotation_events, conversation_notes, conversation_annotation_tags, conversation_annotations, annotation_tags;
