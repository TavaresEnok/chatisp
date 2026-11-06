package upgradetest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestUpgradePrivacyIngressPreservesOriginalReceiptTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	full, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	baseline := t.TempDir()
	entries, err := os.ReadDir(full)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var n int
		if _, err := parseNumber(entry.Name(), &n); err != nil || n > 53 {
			continue
		}
		content, err := os.ReadFile(filepath.Join(full, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseline, entry.Name()), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, baseline); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant, connector, inbox, delivery, item, contact := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	received := time.Date(2026, 11, 1, 12, 34, 56, 0, time.UTC)
	steps := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id,timezone,status) VALUES($1,'UTC','active')`, []any{tenant}},
		{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, tenant, connector.String()}},
		{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, []any{inbox, tenant, connector}},
		{`INSERT INTO contacts(tenant_id,id,name,privacy_erased_at) VALUES($1,$2,'titular excluído',$3)`, []any{tenant, contact, received}},
		{`INSERT INTO retention_deletion_records(tenant_id,id,class,target_type,target_id,note) VALUES($1,$2,'content','contact',$3,'exclusão anterior')`, []any{tenant, uuid.New(), contact}},
		{`INSERT INTO ingress_deliveries(id,source,app_id,body_sha256,body,state,received_at) VALUES($1,'meta_cloud',$2,decode(repeat('ab',32),'hex'),$3,'routed',$4)`, []any{delivery, delivery.String(), []byte("{}"), received}},
		{`INSERT INTO webhook_items(tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload) VALUES($1,$2,$3,$4,$5,'phone','message','message','pending','{}')`, []any{tenant, item, delivery, inbox, connector}},
	}
	for _, step := range steps {
		if _, err := admin.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := sql.Open("pgx", urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(full), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	beforeUpgrade := time.Now()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	afterUpgrade := time.Now()
	var guarded bool
	if err := admin.QueryRow(ctx, `SELECT ingress_guarded FROM retention_deletion_records WHERE tenant_id=$1 AND target_id=$2`, tenant, contact).Scan(&guarded); err != nil {
		t.Fatal(err)
	}
	if guarded {
		t.Fatal("upgrade marcou exclusão histórica como protegida sem prova")
	}
	var legacyCutoff *time.Time
	if err := admin.QueryRow(ctx, `SELECT privacy_legacy_cutoff FROM contacts WHERE tenant_id=$1 AND id=$2`, tenant, contact).Scan(&legacyCutoff); err != nil {
		t.Fatal(err)
	}
	if legacyCutoff == nil || legacyCutoff.Before(beforeUpgrade.Add(-time.Second)) || legacyCutoff.After(afterUpgrade.Add(time.Second)) {
		t.Fatalf("corte legado não representa ativação: %v", legacyCutoff)
	}
	var got time.Time
	if err := admin.QueryRow(ctx, `SELECT ingress_received_at FROM webhook_items WHERE tenant_id=$1 AND id=$2`, tenant, item).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(received) {
		t.Fatalf("tempo original perdido: %v, esperado %v", got, received)
	}
	second := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO webhook_items(tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload) VALUES($1,$2,$3,$4,$5,'phone','message2','message','pending','{}')`, tenant, second, delivery, inbox, connector); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT ingress_received_at FROM webhook_items WHERE tenant_id=$1 AND id=$2`, tenant, second).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(received) {
		t.Fatalf("trigger perdeu tempo original: %v", got)
	}
}
