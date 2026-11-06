package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestVerifyRecoveredDataKeysAuthenticatesEverySecretClass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		t.Fatal(err)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	keyMaterial := "v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	ring, err := crypto.Parse(keyMaterial, "v1")
	if err != nil {
		t.Fatal(err)
	}
	tenant, user, member, contact, registry, app, entity, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, query := range []string{
		fmt.Sprintf(`INSERT INTO tenants(id,timezone,status) VALUES ('%s','UTC','active')`, tenant),
		fmt.Sprintf(`INSERT INTO users(id,email,status) VALUES ('%s','recovery-verify@example.invalid','active')`, user),
		fmt.Sprintf(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES ('%s','%s','%s','admin','active')`, member, tenant, user),
		fmt.Sprintf(`INSERT INTO contacts(id,tenant_id) VALUES ('%s','%s')`, contact, tenant),
		fmt.Sprintf(`INSERT INTO meta_app_registry(id,app_id,status) VALUES ('%s','recovery-app','active')`, registry),
		fmt.Sprintf(`INSERT INTO meta_apps(id,tenant_id,app_id,status,registry_id) VALUES ('%s','%s','recovery-app','active','%s')`, app, tenant, registry),
	} {
		if _, err := conn.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	type fixture struct {
		table, column, query string
		aad                  crypto.Context
		cipher               []byte
	}
	fixtures := []fixture{
		{table: "users", column: "totp_ciphertext", query: `UPDATE users SET totp_ciphertext=$1,totp_key_version=$2,totp_aad_tenant='` + tenant + `' WHERE id='` + user + `'`, aad: crypto.Context{TenantID: tenant, Entity: "user", EntityID: user, Field: "totp_secret"}},
		{table: "credential_secrets", column: "ciphertext", query: `INSERT INTO credential_secrets(tenant_id,id,entity,entity_id,field,key_version,ciphertext,aad) VALUES ('` + tenant + `','` + uuid.NewString() + `','channel_connector','` + entity + `','access_token',$2,$1,$3)`, aad: crypto.Context{TenantID: tenant, Entity: "channel_connector", EntityID: entity, Field: "access_token"}},
		{table: "meta_app_registry_secrets", column: "ciphertext", query: `INSERT INTO meta_app_registry_secrets(registry_id,key_version,ciphertext,aad_scope,aad_entity,aad_entity_id,aad_field,aad) VALUES ('` + registry + `',$2,$1,'ingress_registry','meta_app_registry','` + registry + `','app_secret',$3)`, aad: crypto.Context{TenantID: "ingress_registry", Entity: "meta_app_registry", EntityID: registry, Field: "app_secret"}},
		{table: "meta_app_secrets", column: "ciphertext", query: `INSERT INTO meta_app_secrets(tenant_id,meta_app_id,key_version,ciphertext,aad) VALUES ('` + tenant + `','` + app + `',$2,$1,$3)`, aad: crypto.Context{TenantID: tenant, Entity: "meta_app", EntityID: app, Field: "app_secret"}},
		{table: "contact_documents", column: "ciphertext", query: `INSERT INTO contact_documents(tenant_id,contact_id,kind,key_version,ciphertext,document_hmac,version,updated_by_membership_id) VALUES ('` + tenant + `','` + contact + `','cpf',$2,$1,decode(repeat('ab',32),'hex'),1,'` + member + `')`, aad: crypto.Context{TenantID: tenant, Entity: "contact_document", EntityID: contact, Field: "document"}},
		{table: "contact_document_peppers", column: "ciphertext", query: `INSERT INTO contact_document_peppers(tenant_id,key_version,ciphertext) VALUES ('` + tenant + `',$2,$1)`, aad: crypto.Context{TenantID: tenant, Entity: "contact_document_pepper", EntityID: tenant, Field: "pepper"}},
		{table: "webhook_subscriptions", column: "secret_ciphertext", query: `INSERT INTO webhook_subscriptions(tenant_id,id,url,secret_ciphertext,secret_key_version,secret_aad,events) VALUES ('` + tenant + `','` + sub + `','https://receiver.example.invalid/events',$1,$2,$3,ARRAY['message.created'])`, aad: crypto.Context{TenantID: tenant, Entity: "webhook_subscription", EntityID: sub, Field: "signing_secret"}},
	}
	for i := range fixtures {
		f := &fixtures[i]
		sealed, err := ring.Encrypt([]byte("synthetic-recoverable-secret"), f.aad)
		if err != nil {
			t.Fatal(err)
		}
		f.cipher = sealed.Ciphertext
		args := []any{sealed.Ciphertext, sealed.Version}
		if strings.Contains(f.query, "$3") {
			args = append(args, strings.Join([]string{f.aad.TenantID, f.aad.Entity, f.aad.EntityID, f.aad.Field}, "\x1f"))
		}
		if _, err := conn.Exec(ctx, f.query, args...); err != nil {
			t.Fatalf("seed %s: %v", f.table, err)
		}
	}
	count, err := verifyRecoveredCiphertexts(ctx, conn, ring)
	if err != nil || count != len(fixtures) {
		t.Fatalf("correto: %d %v", count, err)
	}
	wrong, _ := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x99}, 32)), "v1")
	if count, err := verifyRecoveredCiphertexts(ctx, conn, wrong); err == nil || count != len(fixtures) || !strings.Contains(err.Error(), "7 de 7") {
		t.Fatalf("bytes errados com mesmo ID aceitos: %d %v", count, err)
	}
	missing, _ := crypto.Parse("v2:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)), "v2")
	if _, err := verifyRecoveredCiphertexts(ctx, conn, missing); err == nil {
		t.Fatal("versão ausente aceita")
	}
	for _, f := range fixtures {
		t.Run("corrupt_"+f.table, func(t *testing.T) {
			corrupted := bytes.Clone(f.cipher)
			corrupted[len(corrupted)-1] ^= 1
			query := fmt.Sprintf("UPDATE %s SET %s=$1", f.table, f.column)
			if _, err := conn.Exec(ctx, query, corrupted); err != nil {
				t.Fatal(err)
			}
			if _, err := verifyRecoveredCiphertexts(ctx, conn, ring); err == nil || !strings.Contains(err.Error(), "1 de 7") {
				t.Fatalf("corrupção ignorada em %s: %v", f.table, err)
			}
			if _, err := conn.Exec(ctx, query, f.cipher); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := conn.Exec(ctx, `UPDATE credential_secrets SET aad='forged-aad'`); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRecoveredCiphertexts(ctx, conn, ring); err == nil {
		t.Fatal("AAD divergente aceito")
	}
	f := fixtures[1]
	if _, err := conn.Exec(ctx, `UPDATE credential_secrets SET aad=$1`, strings.Join([]string{f.aad.TenantID, f.aad.Entity, f.aad.EntityID, f.aad.Field}, "\x1f")); err != nil {
		t.Fatal(err)
	}
	owner, err := pgx.Connect(ctx, urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	if _, err := verifyRecoveredCiphertexts(ctx, owner, ring); err == nil {
		t.Fatal("owner sem bypass RLS aceito como prova completa")
	}
	var out bytes.Buffer
	cliEnv := []string{"CHATISP_RECOVERY_DATABASE_URL=" + env.AdminURL, "CHATISP_DATA_KEYS=" + keyMaterial, "CHATISP_DATA_KEY_VERSION=v1"}
	if err := verifyDataKeys(ctx, nil, cliEnv, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "7 material(is)") || strings.Contains(out.String(), "synthetic-recoverable-secret") || strings.Contains(out.String(), keyMaterial) {
		t.Fatalf("saída inadequada: %q", out.String())
	}
	if err := verifyDataKeys(ctx, nil, nil, io.Discard); err == nil {
		t.Fatal("CLI sem DSN/chaves aceito")
	}
	if err := verifyDataKeys(ctx, []string{"unexpected"}, cliEnv, io.Discard); err == nil {
		t.Fatal("CLI aceitou argumentos")
	}
}
