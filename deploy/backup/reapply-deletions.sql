-- Reaplica as exclusões depois de uma restauração, ANTES de reabrir o serviço.
-- Executar como superusuário (psql -v ON_ERROR_STOP=1 -f). Entradas:
--   - a tabela deletion_records da base restaurada (cobre o que estava no backup ou no WAL);
--   - o livro-razão externo exportado depois do backup, carregado em ledger_external
--     (chatisp-restore.sh o carrega de ledgers/ do destino do backup).
-- Deduplica por identidade completa (tenant_id, subject, subject_id), preserva o isolamento por
-- tenant e é idempotente. Não apaga o backup: a cópia imutável não é reescrita.
BEGIN;
-- Guard timestamps are the original ingress cutoffs, not the recovery time.
-- A missing connector fails its foreign key and keeps the service closed.
INSERT INTO privacy_ingress_guards (tenant_id, connector_id, key_version, identity_mac, cutoff)
 SELECT tenant_id, connector_id, key_version, identity_mac, max(cutoff)
 FROM guards_external GROUP BY tenant_id, connector_id, key_version, identity_mac
 ON CONFLICT (tenant_id, connector_id, key_version, identity_mac) DO UPDATE
 SET cutoff = GREATEST(privacy_ingress_guards.cutoff, EXCLUDED.cutoff);
CREATE TEMP TABLE ledger (
  tenant_id uuid NOT NULL,
  subject text NOT NULL,
  subject_id uuid NOT NULL,
  PRIMARY KEY (tenant_id, subject, subject_id)
) ON COMMIT DROP;
INSERT INTO ledger (tenant_id, subject, subject_id)
  SELECT tenant_id, subject, subject_id FROM deletion_records
  ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING;
INSERT INTO ledger (tenant_id, subject, subject_id)
  SELECT tenant_id, CASE WHEN subject = 'contact_privacy_guarded' THEN 'contact_privacy' ELSE subject END, subject_id FROM ledger_external
  ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING;
INSERT INTO ledger (tenant_id, subject, subject_id)
  SELECT tenant_id, 'contact_privacy', target_id FROM retention_deletion_records
   WHERE class = 'content' AND target_type = 'contact' AND target_id IS NOT NULL
  ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING;

-- A subject erasure after the database backup must also remove identifiers and bodies.
-- Keep the same redactions as retention.Service.DeletePrivacy; preserve cycles/protocols.
CREATE TEMP TABLE erased_contacts ON COMMIT DROP AS
  SELECT tenant_id, subject_id AS id FROM ledger WHERE subject = 'contact_privacy';
INSERT INTO ledger (tenant_id, subject, subject_id)
  SELECT a.tenant_id, 'attachment', a.id FROM attachments a
    JOIN conversations c ON c.tenant_id = a.tenant_id AND c.id = a.conversation_id
    JOIN erased_contacts e ON e.tenant_id = c.tenant_id AND e.id = c.contact_id
  ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING;

-- Anexos: linhas dependentes, as próprias linhas e o orçamento do provedor.
CREATE TEMP TABLE excluded (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  expected_bytes bigint NOT NULL,
  object_key text NOT NULL,
  object_version text NOT NULL,
  PRIMARY KEY (tenant_id, id)
) ON COMMIT DROP;
INSERT INTO excluded (tenant_id, id, expected_bytes, object_key, object_version)
  SELECT DISTINCT a.tenant_id, a.id, a.expected_bytes, a.object_key, a.object_version
    FROM attachments a
    JOIN ledger l ON l.tenant_id = a.tenant_id AND l.subject = 'attachment' AND l.subject_id = a.id;
DELETE FROM media_items m USING excluded e WHERE m.tenant_id = e.tenant_id AND (m.attachment_id = e.id OR m.output_attachment_id = e.id);
DELETE FROM attachments a USING excluded e WHERE a.tenant_id = e.tenant_id AND a.id = e.id;
UPDATE attachment_budgets b
   SET reserved_bytes = GREATEST(0, b.reserved_bytes - s.total)
  FROM (SELECT tenant_id, sum(expected_bytes) AS total FROM excluded GROUP BY tenant_id) s
 WHERE b.tenant_id = s.tenant_id;

-- Segredos de credencial.
DELETE FROM credential_secrets c USING ledger l
 WHERE l.subject = 'credential_secret' AND l.tenant_id = c.tenant_id AND l.subject_id = c.id;

UPDATE messages m SET body = '', content_context = '{}'::jsonb FROM conversations c, erased_contacts e
 WHERE m.tenant_id = c.tenant_id AND m.conversation_id = c.id
   AND c.tenant_id = e.tenant_id AND c.contact_id = e.id
   AND (m.body <> '' OR m.content_context <> '{}'::jsonb);
UPDATE realtime_events r
 SET payload = jsonb_build_object('conversation_id', c.id, 'privacy_erased', true, 'historical', true)
 FROM conversations c, erased_contacts e
 WHERE r.tenant_id = c.tenant_id AND r.aggregate_type = 'conversation' AND r.aggregate_id = c.id
   AND c.tenant_id = e.tenant_id AND c.contact_id = e.id
   AND r.payload IS DISTINCT FROM jsonb_build_object('conversation_id', c.id, 'privacy_erased', true, 'historical', true);
UPDATE conversation_notes n SET body = 'conteúdo removido a pedido do titular'
 FROM conversations c, erased_contacts e
 WHERE n.tenant_id = c.tenant_id AND n.conversation_id = c.id
   AND c.tenant_id = e.tenant_id AND c.contact_id = e.id
   AND n.body <> 'conteúdo removido a pedido do titular';
UPDATE message_reactions r SET actor_external_id = '', updated_at = now()
 FROM messages m JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id,
      erased_contacts e
 WHERE r.tenant_id = m.tenant_id AND r.target_message_id = m.id
   AND c.tenant_id = e.tenant_id AND c.contact_id = e.id AND r.actor_external_id <> '';
UPDATE contact_channel_identities i SET external_id = 'excluido-' || replace(i.id::text, '-', '')
 FROM erased_contacts e WHERE i.tenant_id = e.tenant_id AND i.contact_id = e.id
   AND i.external_id NOT LIKE 'excluido-%';
UPDATE erp_links l SET external_customer_id = 'excluido', external_contract_id = NULL
 FROM erased_contacts e WHERE l.tenant_id = e.tenant_id AND l.contact_id = e.id
   AND (l.external_customer_id <> 'excluido' OR l.external_contract_id IS NOT NULL);
UPDATE erp_actions a SET external_ref = NULL FROM conversations c, erased_contacts e
 WHERE a.tenant_id = c.tenant_id AND a.conversation_id = c.id
   AND c.tenant_id = e.tenant_id AND c.contact_id = e.id AND a.external_ref IS NOT NULL;
UPDATE contact_documents d
 SET kind = NULL, key_version = NULL, ciphertext = NULL, document_hmac = NULL,
     version = d.version + 1, updated_at = now()
 FROM erased_contacts e WHERE d.tenant_id = e.tenant_id AND d.contact_id = e.id AND d.kind IS NOT NULL;
UPDATE contacts c
 SET name = 'titular excluído', phone = NULL, email = NULL, notes = '', external_ref = NULL,
     privacy_erased_at = COALESCE(c.privacy_erased_at, now()), updated_at = now()
 FROM erased_contacts e WHERE c.tenant_id = e.tenant_id AND c.id = e.id
   AND (c.privacy_erased_at IS NULL OR c.name <> 'titular excluído' OR c.phone IS NOT NULL
        OR c.email IS NOT NULL OR c.notes <> '' OR c.external_ref IS NOT NULL);
INSERT INTO retention_deletion_records (tenant_id, id, class, target_type, target_id, note, ingress_guarded)
 SELECT e.tenant_id, gen_random_uuid(), 'content', 'contact', e.id,
        'identificadores e corpos removidos; exclusão reaplicada após restauração', true
 FROM erased_contacts e WHERE EXISTS (
   SELECT 1 FROM ledger_external x WHERE x.tenant_id = e.tenant_id
     AND x.subject = 'contact_privacy_guarded' AND x.subject_id = e.id)
 AND NOT EXISTS (
   SELECT 1 FROM retention_deletion_records r WHERE r.tenant_id = e.tenant_id
     AND r.class = 'content' AND r.target_type = 'contact' AND r.target_id = e.id AND r.ingress_guarded);

-- O livro-razão externo passa a fazer parte da base (o registro mínimo sobrevive à restauração).
INSERT INTO deletion_records (id, tenant_id, subject, subject_id)
  SELECT gen_random_uuid(), tenant_id, subject, subject_id FROM ledger
   WHERE subject IN ('attachment', 'credential_secret')
  ON CONFLICT (tenant_id, subject, subject_id) DO NOTHING;
UPDATE deletion_records d
 SET object_key = e.object_key, object_version = e.object_version, object_bytes = e.expected_bytes
 FROM excluded e WHERE d.tenant_id = e.tenant_id AND d.subject = 'attachment' AND d.subject_id = e.id
   AND d.object_key IS NULL;
SELECT (SELECT count(*) FROM excluded) AS anexos_removidos,
       (SELECT count(*) FROM ledger) AS registros_no_livro;
COMMIT;
