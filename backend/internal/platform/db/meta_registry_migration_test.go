package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMetaRegistryMigrationBackfillAndRollback(t *testing.T) {
	ctx := context.Background()
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", testURLs.Owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := provider.Up(ctx); e != nil {
			t.Errorf("restauração da migração: %v", e)
		}
	})
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant, app := uuid.New(), uuid.New()
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	c := crypto.Context{TenantID: tenant.String(), Entity: "meta_app", EntityID: app.String(), Field: "app_secret"}
	sealed, err := ring.Encrypt([]byte("fixture-secret"), c)
	if err != nil {
		t.Fatal(err)
	}
	aad := strings.Join([]string{c.TenantID, c.Entity, c.EntityID, c.Field}, "\x1f")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id, timezone, status) VALUES ($1,'UTC','active')`, []any{tenant}},
		{`INSERT INTO meta_apps(tenant_id,id,app_id,status) VALUES ($1,$2,'migration-app','active')`, []any{tenant, app}},
		{`INSERT INTO meta_app_secrets(tenant_id,meta_app_id,key_version,ciphertext,aad) VALUES ($1,$2,$3,$4,$5)`, []any{tenant, app, sealed.Version, sealed.Ciphertext, aad}},
	} {
		if _, err = admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	ingress, err := pgx.Connect(ctx, testURLs.Ingress)
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close(ctx)
	if _, err = ingress.Exec(ctx, `SELECT set_config('app.access_scope','ingress',false)`); err != nil {
		t.Fatal(err)
	}
	var id, tenantGot, entityID uuid.UUID
	var appID, status, version, scope, entity, field, aadGot string
	var ciphertext []byte
	err = ingress.QueryRow(ctx, `SELECT r.id,r.app_id,r.status,s.key_version,s.ciphertext,s.aad_scope,s.aad_tenant_id,s.aad_entity,s.aad_entity_id,s.aad_field,s.aad FROM meta_app_registry r JOIN meta_app_registry_secrets s ON s.registry_id=r.id WHERE r.id=$1`, app).Scan(&id, &appID, &status, &version, &ciphertext, &scope, &tenantGot, &entity, &entityID, &field, &aadGot)
	if err != nil {
		t.Fatal(err)
	}
	if id != app || appID != "migration-app" || status != "active" || version != sealed.Version || !bytes.Equal(ciphertext, sealed.Ciphertext) || scope != "legacy_tenant" || tenantGot != tenant || entityID != app || entity != c.Entity || field != c.Field || aadGot != aad {
		t.Fatal("cópia ou contexto diferente")
	}
	plain, err := ring.Decrypt(version, ciphertext, crypto.Context{TenantID: tenantGot.String(), Entity: entity, EntityID: entityID.String(), Field: field})
	if err != nil || string(plain) != "fixture-secret" {
		t.Fatal("ciphertext legado não abre")
	}
	var exists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_app_secrets s JOIN meta_apps a ON (a.tenant_id,a.id)=(s.tenant_id,s.meta_app_id) WHERE a.id=$1) AND EXISTS (SELECT 1 FROM pg_constraint WHERE conname='meta_app_secrets_app_fkey') AND EXISTS (SELECT 1 FROM pg_constraint WHERE conname='meta_apps_app_id_key')`, app).Scan(&exists); err != nil || !exists {
		t.Fatal("estrutura legada não preservada", err)
	}
	for _, url := range []string{testURLs.App, testURLs.Platform} {
		role, e := pgx.Connect(ctx, url)
		if e != nil {
			t.Fatal(e)
		}
		_, e = role.Exec(ctx, `SELECT ciphertext FROM meta_app_registry_secrets`)
		role.Close(ctx)
		if e == nil {
			t.Fatal("papel indevido leu segredo global")
		}
	}
	if _, err = ingress.Exec(ctx, `UPDATE meta_app_registry SET status='disabled' WHERE id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("reversão deveria rejeitar perda")
	}
	if _, err = ingress.Exec(ctx, `UPDATE meta_app_registry SET status='active' WHERE id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
}
