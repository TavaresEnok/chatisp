-- name: GetDistributionSectorAuto :one
SELECT enabled, version
FROM distribution_sector_auto
WHERE tenant_id = sqlc.arg('tenant_id')
  AND sector_id = sqlc.arg('sector_id');

-- name: GetWebhookItemKindState :one
SELECT kind, state
FROM webhook_items
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('item_id');

-- name: ClaimDistributionArrival :execrows
-- One processed message item, one attempt. The marker lives on the item
-- payload so this composition does not add a table. Inbound ignores unknown
-- JSON fields and returns before reading a payload that is already processed.
UPDATE webhook_items
SET payload = payload || jsonb_build_object(
    'distribution_arrival_claim',
    to_jsonb(sqlc.arg('claimed_at')::timestamptz)
)
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('item_id')
  AND kind = 'message'
  AND state = 'processed'
  AND NOT (payload ? 'distribution_arrival_claim');
