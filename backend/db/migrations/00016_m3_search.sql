-- +goose Up
-- Extension installation requires CREATE on the database, which the minimal
-- migration role may not have. Use an immutable Latin fallback in that case.
-- +goose StatementBegin
DO $$ BEGIN
 IF has_database_privilege(current_user,current_database(),'CREATE') THEN
  CREATE EXTENSION IF NOT EXISTS unaccent;
  CREATE EXTENSION IF NOT EXISTS pg_trgm;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_extension WHERE extname='unaccent' AND extnamespace='public'::regnamespace) THEN
  EXECUTE $fn$CREATE FUNCTION m3_search_normalize(text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE RETURN lower(public.unaccent('public.unaccent'::regdictionary,coalesce($1,'')))$fn$;
 ELSE
  EXECUTE $fn$CREATE FUNCTION m3_search_normalize(text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE RETURN translate(lower(coalesce($1,'')),'áàâãäéèêëíìîïóòôõöúùûüçñ','aaaaaeeeeiiiiooooouuuucn')$fn$;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_trgm' AND extnamespace='public'::regnamespace) THEN
  CREATE INDEX m3_contact_name_trgm ON contacts USING gin (m3_search_normalize(name) gin_trgm_ops);
  CREATE INDEX m3_contact_phone_trgm ON contacts USING gin (coalesce(phone,'') gin_trgm_ops);
  CREATE INDEX m3_conversation_protocol_trgm ON conversations USING gin (coalesce(protocol,'') gin_trgm_ops);
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX m3_message_search ON messages USING gin (to_tsvector('portuguese',m3_search_normalize(body))) WHERE visibility='public';
CREATE INDEX m3_queue_scope ON conversations (tenant_id,inbox_id,sector_id,status,id);
CREATE INDEX m3_queue_message_cursor ON annotation_message_cursors (tenant_id,conversation_id,seq);
-- +goose Down
DROP INDEX m3_queue_message_cursor, m3_queue_scope, m3_message_search;
DROP INDEX IF EXISTS m3_conversation_protocol_trgm, m3_contact_phone_trgm, m3_contact_name_trgm;
DROP FUNCTION m3_search_normalize(text);
