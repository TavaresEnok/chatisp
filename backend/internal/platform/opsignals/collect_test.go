package opsignals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/platform/testdb"
)

var (
	testPools *db.Pools
	testStore *db.Store
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_ = os.Unsetenv("CHATISP_DATABASE_URL")
	_ = os.Unsetenv(testdb.EnvDatabaseURL)
	env, err := testdb.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao iniciar PostgreSQL de teste: %s\n", err)
		os.Exit(1)
	}
	secrets, err := db.NewRoleSecrets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao preparar papéis: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	urls, err := db.Bootstrap(ctx, env.AdminURL, secrets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha no bootstrap: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	dir, err := db.MigrationsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	if err := db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		fmt.Fprintf(os.Stderr, "falha nas migrações: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	pools, err := db.OpenPools(ctx, db.RoleURLs{App: urls.App, Platform: urls.Platform, Ingress: urls.Ingress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}
	testPools = pools
	testStore = db.NewStore(pools.App, pools.Platform)
	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

func TestCollectPublishesClosedAggregates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	originA := "origem-secreta-ops-a"
	originB := "origem-secreta-ops-b"
	tenantA := insertConnector(t, ctx, originA)
	_ = insertConnector(t, ctx, originB)
	if tenantA == uuid.Nil {
		t.Fatal("provedor vazio")
	}

	var hidden int
	scoped, err := testPools.Platform.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Rollback(ctx)
	if _, err := scoped.Exec(ctx, `SELECT set_config('app.access_scope', 'platform', true)`); err != nil {
		t.Fatal(err)
	}
	if err := scoped.QueryRow(ctx, `SELECT count(*) FROM channel_connectors`).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden != 2 {
		t.Fatalf("leitura de plataforma = %d", hidden)
	}

	withoutScope := testPools.Platform
	var bare int
	if err := withoutScope.QueryRow(ctx, `SELECT count(*) FROM channel_connectors`).Scan(&bare); err != nil {
		t.Fatal(err)
	}
	if bare != 0 {
		t.Fatalf("plataforma sem escopo leu %d conectores", bare)
	}

	assertColumnDenied(t, ctx, testPools.Platform, `SELECT credential_ref FROM channel_connectors`)
	assertColumnDenied(t, ctx, testPools.Platform, `SELECT remote_origin FROM channel_connectors`)
	assertColumnDenied(t, ctx, testPools.Platform, `SELECT payload FROM webhook_items`)
	assertColumnDenied(t, ctx, testPools.Platform, `SELECT error_message FROM outbound_attempts`)
	assertColumnDenied(t, ctx, testPools.Platform, `SELECT phone_number_id FROM webhook_items`)

	meter := metrics.New()
	readCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer stop()
	done := make(chan struct{})
	go func() {
		Collect(readCtx, testPools.Platform, meter)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		raw, err := meter.Gather()
		if err != nil {
			t.Fatal(err)
		}
		body = string(raw)
		if strings.Contains(body, "chatisp_ops_collect_ok 1") || strings.Contains(body, "chatisp_ops_collect_ok{} 1") {
			break
		}
		if strings.Contains(body, `chatisp_ops_collect_ok 1`) {
			break
		}
		value, ok := lineValue(body, "chatisp_ops_collect_ok")
		if ok && value == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	<-done
	if strings.Contains(body, originA) || strings.Contains(body, originB) || strings.Contains(body, tenantA.String()) {
		t.Fatalf("identificador entrou na série:\n%s", body)
	}
	connectors, ok := lineValue(body, "chatisp_connectors", `type="meta_cloud"`, `status="disconnected"`)
	if !ok || connectors != 2 {
		t.Fatalf("conectores = %v ok=%v\n%s", connectors, ok, body)
	}
	storage, ok := lineValue(body, "chatisp_storage_expected_bytes")
	if !ok || storage != 0 {
		t.Fatalf("storage = %v ok=%v\n%s", storage, ok, body)
	}
	uncertain, ok := lineValue(body, "chatisp_outbound_uncertain")
	if !ok || uncertain != 0 {
		t.Fatalf("incertos = %v ok=%v\n%s", uncertain, ok, body)
	}
	okValue, ok := lineValue(body, "chatisp_ops_collect_ok")
	if !ok || okValue != 1 {
		t.Fatalf("coleta = %v ok=%v\n%s", okValue, ok, body)
	}
}

func insertConnector(t *testing.T, ctx context.Context, origin string) uuid.UUID {
	t.Helper()
	tenantID := uuid.New()
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID: tenantID, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testPools.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var got string
	if err := tx.QueryRow(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()).Scan(&got); err != nil || got != tenantID.String() {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO channel_connectors (id, tenant_id, connector_type, remote_origin, status, enabled, channel)
		VALUES ($1, $2, 'meta_cloud', $3, 'disconnected', false, 'whatsapp')`,
		uuid.New(), tenantID, origin)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return tenantID
}

func assertColumnDenied(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.access_scope', 'platform', true)`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, sql)
	if err == nil {
		t.Fatalf("coluna restrita foi lida: %s", sql)
	}
}

func lineValue(text, name string, parts ...string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		if len(line) > len(name) && line[len(name)] != ' ' && line[len(name)] != '{' {
			continue
		}
		match := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var value float64
		if _, err := fmt.Sscan(fields[len(fields)-1], &value); err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}
