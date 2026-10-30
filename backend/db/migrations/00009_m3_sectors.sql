-- +goose Up
-- The owner temporarily removes FORCE only while backfilling existing rows.
ALTER TABLE tenants NO FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships NO FORCE ROW LEVEL SECURITY;
ALTER TABLE inboxes NO FORCE ROW LEVEL SECURITY;
ALTER TABLE conversations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE contact_channel_identities NO FORCE ROW LEVEL SECURITY;

CREATE TABLE sectors (
 tenant_id uuid NOT NULL REFERENCES tenants(id), id uuid NOT NULL,
 name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
 active boolean NOT NULL DEFAULT true, legacy_default boolean NOT NULL DEFAULT false,
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,name)
);
CREATE UNIQUE INDEX sectors_one_legacy_default ON sectors(tenant_id) WHERE legacy_default;
CREATE TABLE sector_memberships (
 tenant_id uuid NOT NULL, sector_id uuid NOT NULL, membership_id uuid NOT NULL,
 PRIMARY KEY (tenant_id,sector_id,membership_id),
 FOREIGN KEY (tenant_id,sector_id) REFERENCES sectors(tenant_id,id),
 FOREIGN KEY (tenant_id,membership_id) REFERENCES memberships(tenant_id,id)
);
CREATE TABLE sector_inboxes (
 tenant_id uuid NOT NULL, sector_id uuid NOT NULL, inbox_id uuid NOT NULL,
 PRIMARY KEY (tenant_id,sector_id,inbox_id),
 FOREIGN KEY (tenant_id,sector_id) REFERENCES sectors(tenant_id,id),
 FOREIGN KEY (tenant_id,inbox_id) REFERENCES inboxes(tenant_id,id)
);
CREATE TABLE inbox_sector_defaults (
 tenant_id uuid NOT NULL, inbox_id uuid NOT NULL, sector_id uuid NOT NULL,
 PRIMARY KEY (tenant_id,inbox_id),
 FOREIGN KEY (tenant_id,sector_id,inbox_id) REFERENCES sector_inboxes(tenant_id,sector_id,inbox_id)
);
ALTER TABLE conversations ADD COLUMN sector_id uuid;
ALTER TABLE conversations ADD CONSTRAINT conversations_sector_inbox_fkey
 FOREIGN KEY (tenant_id,sector_id,inbox_id) REFERENCES sector_inboxes(tenant_id,sector_id,inbox_id);

INSERT INTO sectors (tenant_id,id,name,legacy_default) SELECT id,gen_random_uuid(),'Padrão',true FROM tenants;
INSERT INTO sector_memberships SELECT m.tenant_id,s.id,m.id FROM memberships m JOIN sectors s ON s.tenant_id=m.tenant_id AND s.legacy_default WHERE m.status='active';
INSERT INTO sector_inboxes SELECT i.tenant_id,s.id,i.id FROM inboxes i JOIN sectors s ON s.tenant_id=i.tenant_id AND s.legacy_default;
INSERT INTO inbox_sector_defaults SELECT tenant_id,inbox_id,sector_id FROM sector_inboxes;
UPDATE conversations c SET sector_id=s.id FROM sectors s WHERE s.tenant_id=c.tenant_id AND s.legacy_default AND c.inbox_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION conversations_assign_sector() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.inbox_id IS NOT NULL AND NEW.sector_id IS NULL THEN
  SELECT d.sector_id INTO NEW.sector_id FROM public.inbox_sector_defaults d
   JOIN public.sectors s ON s.tenant_id=d.tenant_id AND s.id=d.sector_id AND s.active
   WHERE d.tenant_id=NEW.tenant_id AND d.inbox_id=NEW.inbox_id;
  IF NEW.sector_id IS NULL THEN RAISE EXCEPTION 'setor padrão ausente' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.inbox_id IS NULL AND NEW.sector_id IS NOT NULL THEN RAISE EXCEPTION 'caixa ausente' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER conversations_assign_sector BEFORE INSERT OR UPDATE OF inbox_id,sector_id ON conversations FOR EACH ROW EXECUTE FUNCTION conversations_assign_sector();

ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
ALTER TABLE inboxes FORCE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;
ALTER TABLE contact_channel_identities FORCE ROW LEVEL SECURITY;

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['sectors','sector_memberships','sector_inboxes','inbox_sector_defaults'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',tab);
  EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO chatisp_app',tab);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'rollback de setores descartaria dados operacionais';
END $$;
-- +goose StatementEnd
