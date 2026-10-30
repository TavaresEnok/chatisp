package configuration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigration08RejectsUnsafeRollback(t *testing.T) {
	ctx := context.Background()
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
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	oldStore := testStore
	testStore = db.NewStore(pools.App, pools.Platform)
	defer func() { testStore = oldStore }()
	svc, err := New(testStore, pools.Ingress, testRing(t))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	a := seedActor(t, "admin", true, "full")
	input := manual("A", "rollback-proof", "rollback-waba-a", "rollback-phone-a", testToken, testSecret)
	if _, err = svc.Configure(ctx, a.hash, input); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "lacks compatible legacy representation") {
		t.Fatalf("single new binding rollback: %v", err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	app, err := svc.Server().ResolveApp(ctx, input.AppID)
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("binding after failed rollback: %v", err)
	}
	status, err := svc.Status(ctx, a.hash)
	if err != nil || len(status.Boxes) != 1 || status.Boxes[0].AppSecretKeyVersion == "" {
		t.Fatalf("status after rollback: %+v %v", status, err)
	}
	b := seedActor(t, "admin", true, "full")
	input.WABAID, input.PhoneNumberID = "rollback-waba-b", "rollback-phone-b"
	if _, err = svc.Configure(ctx, b.hash, input); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "shared app bindings would be lost") {
		t.Fatalf("shared binding rollback: %v", err)
	}
	if _, err = svc.Server().ResolveApp(ctx, input.AppID); err != nil {
		t.Fatal(err)
	}
}

func TestSharedAppRegistry(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, testRing(t))
	a, b := seedActor(t, "admin", true, "full"), seedActor(t, "admin", true, "full")
	first := manual("A", "app-shared-proof", "waba-shared-proof-a", "phone-shared-proof-a", testToken, testSecret)
	second := manual("B", first.AppID, "waba-shared-proof-b", "phone-shared-proof-b", "token-teste-local-bbbb", testSecret)
	boxA, err := svc.Configure(ctx, a.hash, first)
	if err != nil {
		t.Fatal(err)
	}
	boxB, err := svc.Configure(ctx, b.hash, second)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := svc.Server().ResolveApp(ctx, first.AppID)
	if err != nil || trusted.AppSecret != testSecret {
		t.Fatalf("app: %v", err)
	}
	for _, item := range []struct {
		phone  string
		box    Box
		tenant seeded
	}{{first.PhoneNumberID, boxA, a}, {second.PhoneNumberID, boxB, b}} {
		route, err := svc.Server().ResolveRoute(ctx, first.AppID, item.phone)
		if err != nil || route.TenantID != item.tenant.tenant || route.InboxID != item.box.InboxID {
			t.Fatalf("route: %+v %v", route, err)
		}
	}
	for _, item := range []struct {
		actor seeded
		box   Box
	}{{a, boxA}, {b, boxB}} {
		status, err := svc.Status(ctx, item.actor.hash)
		if err != nil || len(status.Boxes) != 1 || status.Boxes[0].ConnectorID != item.box.ConnectorID || status.Boxes[0].AppSecretKeyVersion != boxA.AppSecretKeyVersion {
			t.Fatalf("status: %+v %v", status, err)
		}
	}
	foreign := second
	foreign.PhoneNumberID = first.PhoneNumberID
	foreign.WABAID = "waba-shared-foreign"
	if _, err := svc.Configure(ctx, b.hash, foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign phone: %v", err)
	}
	newApp := manual("Rollback", "app-shared-rollback", first.WABAID, "phone-shared-rollback", testToken, testSecret)
	if _, err := svc.Configure(ctx, b.hash, newApp); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign WABA: %v", err)
	}
	if _, err := svc.Server().ResolveApp(ctx, newApp.AppID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("registry survived rollback: %v", err)
	}
	second.AppSecret = "segredo-diferente-123"
	second.PhoneNumberID = "phone-shared-conflict"
	second.WABAID = "waba-shared-conflict"
	if _, err := svc.Configure(ctx, b.hash, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("secret conflict: %v", err)
	}
	status, err := svc.Status(ctx, b.hash)
	if err != nil || len(status.Boxes) != 1 {
		t.Fatalf("rollback: %+v %v", status, err)
	}
}

// This fixture preserves the tenant AAD and ciphertext copied by migration 00007.
func TestSharedAppLegacySecret(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, testRing(t))
	a, b := seedActor(t, "admin", true, "full"), seedActor(t, "admin", true, "full")
	id := uuid.New()
	secret, err := svc.seal(a.tenant, id, entityApp, fieldAppSecret, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO meta_app_registry(id, app_id, status) VALUES ($1,$2,'active')`, id, "app-legacy-proof"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO meta_app_registry_secrets(registry_id,key_version,ciphertext,aad_scope,aad_tenant_id,aad_entity,aad_entity_id,aad_field,aad) VALUES ($1,$2,$3,'legacy_tenant',$4,'meta_app',$1,'app_secret',$5)`, id, secret.KeyVersion, secret.Ciphertext, a.tenant, secret.AAD)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	box, err := svc.Configure(ctx, b.hash, manual("Legacy", "app-legacy-proof", "waba-legacy-proof", "phone-legacy-proof", testToken, testSecret))
	if err != nil {
		t.Fatal(err)
	}
	app, err := svc.Server().ResolveApp(ctx, "app-legacy-proof")
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("legacy: %v", err)
	}
	status, err := svc.Status(ctx, b.hash)
	if err != nil || len(status.Boxes) != 1 || status.Boxes[0].AppSecretKeyVersion != secret.KeyVersion || status.Boxes[0].ConnectorID != box.ConnectorID {
		t.Fatalf("legacy status: %+v %v", status, err)
	}
}

// Exercise writes made by the old service after 00007's initial backfill.
func TestMigration08LegacyWrites(t *testing.T) {
	ctx := context.Background()
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
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	oldStore := testStore
	testStore = db.NewStore(pools.App, pools.Platform)
	defer func() { testStore = oldStore }()
	svc, err := New(testStore, pools.Ingress, testRing(t))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	a := seedActor(t, "admin", true, "full")
	id := uuid.New()
	secret, err := svc.seal(a.tenant, id, entityApp, fieldAppSecret, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenantTx(ctx, a.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO meta_apps(tenant_id,id,app_id,status) VALUES ($1,$2,'legacy-upgrade','active')`, a.tenant, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO meta_app_secrets(tenant_id,meta_app_id,key_version,ciphertext,aad) VALUES ($1,$2,$3,$4,$5)`, a.tenant, id, secret.KeyVersion, secret.Ciphertext, secret.AAD)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A conflicting canonical authority must abort the upgrade without replacement.
	err = db.WithIngressTx(ctx, pools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO meta_app_registry(id, app_id, status) VALUES ($1, 'legacy-upgrade', 'disabled')`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "legacy app conflicts") {
		t.Fatalf("conflicting upgrade: %v", err)
	}
	err = db.WithIngressTx(ctx, pools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM meta_app_registry WHERE id = $1 AND status = 'disabled'`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	app, err := svc.Server().ResolveApp(ctx, "legacy-upgrade")
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("legacy app: %+v %v", app, err)
	}
	b := seedActor(t, "admin", true, "full")
	box, err := svc.Configure(ctx, b.hash, manual("Legacy", "legacy-upgrade", "legacy-upgrade-waba", "legacy-upgrade-phone", testToken, testSecret))
	if err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(ctx, b.hash)
	if err != nil || len(status.Boxes) != 1 || status.Boxes[0].AppSecretKeyVersion != secret.KeyVersion {
		t.Fatalf("status: %+v %v", status, err)
	}
	material, err := svc.Server().ResolveMaterial(ctx, b.tenant, box.ConnectorID, box.Version)
	if err != nil || material.AppSecret != testSecret || material.AccessToken != testToken {
		t.Fatalf("material: %+v %v", material, err)
	}
}
