package configuration

import (
	"context"
	"crypto/subtle"
	"errors"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type registrySecret struct {
	ID         uuid.UUID
	Secret     store.Secret
	Scope      string
	Tenant     *uuid.UUID
	Entity     string
	EntityID   uuid.UUID
	Field      string
	Capability uuid.UUID
	Create     bool
}

func (s *Service) registry(ctx context.Context, appID string) (registrySecret, error) {
	var r registrySecret
	err := db.WithIngressTx(ctx, s.ingress, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT r.id, s.key_version, s.ciphertext, s.aad_scope, s.aad_tenant_id, s.aad_entity, s.aad_entity_id, s.aad_field, s.aad, s.binding_capability FROM meta_app_registry r JOIN meta_app_registry_secrets s ON s.registry_id = r.id WHERE r.app_id = $1 AND r.status = 'active'`, appID).Scan(&r.ID, &r.Secret.KeyVersion, &r.Secret.Ciphertext, &r.Scope, &r.Tenant, &r.Entity, &r.EntityID, &r.Field, &r.Secret.AAD, &r.Capability)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return registrySecret{}, ErrNotFound
	}
	return r, err
}

func (s *Service) openRegistry(r registrySecret) (string, error) {
	var domain string
	switch r.Scope {
	case "legacy_tenant":
		if r.Tenant == nil || *r.Tenant == uuid.Nil || r.Entity != entityApp {
			return "", ErrSecret
		}
		domain = r.Tenant.String()
	case "ingress_registry":
		if r.Tenant != nil || r.Entity != "meta_app_registry" {
			return "", ErrSecret
		}
		domain = "ingress_registry"
	default:
		return "", ErrSecret
	}
	if r.ID == uuid.Nil || r.EntityID != r.ID || r.Field != fieldAppSecret {
		return "", ErrSecret
	}
	c := crypto.Context{TenantID: domain, Entity: r.Entity, EntityID: r.ID.String(), Field: r.Field}
	aad, err := canonical(c)
	if err != nil || subtle.ConstantTimeCompare([]byte(aad), []byte(r.Secret.AAD)) != 1 {
		return "", ErrSecret
	}
	plain, err := s.ring.Decrypt(r.Secret.KeyVersion, r.Secret.Ciphertext, c)
	if err != nil {
		return "", ErrSecret
	}
	return string(plain), nil
}

func (s *Service) prepareRegistry(ctx context.Context, appID, plaintext string) (registrySecret, error) {
	r, err := s.registry(ctx, appID)
	if errors.Is(err, ErrNotFound) {
		r = registrySecret{ID: uuid.New(), Scope: "ingress_registry", Entity: "meta_app_registry", Field: fieldAppSecret, Create: true}
		r.EntityID = r.ID
		c := crypto.Context{TenantID: "ingress_registry", Entity: r.Entity, EntityID: r.ID.String(), Field: r.Field}
		sealed, err := s.ring.Encrypt([]byte(plaintext), c)
		if err != nil {
			return registrySecret{}, ErrSecret
		}
		aad, err := canonical(c)
		if err != nil {
			return registrySecret{}, err
		}
		r.Secret = store.Secret{KeyVersion: sealed.Version, Ciphertext: sealed.Ciphertext, AAD: aad}
		return r, nil
	}
	if err != nil {
		return registrySecret{}, err
	}
	opened, err := s.openRegistry(r)
	if err != nil {
		return registrySecret{}, err
	}
	if !sameSecret(opened, plaintext) {
		return registrySecret{}, ErrConflict
	}
	return r, nil
}

func bindRegistry(ctx context.Context, tx pgx.Tx, appID string, r registrySecret) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT bind_meta_app($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, appID, uuid.New(), r.ID, r.Secret.KeyVersion, r.Secret.Ciphertext, r.Scope, r.Tenant, r.Entity, r.EntityID, r.Field, r.Secret.AAD, r.Create, r.Capability).Scan(&id)
	return id, err
}
