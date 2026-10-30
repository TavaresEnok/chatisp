-- Attendance queries. Every tenant statement also matches app.tenant_id.
-- The caller rejects a missing setting before these run. RLS is the backstop.

-- name: AllocateProtocol :one
INSERT INTO protocol_counters (tenant_id, period, last_value)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('period'), 1)
ON CONFLICT (tenant_id, period) DO UPDATE
SET last_value = protocol_counters.last_value + 1
WHERE protocol_counters.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
RETURNING last_value;

-- name: LockActiveConversation :one
SELECT id
FROM conversations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND inbox_id = sqlc.arg('inbox_id')
  AND contact_identity_id = sqlc.arg('contact_identity_id')
  AND status <> 'closed'
FOR UPDATE;

-- name: LockConversation :one
SELECT id
FROM conversations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
FOR UPDATE;

-- name: GetConversation :one
SELECT
    id, tenant_id, summary, created_at, inbox_id, contact_id, contact_identity_id,
    status, controller, controller_epoch, version, assignee_id, protocol, protocol_period,
    protocol_communicated, external_protocol, last_customer_message_at,
    last_eligible_human_reply_at, waiting_since, cycle_number, updated_at
FROM conversations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id');

-- name: InsertOperationalConversation :one
INSERT INTO conversations (
    id, tenant_id, summary, inbox_id, contact_id, contact_identity_id,
    status, controller, controller_epoch, version, protocol, protocol_period, cycle_number
) VALUES (
    sqlc.arg('id'), sqlc.arg('tenant_id'), '', sqlc.arg('inbox_id'), sqlc.arg('contact_id'),
    sqlc.arg('contact_identity_id'), sqlc.arg('status'), 'menu', 1, 1,
    sqlc.arg('protocol'), sqlc.arg('period'), 1
)
RETURNING id, version, controller_epoch, protocol, status;

-- name: InsertCycle :exec
INSERT INTO conversation_cycles (tenant_id, id, conversation_id, cycle_number, opened_at)
VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('conversation_id'),
    sqlc.arg('cycle_number'), sqlc.arg('opened_at')
);

-- name: AssumeConversation :one
UPDATE conversations
SET assignee_id = sqlc.arg('assignee_id'),
    controller = 'human',
    controller_epoch = CASE WHEN controller <> 'human' THEN controller_epoch + 1 ELSE controller_epoch END,
    status = CASE WHEN status = 'triage' THEN 'open' ELSE status END,
    version = version + 1,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('expected_version')
  AND status <> 'closed'
RETURNING id, version, controller_epoch, status, controller, assignee_id;

-- name: CloseConversation :one
UPDATE conversations
SET status = 'closed',
    controller_epoch = controller_epoch + 1,
    version = version + 1,
    waiting_since = NULL,
    summary = sqlc.arg('summary'),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('expected_version')
  AND status IN ('triage', 'open', 'waiting_customer')
RETURNING id, version, controller_epoch, status, protocol, cycle_number, last_customer_message_at, external_protocol;

-- name: CloseCycle :execrows
UPDATE conversation_cycles
SET closed_at = sqlc.arg('closed_at'),
    close_reason = sqlc.arg('reason'),
    close_result = sqlc.arg('result'),
    close_summary = sqlc.arg('summary'),
    close_author_id = sqlc.arg('author_id'),
    close_membership_id = sqlc.arg('membership_id'),
    close_author_kind = sqlc.arg('author_kind'),
    external_reference = sqlc.narg('external_reference')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND conversation_id = sqlc.arg('conversation_id')
  AND cycle_number = sqlc.arg('cycle_number')
  AND closed_at IS NULL;

-- name: ReceiveCustomerClock :one
UPDATE conversations
SET last_customer_message_at = sqlc.arg('trusted_at'),
    status = CASE WHEN status = 'waiting_customer' THEN 'open' ELSE status END,
    waiting_since = CASE WHEN status = 'waiting_customer' THEN NULL ELSE waiting_since END,
    version = version + 1,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('expected_version')
  AND status <> 'closed'
  AND (last_customer_message_at IS NULL OR sqlc.arg('trusted_at') > last_customer_message_at)
RETURNING id, version, status;

-- name: MarkWaitingCustomer :one
UPDATE conversations
SET status = 'waiting_customer',
    waiting_since = sqlc.arg('accepted_at'),
    last_eligible_human_reply_at = CASE
        WHEN last_eligible_human_reply_at IS NULL OR sqlc.arg('accepted_at') > last_eligible_human_reply_at
        THEN sqlc.arg('accepted_at') ELSE last_eligible_human_reply_at END,
    version = version + 1,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('expected_version')
  AND status IN ('open', 'waiting_customer')
  AND (status = 'open' OR waiting_since IS NULL OR sqlc.arg('accepted_at') > waiting_since)
  AND (last_customer_message_at IS NULL OR sqlc.arg('accepted_at') >= last_customer_message_at)
RETURNING id, version, status, waiting_since;

-- name: AttachExternalProtocol :one
UPDATE conversations
SET external_protocol = sqlc.arg('external_protocol'),
    version = version + 1,
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND version = sqlc.arg('expected_version')
  AND external_protocol IS NULL
RETURNING id, version, external_protocol;

-- name: InsertTimeline :exec
INSERT INTO conversation_events (
    tenant_id, id, conversation_id, cycle_number, kind, actor_id, at,
    version, controller_epoch, controller, assignee_id
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('conversation_id'), sqlc.arg('cycle_number'),
    sqlc.arg('kind'), sqlc.narg('actor_id'), sqlc.arg('at'), sqlc.arg('version'),
    sqlc.arg('controller_epoch'), sqlc.arg('controller'), sqlc.narg('assignee_id')
);

-- name: ListTimeline :many
SELECT id, conversation_id, cycle_number, kind, actor_id, at, version, controller_epoch, controller, assignee_id
FROM conversation_events
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND conversation_id = sqlc.arg('conversation_id')
ORDER BY at, id;

-- name: GetMembershipUser :one
SELECT user_id
FROM memberships
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND status = 'active';

-- name: InsertIdempotency :one
INSERT INTO idempotency_records (
    tenant_id, id, principal_id, operation, client_request_id, request_hash
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('principal_id'),
    sqlc.arg('operation'), sqlc.arg('client_request_id'), sqlc.arg('request_hash')
)
ON CONFLICT (tenant_id, principal_id, operation, client_request_id) DO NOTHING
RETURNING id, request_hash;

-- name: GetIdempotency :one
SELECT id, request_hash
FROM idempotency_records
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND principal_id = sqlc.arg('principal_id')
  AND operation = sqlc.arg('operation')
  AND client_request_id = sqlc.arg('client_request_id');

-- name: InsertMessage :one
INSERT INTO messages (
    tenant_id, id, conversation_id, connector_id, idempotency_id, external_message_id,
    direction, origin, visibility, body, local_state, controller, controller_epoch,
    client_request_id, author_membership_id, trusted_at, historical
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('conversation_id'), sqlc.arg('connector_id'),
    sqlc.narg('idempotency_id'), sqlc.narg('external_message_id'), sqlc.arg('direction'),
    sqlc.arg('origin'), sqlc.arg('visibility'), sqlc.arg('body'), sqlc.narg('local_state'),
    sqlc.narg('controller'), sqlc.narg('controller_epoch'), sqlc.narg('client_request_id'),
    sqlc.narg('author_membership_id'), sqlc.narg('trusted_at'), sqlc.arg('historical')
)
RETURNING id, local_state, ingested_at;

-- name: GetMessageByExternal :one
SELECT id, conversation_id, local_state, body
FROM messages
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND connector_id = sqlc.arg('connector_id')
  AND external_message_id = sqlc.arg('external_message_id');

-- name: GetMessageByIdempotency :one
SELECT id, conversation_id, local_state
FROM messages
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND idempotency_id = sqlc.arg('idempotency_id');

-- name: ListMessages :many
SELECT id, conversation_id, external_message_id, direction, origin, visibility, body,
       local_state, trusted_at, ingested_at, historical
FROM messages
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND conversation_id = sqlc.arg('conversation_id')
ORDER BY ingested_at, id;

-- name: ListInboxConversations :many
SELECT id, protocol, status, version, controller, controller_epoch, assignee_id,
       contact_id, cycle_number, summary, last_customer_message_at, waiting_since, created_at
FROM conversations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND inbox_id = sqlc.arg('inbox_id')
ORDER BY created_at, id;

-- name: InsertOutbound :one
INSERT INTO outbound_attempts (
    tenant_id, id, message_id, conversation_id, idempotency_id, attempt_no, state,
    controller, controller_epoch, origin, previous_attempt_id
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('message_id'), sqlc.arg('conversation_id'),
    sqlc.narg('idempotency_id'), sqlc.arg('attempt_no'), 'queued',
    sqlc.arg('controller'), sqlc.arg('controller_epoch'), sqlc.arg('origin'),
    sqlc.narg('previous_attempt_id')
)
RETURNING id, state, attempt_no;

-- name: GetOutboundByIdempotency :one
SELECT id, message_id, state, attempt_no
FROM outbound_attempts
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND idempotency_id = sqlc.arg('idempotency_id');

-- name: PeekOutbound :one
SELECT id, state, message_id, conversation_id, controller_epoch, attempt_no
FROM outbound_attempts
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id');

-- name: LockMessage :one
SELECT id
FROM messages
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
FOR UPDATE;

-- name: LockOutbound :one
SELECT id, state, message_id, conversation_id, controller_epoch, attempt_no
FROM outbound_attempts
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
FOR UPDATE;

-- name: UpdateOutboundState :one
UPDATE outbound_attempts
SET state = sqlc.arg('state'),
    dispatched_at = COALESCE(sqlc.narg('dispatched_at'), dispatched_at),
    driver_ref = COALESCE(sqlc.narg('driver_ref'), driver_ref),
    error_code = COALESCE(sqlc.narg('error_code'), error_code),
    error_message = COALESCE(sqlc.narg('error_message'), error_message),
    lease_version = COALESCE(sqlc.narg('lease_version'), lease_version),
    updated_at = now()
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
RETURNING id, state;

-- name: UpdateMessageState :execrows
UPDATE messages
SET local_state = sqlc.arg('local_state'),
    external_message_id = COALESCE(sqlc.narg('external_message_id'), external_message_id)
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id');

-- name: LockQueuedAutomaticMessages :many
SELECT message.id
FROM messages AS message
WHERE message.tenant_id = sqlc.arg('tenant_id')
  AND message.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND message.id IN (
      SELECT attempt.message_id
      FROM outbound_attempts AS attempt
      WHERE attempt.tenant_id = message.tenant_id
        AND attempt.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        AND attempt.conversation_id = sqlc.arg('conversation_id')
        AND attempt.state = 'queued'
        AND attempt.controller_epoch = sqlc.arg('controller_epoch')
        AND attempt.origin IN ('bot', 'menu')
  )
ORDER BY message.id
FOR UPDATE;

-- name: CancelQueuedAutomatic :many
WITH cancelled AS (
    UPDATE outbound_attempts AS attempt
    SET state = 'cancelled', updated_at = now()
    WHERE attempt.tenant_id = sqlc.arg('tenant_id')
      AND attempt.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
      AND attempt.id IN (
          SELECT queued.id
          FROM outbound_attempts AS queued
          WHERE queued.tenant_id = sqlc.arg('tenant_id')
            AND queued.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
            AND queued.conversation_id = sqlc.arg('conversation_id')
            AND queued.state = 'queued'
            AND queued.controller_epoch = sqlc.arg('controller_epoch')
            AND queued.origin IN ('bot', 'menu')
          ORDER BY queued.id
          FOR UPDATE
      )
    RETURNING attempt.id, attempt.message_id
)
SELECT id, message_id
FROM cancelled
ORDER BY id;

-- name: InsertStatusEvent :one
INSERT INTO message_status_events (
    tenant_id, id, connector_id, message_id, external_message_id, dedup_key, kind,
    trusted_at, billing_category, billable, pending, detail
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('connector_id'), sqlc.narg('message_id'),
    sqlc.narg('external_message_id'), sqlc.arg('dedup_key'), sqlc.arg('kind'),
    sqlc.narg('trusted_at'), sqlc.narg('billing_category'), sqlc.arg('billable'),
    sqlc.arg('pending'), sqlc.arg('detail')
)
ON CONFLICT (tenant_id, connector_id, dedup_key) DO NOTHING
RETURNING id, message_id, pending;

-- name: GetStatusByDedup :one
SELECT id, message_id, kind, pending, billing_category, billable
FROM message_status_events
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND connector_id = sqlc.arg('connector_id')
  AND dedup_key = sqlc.arg('dedup_key');

-- name: LinkPendingStatus :execrows
UPDATE message_status_events
SET message_id = sqlc.arg('message_id'), pending = false
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND connector_id = sqlc.arg('connector_id')
  AND external_message_id = sqlc.arg('external_message_id')
  AND message_id IS NULL;

-- name: EnsureRealtimeCursor :exec
INSERT INTO realtime_tenant_cursors (tenant_id, seq, retain_after)
VALUES (sqlc.arg('tenant_id'), 0, 0)
ON CONFLICT (tenant_id) DO NOTHING;

-- name: LockRealtimeCursor :one
SELECT seq, retain_after
FROM realtime_tenant_cursors
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
FOR SHARE;

-- name: AdvanceRealtimeCursor :one
UPDATE realtime_tenant_cursors
SET seq = seq + 1
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
RETURNING seq;

-- name: InsertRealtimeEvent :exec
INSERT INTO realtime_events (
    tenant_id, seq, id, event_type, aggregate_type, aggregate_id, version, payload
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('seq'), sqlc.arg('id'), sqlc.arg('event_type'),
    sqlc.arg('aggregate_type'), sqlc.arg('aggregate_id'), sqlc.arg('version'), sqlc.arg('payload')
);

-- name: ListRealtimeEvents :many
SELECT seq, id, event_type, aggregate_type, aggregate_id, version, payload
FROM realtime_events
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND seq > sqlc.arg('after_seq')
  AND seq <= sqlc.arg('through_seq')
ORDER BY seq;

-- name: CompactRealtime :one
UPDATE realtime_tenant_cursors
SET retain_after = sqlc.arg('retain_after')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND sqlc.arg('retain_after') >= retain_after
  AND sqlc.arg('retain_after') <= seq
RETURNING retain_after;

-- name: DeleteExpiredRealtime :exec
DELETE FROM realtime_events
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND seq <= sqlc.arg('retain_after');

-- name: EnsureJobSlot :exec
INSERT INTO tenant_job_slots (tenant_id, queue_name, max_slots)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('queue_name'), sqlc.arg('max_slots'))
ON CONFLICT (tenant_id, queue_name) DO NOTHING;

-- name: LockJobSlot :one
SELECT max_slots
FROM tenant_job_slots
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND queue_name = sqlc.arg('queue_name')
FOR UPDATE;

-- name: DeleteExpiredJobReservations :exec
DELETE FROM tenant_job_reservations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND queue_name = sqlc.arg('queue_name')
  AND expires_at <= now();

-- name: CountLiveJobReservations :one
SELECT count(*)::bigint AS live
FROM tenant_job_reservations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND queue_name = sqlc.arg('queue_name')
  AND expires_at > now();

-- name: InsertJobReservation :one
INSERT INTO tenant_job_reservations (tenant_id, id, queue_name, owner_id, expires_at)
VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('queue_name'),
    sqlc.arg('owner_id'), sqlc.arg('expires_at')
)
RETURNING id, expires_at;

-- name: DeleteJobReservation :execrows
DELETE FROM tenant_job_reservations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND queue_name = sqlc.arg('queue_name')
  AND owner_id = sqlc.arg('owner_id');

-- name: AcquireSendLease :one
INSERT INTO inbox_send_leases (tenant_id, inbox_id, owner_id, lease_version, expires_at)
VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('inbox_id'), sqlc.arg('owner_id'), 1, sqlc.arg('expires_at')
)
ON CONFLICT (tenant_id, inbox_id) DO UPDATE
SET owner_id = EXCLUDED.owner_id,
    lease_version = inbox_send_leases.lease_version + 1,
    expires_at = EXCLUDED.expires_at
WHERE inbox_send_leases.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND (
      inbox_send_leases.owner_id = EXCLUDED.owner_id
      OR inbox_send_leases.expires_at <= now()
  )
RETURNING owner_id, lease_version, expires_at;

-- name: RenewSendLease :one
UPDATE inbox_send_leases
SET expires_at = sqlc.arg('expires_at')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND inbox_id = sqlc.arg('inbox_id')
  AND owner_id = sqlc.arg('owner_id')
  AND lease_version = sqlc.arg('lease_version')
  AND expires_at > now()
RETURNING lease_version, expires_at;

-- name: LockSendLease :one
SELECT lease_version
FROM inbox_send_leases
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND inbox_id = sqlc.arg('inbox_id')
  AND owner_id = sqlc.arg('owner_id')
  AND lease_version = sqlc.arg('lease_version')
  AND expires_at > now()
FOR UPDATE;

-- name: InsertSendReservation :one
INSERT INTO send_reservations (
    tenant_id, id, inbox_id, attempt_id, owner_id, lease_version, expires_at, state
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('inbox_id'), sqlc.arg('attempt_id'),
    sqlc.arg('owner_id'), sqlc.arg('lease_version'), sqlc.arg('expires_at'), 'held'
)
RETURNING id;

-- name: FinishSendReservation :one
UPDATE send_reservations
SET state = sqlc.arg('state')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND owner_id = sqlc.arg('owner_id')
  AND state = 'held'
RETURNING id;

-- name: GetSendReservationState :one
SELECT state
FROM send_reservations
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id')
  AND owner_id = sqlc.arg('owner_id');

-- name: GetInboxConnector :one
SELECT connector_id
FROM inboxes
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND id = sqlc.arg('id');

-- name: InsertCredential :one
INSERT INTO credential_secrets (
    tenant_id, id, entity, entity_id, field, key_version, ciphertext, aad
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), 'channel_connector', sqlc.arg('entity_id'),
    sqlc.arg('field'), sqlc.arg('key_version'), sqlc.arg('ciphertext'), sqlc.arg('aad')
)
RETURNING id, key_version, aad;

-- name: GetCredential :one
SELECT key_version, ciphertext, aad
FROM credential_secrets
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND entity = 'channel_connector'
  AND entity_id = sqlc.arg('entity_id')
  AND field = sqlc.arg('field');

-- name: InsertMetaWaba :exec
INSERT INTO meta_wabas (tenant_id, id, meta_app_id, waba_id, status)
VALUES (sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('meta_app_id'), sqlc.arg('waba_id'), 'active');

-- name: InsertMetaPhone :exec
INSERT INTO meta_phone_numbers (
    tenant_id, id, meta_waba_id, phone_number_id, display_number, status, connector_id, inbox_id
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('meta_waba_id'), sqlc.arg('phone_number_id'),
    sqlc.narg('display_number'), 'active', sqlc.arg('connector_id'), sqlc.arg('inbox_id')
);

-- name: ResolveMetaPhone :one
SELECT phone.tenant_id, phone.inbox_id, phone.connector_id, phone.phone_number_id
FROM meta_phone_numbers AS phone
JOIN meta_wabas AS waba
  ON waba.tenant_id = phone.tenant_id AND waba.id = phone.meta_waba_id
JOIN meta_apps AS app
  ON app.tenant_id = waba.tenant_id AND app.id = waba.meta_app_id
WHERE app.app_id = sqlc.arg('app_id')
  AND phone.phone_number_id = sqlc.arg('phone_number_id')
  AND phone.status = 'active'
  AND waba.status = 'active'
  AND app.status = 'active'
  AND current_setting('app.access_scope', true) = 'ingress';

-- name: ReadMetaAppSecret :one
SELECT secret.key_version, secret.ciphertext, secret.aad
FROM meta_app_registry_secrets AS secret
JOIN meta_app_registry AS app ON app.id = secret.registry_id
WHERE app.app_id = sqlc.arg('app_id')
  AND current_setting('app.access_scope', true) = 'ingress';

-- name: InsertIngressDelivery :one
INSERT INTO ingress_deliveries (id, source, app_id, body_sha256, body, state)
VALUES (sqlc.arg('id'), 'meta_cloud', sqlc.arg('app_id'), sqlc.arg('body_sha256'), sqlc.arg('body'), sqlc.arg('state'))
ON CONFLICT (source, app_id, body_sha256) DO NOTHING
RETURNING id;

-- name: GetIngressDeliveryByHash :one
SELECT id, state
FROM ingress_deliveries
WHERE source = 'meta_cloud'
  AND app_id = sqlc.arg('app_id')
  AND body_sha256 = sqlc.arg('body_sha256');

-- name: MarkIngressDelivery :exec
UPDATE ingress_deliveries
SET state = sqlc.arg('state')
WHERE id = sqlc.arg('id');

-- name: InsertQuarantine :exec
INSERT INTO ingress_quarantine (id, delivery_id, reason, remote_ref)
VALUES (sqlc.arg('id'), sqlc.arg('delivery_id'), sqlc.arg('reason'), sqlc.arg('remote_ref'));

-- name: InsertWebhookItem :one
INSERT INTO webhook_items (
    tenant_id, id, delivery_id, inbox_id, connector_id, phone_number_id,
    external_key, kind, state, payload
) VALUES (
    sqlc.arg('tenant_id'), sqlc.arg('id'), sqlc.arg('delivery_id'), sqlc.arg('inbox_id'),
    sqlc.arg('connector_id'), sqlc.arg('phone_number_id'), sqlc.arg('external_key'),
    sqlc.arg('kind'), 'pending', sqlc.arg('payload')
)
ON CONFLICT (delivery_id, external_key) DO NOTHING
RETURNING id;

-- name: GetWebhookItem :one
SELECT id
FROM webhook_items
WHERE delivery_id = sqlc.arg('delivery_id')
  AND external_key = sqlc.arg('external_key')
  AND current_setting('app.access_scope', true) = 'ingress';

-- name: ListWebhookItems :many
SELECT id, delivery_id, inbox_id, external_key, kind, state
FROM webhook_items
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND inbox_id = sqlc.arg('inbox_id')
ORDER BY created_at, id;

-- name: GetCycleClosure :one
SELECT cycle_number, close_reason, close_result, close_summary, close_author_id, close_author_kind, closed_at, external_reference
FROM conversation_cycles
WHERE tenant_id = sqlc.arg('tenant_id')
  AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
  AND conversation_id = sqlc.arg('conversation_id')
  AND cycle_number = sqlc.arg('cycle_number');
