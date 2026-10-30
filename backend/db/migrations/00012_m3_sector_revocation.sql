-- +goose Up
-- Historical identity references durable entities, not operational authorization.
-- The existing tenant/inbox FK remains; sector deletion must preserve history too.
ALTER TABLE conversations ADD CONSTRAINT conversations_sector_fkey
 FOREIGN KEY (tenant_id,sector_id) REFERENCES sectors(tenant_id,id);
ALTER TABLE conversations DROP CONSTRAINT conversations_sector_inbox_fkey;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION conversations_assign_sector() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 -- Ordinary updates of historical conversations do not grant a new assignment.
 IF TG_OP = 'UPDATE' THEN
  IF (NEW.tenant_id,NEW.inbox_id,NEW.sector_id) IS NOT DISTINCT FROM
     (OLD.tenant_id,OLD.inbox_id,OLD.sector_id) THEN RETURN NEW; END IF;
 END IF;

 -- Same gate as policy consumers, before sector/link locks. Configuration takes
 -- it exclusively before touching these rows. No conversation lock is taken by
 -- removal, and neither the tenant gate nor RLS is bypassed by this invoker.
 PERFORM pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext(NEW.tenant_id::text));
 IF NEW.inbox_id IS NOT NULL AND NEW.sector_id IS NULL THEN
  SELECT d.sector_id INTO NEW.sector_id FROM public.inbox_sector_defaults d
   JOIN public.sectors s ON s.tenant_id=d.tenant_id AND s.id=d.sector_id AND s.active
   WHERE d.tenant_id=NEW.tenant_id AND d.inbox_id=NEW.inbox_id;
  IF NEW.sector_id IS NULL THEN RAISE EXCEPTION 'setor padrão ausente' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.inbox_id IS NULL AND NEW.sector_id IS NOT NULL THEN
  RAISE EXCEPTION 'caixa ausente' USING ERRCODE='23514';
 END IF;
 IF NEW.inbox_id IS NOT NULL THEN
  PERFORM 1 FROM public.sectors s
   WHERE s.tenant_id=NEW.tenant_id AND s.id=NEW.sector_id AND s.active FOR SHARE OF s;
  IF NOT FOUND THEN RAISE EXCEPTION 'setor inválido' USING ERRCODE='23503'; END IF;
  -- SHARE (not KEY SHARE) also synchronizes with non-key operational changes.
  -- A concurrent DELETE either waits for assignment commit, or wins and causes
  -- this locking read to find no row. Locks last until transaction completion.
  PERFORM 1 FROM public.sector_inboxes si
   WHERE si.tenant_id=NEW.tenant_id AND si.sector_id=NEW.sector_id AND si.inbox_id=NEW.inbox_id
   FOR SHARE OF si;
  IF NOT FOUND THEN RAISE EXCEPTION 'vínculo setor/caixa ausente' USING ERRCODE='23503'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER conversations_assign_sector ON conversations;
CREATE TRIGGER conversations_assign_sector BEFORE INSERT OR UPDATE OF tenant_id,inbox_id,sector_id
 ON conversations FOR EACH ROW EXECUTE FUNCTION conversations_assign_sector();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'rollback recobraria vínculos operacionais revogados ou perderia histórico';
END $$;
-- +goose StatementEnd
