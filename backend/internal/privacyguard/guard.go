// Package privacyguard prevents replay of ingress accepted before a subject erasure.
package privacyguard

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/platform/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func Fingerprint(ring *crypto.Ring, version string, tenant, connector uuid.UUID, identity string) ([]byte, error) {
	if ring == nil {
		return nil, errors.New("privacy guard key unavailable")
	}
	return ring.Fingerprint(version, []byte(identity), crypto.Context{TenantID: tenant.String(), Entity: "privacy_ingress_guard", EntityID: connector.String(), Field: "external_id"})
}

// LegacyBlocked quarantines old ingress when an earlier erasure predates
// identity guards. The original payload is retained for reconciliation:
// without the erased identity we cannot safely identify its owner.
func LegacyBlocked(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, received time.Time) (bool, error) {
	var blocked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM contacts c
		WHERE c.tenant_id=$1 AND c.privacy_legacy_cutoff >= $2
		AND (EXISTS (
			SELECT 1 FROM retention_deletion_records d
			WHERE d.tenant_id=c.tenant_id AND d.target_id=c.id
			AND d.class='content' AND d.target_type='contact' AND NOT d.ingress_guarded
		) OR NOT EXISTS (
			SELECT 1 FROM retention_deletion_records d
			WHERE d.tenant_id=c.tenant_id AND d.target_id=c.id
			AND d.class='content' AND d.target_type='contact' AND d.ingress_guarded
		))
	)`, tenant, received).Scan(&blocked)
	return blocked, err
}

// Blocked fails closed when a recorded key version cannot be opened.
func Blocked(ctx context.Context, tx pgx.Tx, ring *crypto.Ring, tenant, connector uuid.UUID, identity string, received time.Time) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT key_version FROM privacy_ingress_guards WHERE tenant_id=$1 AND connector_id=$2 AND cutoff >= $3`, tenant, connector, received)
	if err != nil {
		return false, err
	}
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return false, err
		}
		versions = append(versions, version)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, version := range versions {
		actual, err := Fingerprint(ring, version, tenant, connector, identity)
		if err != nil {
			return false, err
		}
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM privacy_ingress_guards WHERE tenant_id=$1 AND connector_id=$2 AND key_version=$3 AND identity_mac=$4 AND cutoff >= $5)`, tenant, connector, version, actual, received).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}
