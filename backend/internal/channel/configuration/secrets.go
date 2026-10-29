package configuration

import (
	"crypto/subtle"
	"strings"

	"github.com/google/uuid"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/crypto"
)

func (s *Service) seal(tenantID, entityID uuid.UUID, entity, field, plaintext string) (store.Secret, error) {
	ctx := crypto.Context{
		TenantID: tenantID.String(),
		Entity:   entity,
		EntityID: entityID.String(),
		Field:    field,
	}
	sealed, err := s.ring.Encrypt([]byte(plaintext), ctx)
	if err != nil {
		return store.Secret{}, ErrInvalid
	}
	aad, err := canonical(ctx)
	if err != nil {
		return store.Secret{}, ErrInvalid
	}
	return store.Secret{KeyVersion: sealed.Version, Ciphertext: sealed.Ciphertext, AAD: aad}, nil
}

func (s *Service) open(tenantID, entityID uuid.UUID, entity, field string, secret store.Secret) (string, error) {
	ctx := crypto.Context{
		TenantID: tenantID.String(),
		Entity:   entity,
		EntityID: entityID.String(),
		Field:    field,
	}
	expect, err := canonical(ctx)
	if err != nil || subtle.ConstantTimeCompare([]byte(expect), []byte(secret.AAD)) != 1 {
		return "", ErrSecret
	}
	plain, err := s.ring.Decrypt(secret.KeyVersion, secret.Ciphertext, ctx)
	if err != nil {
		return "", ErrSecret
	}
	return string(plain), nil
}

func canonical(ctx crypto.Context) (string, error) {
	parts := []string{ctx.TenantID, ctx.Entity, ctx.EntityID, ctx.Field}
	for _, part := range parts {
		if part == "" || len(part) > 200 || strings.ContainsAny(part, "\x00\x1f") {
			return "", ErrInvalid
		}
	}
	return strings.Join(parts, "\x1f"), nil
}

func sameSecret(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
