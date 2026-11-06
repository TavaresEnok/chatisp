package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"chatisp/internal/platform/crypto"
	"github.com/jackc/pgx/v5"
)

// Recovery runs while API/worker are closed. It deliberately requires a
// privileged recovery DSN: the runtime/migration owner is subject to FORCE
// RLS and cannot prove that every tenant's ciphertext was checked.
func verifyDataKeys(ctx context.Context, args, environ []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("uso: chatisp verify-data-keys")
	}
	values := make(map[string]string)
	for _, entry := range environ {
		if k, v, ok := strings.Cut(entry, "="); ok {
			values[k] = v
		}
	}
	if values["CHATISP_RECOVERY_DATABASE_URL"] == "" {
		return errors.New("defina CHATISP_RECOVERY_DATABASE_URL para verificação completa das chaves")
	}
	ring, err := crypto.Load(values["CHATISP_DATA_KEYS"], values["CHATISP_DATA_KEYS_FILE"], values["CHATISP_DATA_KEY_VERSION"])
	if err != nil {
		return errors.New("anel de chaves recuperado ausente ou inválido")
	}
	conn, err := pgx.Connect(ctx, values["CHATISP_RECOVERY_DATABASE_URL"])
	if err != nil {
		return errors.New("não foi possível conectar ao banco para verificar chaves")
	}
	defer conn.Close(context.Background())
	checked, err := verifyRecoveredCiphertexts(ctx, conn, ring)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verify-data-keys: %d material(is) cifrado(s) autenticado(s) com o anel recuperado\n", checked)
	return nil
}

// The expected authenticated context is derived from each row's identity,
// never blindly from its stored AAD text. Cleared documents contain no secret.
const recoveredCiphertextsSQL = `
SELECT totp_aad_tenant::text, 'user', id::text, 'totp_secret', totp_key_version, totp_ciphertext, NULL::text
  FROM users WHERE totp_ciphertext IS NOT NULL
UNION ALL
SELECT tenant_id::text, entity, entity_id::text, field, key_version, ciphertext, aad FROM credential_secrets
UNION ALL
SELECT CASE WHEN aad_scope='ingress_registry' THEN 'ingress_registry' ELSE aad_tenant_id::text END,
       aad_entity, registry_id::text, 'app_secret', key_version, ciphertext, aad FROM meta_app_registry_secrets
UNION ALL
SELECT tenant_id::text, 'meta_app', meta_app_id::text, 'app_secret', key_version, ciphertext, aad FROM meta_app_secrets
UNION ALL
SELECT tenant_id::text, 'contact_document', contact_id::text, 'document', key_version, ciphertext, NULL::text
  FROM contact_documents WHERE ciphertext IS NOT NULL
UNION ALL
SELECT tenant_id::text, 'contact_document_pepper', tenant_id::text, 'pepper', key_version, ciphertext, NULL::text FROM contact_document_peppers
UNION ALL
SELECT tenant_id::text, 'webhook_subscription', id::text, 'signing_secret', secret_key_version, secret_ciphertext, secret_aad FROM webhook_subscriptions`

func verifyRecoveredCiphertexts(ctx context.Context, conn *pgx.Conn, ring *crypto.Ring) (int, error) {
	if ring == nil {
		return 0, errors.New("anel de chaves recuperado ausente")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, errors.New("não foi possível abrir verificação consistente das chaves")
	}
	defer tx.Rollback(context.Background())
	var privileged bool
	if err := tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&privileged); err != nil || !privileged {
		return 0, errors.New("verificação integral exige papel de recuperação superuser ou BYPASSRLS")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL row_security=off`); err != nil {
		return 0, errors.New("verificação integral das chaves indisponível")
	}
	rows, err := tx.Query(ctx, recoveredCiphertextsSQL)
	if err != nil {
		return 0, errors.New("falha ao ler todo o material cifrado do banco restaurado")
	}
	defer rows.Close()
	checked, failed := 0, 0
	for rows.Next() {
		var aad crypto.Context
		var version string
		var body []byte
		var storedAAD *string
		if err := rows.Scan(&aad.TenantID, &aad.Entity, &aad.EntityID, &aad.Field, &version, &body, &storedAAD); err != nil {
			return checked, errors.New("material cifrado do banco restaurado malformado")
		}
		checked++
		expected := strings.Join([]string{aad.TenantID, aad.Entity, aad.EntityID, aad.Field}, "\x1f")
		if storedAAD != nil && *storedAAD != expected {
			failed++
			continue
		}
		plaintext, err := ring.Decrypt(version, body, aad)
		if err != nil {
			failed++
			continue
		}
		// No plaintext escapes the check. Release the temporary bytes promptly.
		clear(plaintext)
	}
	if rows.Err() != nil {
		return checked, errors.New("verificação das chaves interrompida antes de ler todo o banco")
	}
	rows.Close()
	if failed > 0 {
		return checked, fmt.Errorf("verificação das chaves recuperadas falhou: %d de %d material(is) não autenticado(s); serviço deve permanecer fechado", failed, checked)
	}
	if err := tx.Commit(ctx); err != nil {
		return checked, errors.New("verificação consistente das chaves não concluída")
	}
	return checked, nil
}
