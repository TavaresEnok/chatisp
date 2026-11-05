-- +goose Up
-- Number health for 02-saude-numero and 02-limites-numero.
-- 'limit' rows come from the phone_number_quality_update webhook, which has
-- only the WABA and the display number. Ingress routes it to exactly one
-- active number of the same app and WABA; anything else is quarantined.
-- 'quality' rows come from an explicit admin read of quality_rating.
-- Values are stored as Meta sends them. No tier is translated to a count.

CREATE TABLE meta_number_health (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    phone_id uuid NOT NULL,
    kind text NOT NULL,
    dedup_key text NOT NULL,
    event text,
    current_limit text,
    old_limit text,
    max_daily bigint,
    quality_rating text,
    simulation boolean NOT NULL DEFAULT false,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT meta_number_health_dedup_key UNIQUE (tenant_id, dedup_key),
    CONSTRAINT meta_number_health_phone_fkey
        FOREIGN KEY (tenant_id, phone_id) REFERENCES meta_phone_numbers (tenant_id, id),
    CONSTRAINT meta_number_health_kind_chk CHECK (kind IN ('limit', 'quality')),
    CONSTRAINT meta_number_health_shape_chk CHECK (
        char_length(dedup_key) BETWEEN 1 AND 128
        AND (event IS NULL OR event ~ '^[A-Z0-9_]{1,64}$')
        AND (current_limit IS NULL OR current_limit ~ '^[A-Z0-9_]{1,64}$')
        AND (old_limit IS NULL OR old_limit ~ '^[A-Z0-9_]{1,64}$')
        AND (quality_rating IS NULL OR quality_rating ~ '^[A-Z0-9_]{1,64}$')
        AND (max_daily IS NULL OR max_daily BETWEEN 0 AND 1000000000)
        AND (
            (kind = 'limit' AND event IS NOT NULL AND quality_rating IS NULL
                AND (current_limit IS NOT NULL OR max_daily IS NOT NULL))
            OR (kind = 'quality' AND quality_rating IS NOT NULL AND event IS NULL
                AND current_limit IS NULL AND old_limit IS NULL AND max_daily IS NULL)
        )
    )
);

CREATE INDEX meta_number_health_latest_idx
    ON meta_number_health (tenant_id, phone_id, kind, received_at DESC);

ALTER TABLE meta_number_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_number_health FORCE ROW LEVEL SECURITY;

CREATE POLICY meta_number_health_app_isolation ON meta_number_health
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Ingress inserts limit rows only, and only for a number of the same tenant.
CREATE POLICY meta_number_health_ingress_insert ON meta_number_health
    FOR INSERT
    TO chatisp_ingress
    WITH CHECK (
        current_setting('app.access_scope', true) = 'ingress'
        AND kind = 'limit'
        AND EXISTS (
            SELECT 1
            FROM meta_phone_numbers AS phone
            WHERE phone.tenant_id = meta_number_health.tenant_id
              AND phone.id = meta_number_health.phone_id
        )
    );

CREATE POLICY meta_number_health_ingress_key ON meta_number_health
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

GRANT SELECT, INSERT ON meta_number_health TO chatisp_app;
GRANT SELECT (tenant_id, id, dedup_key) ON meta_number_health TO chatisp_ingress;
GRANT INSERT ON meta_number_health TO chatisp_ingress;

-- A limit change whose display number matches no number, or more than one,
-- of the app and WABA stays global and inspectable. It is not guessed.
ALTER TABLE ingress_quarantine DROP CONSTRAINT ingress_quarantine_reason_chk;
ALTER TABLE ingress_quarantine ADD CONSTRAINT ingress_quarantine_reason_chk CHECK (
    reason IN ('unknown_phone', 'incompatible_binding', 'unknown_display_number', 'ambiguous_display_number')
    AND char_length(remote_ref) <= 256
);

-- +goose Down
ALTER TABLE ingress_quarantine DROP CONSTRAINT ingress_quarantine_reason_chk;
UPDATE ingress_quarantine SET reason = 'unknown_phone'
    WHERE reason IN ('unknown_display_number', 'ambiguous_display_number');
ALTER TABLE ingress_quarantine ADD CONSTRAINT ingress_quarantine_reason_chk CHECK (
    reason IN ('unknown_phone', 'incompatible_binding')
    AND char_length(remote_ref) <= 256
);

DROP TABLE IF EXISTS meta_number_health;
