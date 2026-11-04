-- +goose Up
-- Timeline kind for the automatic release of conversations held by an
-- attendant who stayed offline beyond the tenant tolerance.
ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
    kind IN (
        'assumed', 'controller_changed', 'closed', 'reopened',
        'wait_started', 'wait_ended', 'customer_message', 'outbound_queued',
        'snoozed', 'snooze_cleared'
    )
    OR kind ~ '^(transferred_member|transferred_sector|unassigned|distributed|snooze_return|offline_unassigned):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback invalidaria eventos de desatribuição por ausência'; END $$;
-- +goose StatementEnd
