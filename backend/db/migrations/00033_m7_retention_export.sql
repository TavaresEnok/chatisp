-- +goose Up
-- Política de retenção por classe e trilha de recusa do expurgo.
-- O expurgo automático permanece desligado enquanto D2-06 estiver aberta:
-- a coluna não aceita true e o job não apaga conteúdo, mídia, envelope, log ou job.
-- Prazos nulos significam "ainda não definidos". Não há prazo legal inventado aqui.
-- Limites de números e de armazenamento, quando gravados em tenants.limits,
-- bloqueiam inclusão nova e não apagam o que já existe.

CREATE TABLE tenant_retention_profiles (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    display_name text,
    content_days integer,
    media_days integer,
    envelope_days integer,
    log_days integer,
    job_days integer,
    automatic_purge boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_retention_name_chk CHECK (
        display_name IS NULL
        OR (display_name = btrim(display_name) AND char_length(display_name) BETWEEN 1 AND 80)
    ),
    CONSTRAINT tenant_retention_days_chk CHECK (
        (content_days IS NULL OR content_days BETWEEN 1 AND 36500)
        AND (media_days IS NULL OR media_days BETWEEN 1 AND 36500)
        AND (envelope_days IS NULL OR envelope_days BETWEEN 1 AND 36500)
        AND (log_days IS NULL OR log_days BETWEEN 1 AND 36500)
        AND (job_days IS NULL OR job_days BETWEEN 1 AND 36500)
    ),
    CONSTRAINT tenant_retention_purge_off_chk CHECK (automatic_purge = false)
);

CREATE TABLE retention_tasks (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    class text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT retention_tasks_class_chk CHECK (class IN ('content', 'media', 'envelope', 'log', 'job')),
    CONSTRAINT retention_tasks_status_chk CHECK (status = 'refused'),
    CONSTRAINT retention_tasks_reason_chk CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500)
);

CREATE INDEX retention_tasks_created_idx ON retention_tasks (tenant_id, created_at);

-- Nome distinto de deletion_records (00040): o ensaio de restore reaplica
-- anexo e segredo nessa tabela. Esta trilha é a do provedor, por classe.
CREATE TABLE retention_deletion_records (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    class text NOT NULL,
    target_type text NOT NULL,
    target_id uuid,
    note text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT retention_deletion_records_class_chk CHECK (class IN ('content', 'media', 'envelope', 'log', 'job')),
    CONSTRAINT retention_deletion_records_target_chk CHECK (
        char_length(btrim(target_type)) BETWEEN 1 AND 64
        AND target_type = btrim(target_type)
    ),
    CONSTRAINT retention_deletion_records_note_chk CHECK (char_length(btrim(note)) BETWEEN 1 AND 500)
);

ALTER TABLE tenant_retention_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_retention_profiles FORCE ROW LEVEL SECURITY;
ALTER TABLE retention_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE retention_tasks FORCE ROW LEVEL SECURITY;
ALTER TABLE retention_deletion_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE retention_deletion_records FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON tenant_retention_profiles
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY tenant_isolation ON retention_tasks
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY tenant_isolation ON retention_deletion_records
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON tenant_retention_profiles TO chatisp_app;
GRANT SELECT, INSERT ON retention_tasks TO chatisp_app;
GRANT SELECT, INSERT ON retention_deletion_records TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION plan_guard_new_inbox()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    cap bigint;
    current bigint;
    raw text;
BEGIN
    SELECT CASE
        WHEN t.limits ? 'max_numbers' AND jsonb_typeof(t.limits -> 'max_numbers') = 'number'
            THEN t.limits ->> 'max_numbers'
        ELSE NULL
    END
      INTO raw
      FROM public.tenants AS t
     WHERE t.id = NEW.tenant_id;
    IF raw IS NULL THEN
        RETURN NEW;
    END IF;
    IF raw !~ '^[0-9]+$' THEN
        RAISE EXCEPTION 'limites do plano inválidos'
            USING ERRCODE = '23514';
    END IF;
    cap := raw::bigint;
    SELECT count(*) INTO current FROM public.inboxes WHERE tenant_id = NEW.tenant_id;
    IF current >= cap THEN
        RAISE EXCEPTION 'limite de números do plano atingido'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER plan_guard_new_inbox
    BEFORE INSERT ON inboxes
    FOR EACH ROW
    EXECUTE FUNCTION plan_guard_new_inbox();

-- +goose StatementBegin
CREATE FUNCTION plan_guard_new_attachment()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    plan_raw text;
    reserve_raw text;
    plan_bytes bigint;
    reserve_bytes bigint := 0;
    used bigint;
    ceiling bigint;
BEGIN
    SELECT
        CASE
            WHEN t.limits ? 'max_storage_bytes' AND jsonb_typeof(t.limits -> 'max_storage_bytes') = 'number'
                THEN t.limits ->> 'max_storage_bytes'
            ELSE NULL
        END,
        CASE
            WHEN t.limits ? 'attachment_reserve_bytes' AND jsonb_typeof(t.limits -> 'attachment_reserve_bytes') = 'number'
                THEN t.limits ->> 'attachment_reserve_bytes'
            ELSE NULL
        END
      INTO plan_raw, reserve_raw
      FROM public.tenants AS t
     WHERE t.id = NEW.tenant_id;
    IF plan_raw IS NULL THEN
        RETURN NEW;
    END IF;
    IF plan_raw !~ '^[0-9]+$' THEN
        RAISE EXCEPTION 'limites do plano inválidos'
            USING ERRCODE = '23514';
    END IF;
    plan_bytes := plan_raw::bigint;
    IF reserve_raw IS NOT NULL THEN
        IF reserve_raw !~ '^[0-9]+$' THEN
            RAISE EXCEPTION 'limites do plano inválidos'
                USING ERRCODE = '23514';
        END IF;
        reserve_bytes := reserve_raw::bigint;
    END IF;
    SELECT COALESCE(sum(expected_bytes), 0)
      INTO used
      FROM public.attachments
     WHERE tenant_id = NEW.tenant_id
       AND state <> 'failed';
    ceiling := plan_bytes;
    IF NEW.direction = 'incoming' THEN
        ceiling := plan_bytes + reserve_bytes;
    END IF;
    IF used + NEW.expected_bytes > ceiling THEN
        RAISE EXCEPTION 'limite de armazenamento do plano atingido'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER plan_guard_new_attachment
    BEFORE INSERT ON attachments
    FOR EACH ROW
    EXECUTE FUNCTION plan_guard_new_attachment();

REVOKE ALL ON FUNCTION plan_guard_new_inbox() FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION plan_guard_new_attachment() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION plan_guard_new_inbox() TO chatisp_app;
GRANT EXECUTE ON FUNCTION plan_guard_new_attachment() TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria a política de retenção e a trilha de recusa do expurgo';
END $$;
-- +goose StatementEnd
