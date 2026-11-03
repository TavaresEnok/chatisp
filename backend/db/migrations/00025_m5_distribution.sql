-- +goose Up
-- Round-robin por setor. A escolha e a reserva da vaga ficam na transação do
-- pacote distribution; esta migração não atribui conversa sozinha.
-- O histórico automático usa o mesmo formato de transferred_member, com o
-- prefixo distributed, gravado por attendance/store.ApplyAssignment.
ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
    kind IN (
        'assumed', 'controller_changed', 'closed', 'reopened',
        'wait_started', 'wait_ended', 'customer_message', 'outbound_queued'
    )
    OR kind ~ '^(transferred_member|transferred_sector|unassigned|distributed):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
);

CREATE TABLE distribution_cursors (
    tenant_id uuid NOT NULL,
    sector_id uuid NOT NULL,
    last_membership_id uuid,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (tenant_id, sector_id),
    FOREIGN KEY (tenant_id, sector_id) REFERENCES sectors (tenant_id, id),
    FOREIGN KEY (tenant_id, last_membership_id) REFERENCES memberships (tenant_id, id)
);

CREATE TABLE distribution_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    wait_alert_seconds integer NOT NULL CHECK (wait_alert_seconds BETWEEN 0 AND 604800),
    version bigint NOT NULL CHECK (version > 0)
);

-- Atribuição automática é ligada por setor. Sem linha, o setor permanece ligado.
CREATE TABLE distribution_sector_auto (
    tenant_id uuid NOT NULL,
    sector_id uuid NOT NULL,
    enabled boolean NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    PRIMARY KEY (tenant_id, sector_id),
    FOREIGN KEY (tenant_id, sector_id) REFERENCES sectors (tenant_id, id)
);

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
  FOREACH tab IN ARRAY ARRAY['distribution_cursors','distribution_settings','distribution_sector_auto'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tab);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tab);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)$p$, tab);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON distribution_cursors TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON distribution_settings TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON distribution_sector_auto TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria o cursor de round-robin e o limiar de espera';
END $$;
-- +goose StatementEnd
