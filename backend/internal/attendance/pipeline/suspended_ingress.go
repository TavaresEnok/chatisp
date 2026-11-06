package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/ingress/authmeta"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SuspendedIngressConfig contains installation limits, without commercial
// defaults. Disabled means suspended deliveries are refused, never unbounded.
// An envelope spanning tenants is atomic: if any held part cannot fit, the
// whole envelope is refused so HTTP 200 cannot acknowledge discarded events.
type SuspendedIngressConfig struct {
	Enabled       bool
	MaxDeliveries int64
	MaxBytes      int64
}

var errSuspendedAdmission = errors.New("retenção transitória de suspensão indisponível ou esgotada")

func SuspendedIngressFromEnv(environ []string) (SuspendedIngressConfig, error) {
	values := make(map[string]string)
	for _, entry := range environ {
		if k, v, ok := strings.Cut(entry, "="); ok {
			values[k] = v
		}
	}
	var cfg SuspendedIngressConfig
	if enabled := values["CHATISP_SUSPENDED_INGRESS_ENABLED"]; enabled != "" {
		parsed, err := strconv.ParseBool(enabled)
		if err != nil {
			return cfg, errors.New("CHATISP_SUSPENDED_INGRESS_ENABLED inválido")
		}
		cfg.Enabled = parsed
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	var err error
	cfg.MaxDeliveries, err = strconv.ParseInt(values["CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES"], 10, 64)
	if err != nil || cfg.MaxDeliveries <= 0 {
		return cfg, errors.New("defina CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES positivo")
	}
	cfg.MaxBytes, err = strconv.ParseInt(values["CHATISP_SUSPENDED_INGRESS_MAX_BYTES"], 10, 64)
	if err != nil || cfg.MaxBytes <= 0 {
		return cfg, errors.New("defina CHATISP_SUSPENDED_INGRESS_MAX_BYTES positivo")
	}
	return cfg, nil
}

// itemTenants resolves only registered active App/WABA/number bindings. The
// sender cannot choose a tenant. Display-only quality events use the same
// binding dimensions; all matching tenants are charged conservatively.
func itemTenants(ctx context.Context, tx pgx.Tx, item metacloud.Item) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT p.tenant_id
      FROM meta_phone_numbers p
      JOIN meta_wabas w ON w.tenant_id=p.tenant_id AND w.id=p.meta_waba_id
      JOIN meta_apps a ON a.tenant_id=w.tenant_id AND a.id=w.meta_app_id
      WHERE a.app_id=$1 AND w.waba_id=$2
        AND p.status='active' AND w.status='active' AND a.status='active'
        AND ((p.phone_number_id=$3 AND $3<>'') OR
             ($3='' AND $4<>'' AND regexp_replace(coalesce(p.display_number,''),'[^0-9]','','g')=$4))
      ORDER BY p.tenant_id`, item.AppID, item.WABAID, item.PhoneNumberID, item.DisplayPhone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []uuid.UUID
	for rows.Next() {
		var tenant uuid.UUID
		if err := rows.Scan(&tenant); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}

func admitSuspendedEnvelope(ctx context.Context, tx pgx.Tx, env authmeta.Envelope, cfg SuspendedIngressConfig) (bool, error) {
	digest := sha256.Sum256(env.Raw)
	var persisted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingress_deliveries WHERE app_id=$1 AND body_sha256=$2)`, env.AppID, digest[:]).Scan(&persisted); err != nil {
		return false, err
	}
	// A replay already has durable content and must not consume a second
	// retention slot, even when suspension/configuration changed meanwhile.
	if persisted {
		return true, nil
	}
	items, err := metacloud.Parse(env.AppID, env.Raw, time.Now())
	if err != nil {
		return false, err
	}
	distinct := make(map[uuid.UUID]bool)
	for _, item := range items {
		tenants, err := itemTenants(ctx, tx, item)
		if err != nil {
			return false, err
		}
		for _, tenant := range tenants {
			distinct[tenant] = true
		}
	}
	tenants := make([]uuid.UUID, 0, len(distinct))
	for tenant := range distinct {
		tenants = append(tenants, tenant)
	}
	sort.Slice(tenants, func(i, j int) bool { return tenants[i].String() < tenants[j].String() })
	held := make([]uuid.UUID, 0, len(tenants))
	denied := false
	for _, tenant := range tenants {
		// Same fence as SetTenantStatus: suspension and admission are linearized.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chatisp-tenant'),hashtext($1::text))`, tenant.String()); err != nil {
			return false, err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&status); err != nil {
			return false, err
		}
		if status != "suspended" {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suspended_ingress_holds WHERE tenant_id=$1 AND app_id=$2 AND body_sha256=$3)`, tenant, env.AppID, digest[:]).Scan(&exists); err != nil {
			return false, err
		}
		blocked := false
		if !exists {
			var count, total int64
			if err := tx.QueryRow(ctx, `SELECT count(*),coalesce(sum(body_bytes),0)::bigint FROM suspended_ingress_holds WHERE tenant_id=$1`, tenant).Scan(&count, &total); err != nil {
				return false, err
			}
			blocked = !cfg.Enabled || cfg.MaxDeliveries <= 0 || cfg.MaxBytes <= 0 || count >= cfg.MaxDeliveries || int64(len(env.Raw)) > cfg.MaxBytes || total > cfg.MaxBytes-int64(len(env.Raw))
			held = append(held, tenant)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO suspended_ingress_alerts (tenant_id,last_seen_at,rejected_count,admission_blocked) VALUES ($1,now(),$2,$3)
          ON CONFLICT (tenant_id) DO UPDATE SET last_seen_at=now(),rejected_count=suspended_ingress_alerts.rejected_count+EXCLUDED.rejected_count,admission_blocked=EXCLUDED.admission_blocked`, tenant, boolInt(blocked), blocked); err != nil {
			return false, err
		}
		denied = denied || blocked
	}
	if denied {
		return false, nil
	} // Commit alert only, no raw body or reservation.
	for _, tenant := range held {
		if _, err := tx.Exec(ctx, `INSERT INTO suspended_ingress_holds (tenant_id,app_id,body_sha256,body_bytes) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, env.AppID, digest[:], len(env.Raw)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// A hold remains a hold after reactivation. Expansion records a segregated
// inspectable item but never schedules inbound/bot/distribution for this data.
func retainSuspendedItem(ctx context.Context, tx pgx.Tx, deliveryID uuid.UUID, item metacloud.Item) (bool, error) {
	var anyHold bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suspended_ingress_holds h JOIN ingress_deliveries d ON d.app_id=h.app_id AND d.body_sha256=h.body_sha256 WHERE d.id=$1)`, deliveryID).Scan(&anyHold); err != nil {
		return false, err
	}
	if !anyHold {
		return false, nil
	}
	tenants, err := itemTenants(ctx, tx, item)
	if err != nil {
		return false, err
	}
	retained := false
	for _, tenant := range tenants {
		var held bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suspended_ingress_holds h JOIN ingress_deliveries d ON d.app_id=h.app_id AND d.body_sha256=h.body_sha256 WHERE h.tenant_id=$1 AND d.id=$2)`, tenant, deliveryID).Scan(&held); err != nil {
			return false, err
		}
		if !held {
			continue
		}
		payload, err := json.Marshal(map[string]any{"reason": "tenant_suspended", "item": item})
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO webhook_items (tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload)
          SELECT p.tenant_id,$1,$2,p.inbox_id,p.connector_id,p.phone_number_id,$3,'other','quarantined',$4
          FROM meta_phone_numbers p JOIN meta_wabas w ON w.tenant_id=p.tenant_id AND w.id=p.meta_waba_id JOIN meta_apps a ON a.tenant_id=w.tenant_id AND a.id=w.meta_app_id
          WHERE p.tenant_id=$5 AND a.app_id=$6 AND w.waba_id=$7
            AND ((p.phone_number_id=$8 AND $8<>'') OR ($8='' AND $9<>'' AND regexp_replace(coalesce(p.display_number,''),'[^0-9]','','g')=$9))
          ORDER BY p.id LIMIT 1 ON CONFLICT (delivery_id,external_key) DO NOTHING`, uuid.New(), deliveryID, stableEventKey(item.DedupKey), payload, tenant, item.AppID, item.WABAID, item.PhoneNumberID, item.DisplayPhone)
		if err != nil {
			return false, err
		}
		retained = true
	}
	return retained, nil
}
