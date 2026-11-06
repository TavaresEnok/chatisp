package configuration

import (
	"bytes"
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
	// Bootstrap changes cluster-wide role passwords; never use an external cluster.
	t.Setenv(testdb.EnvDatabaseURL, "")
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	if !env.Embedded {
		t.Fatal("migration 8 requires its own disposable cluster")
	}
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
	conn, err := sql.Open("pgx", urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	assertMigration08Version(t, ctx, provider, 8)
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
	a := seedActor(t, "admin", false, "full")
	input := manual("A", "rollback-proof", "rollback-waba-a", "rollback-phone-a", testToken, testSecret)
	if _, err = svc.Configure(ctx, a.hash, input); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 7); err == nil || !strings.Contains(err.Error(), "app binding lacks compatible legacy representation") {
		t.Fatalf("single new binding rollback: %v", err)
	}
	assertMigration08Version(t, ctx, provider, 8)
	app, err := svc.Server().ResolveApp(ctx, input.AppID)
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("binding after failed rollback: %v", err)
	}
	status, err := svc.Status(ctx, a.hash)
	if err != nil || len(status.Boxes) != 1 || status.Boxes[0].AppSecretKeyVersion == "" {
		t.Fatalf("status after rollback: %+v %v", status, err)
	}
	b := seedActor(t, "admin", false, "full")
	input.WABAID, input.PhoneNumberID = "rollback-waba-b", "rollback-phone-b"
	if _, err = svc.Configure(ctx, b.hash, input); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 7); err == nil || !strings.Contains(err.Error(), "shared app bindings would be lost") {
		t.Fatalf("shared binding rollback: %v", err)
	}
	assertMigration08Version(t, ctx, provider, 8)
	app, err = svc.Server().ResolveApp(ctx, input.AppID)
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("shared app after failed rollback: %+v %v", app, err)
	}
	for _, actor := range []seeded{a, b} {
		status, err := svc.Status(ctx, actor.hash)
		if err != nil || len(status.Boxes) != 1 || status.Boxes[0].AppSecretKeyVersion == "" {
			t.Fatalf("shared binding after failed rollback: %+v %v", status, err)
		}
		box := status.Boxes[0]
		material, err := svc.Server().ResolveMaterial(ctx, actor.tenant, box.ConnectorID, box.Version)
		if err != nil || material.AppSecret != testSecret || material.AccessToken != testToken {
			t.Fatalf("shared material after failed rollback: %+v %v", material, err)
		}
	}
}

func assertMigration08Version(t *testing.T, ctx context.Context, provider *goose.Provider, want int64) {
	t.Helper()
	got, err := provider.GetDBVersion(ctx)
	if err != nil || got != want {
		t.Fatalf("migration version: got %d, want %d: %v", got, want, err)
	}
}

func TestSharedAppRegistry(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, testRing(t))
	a, b := seedActor(t, "admin", false, "full"), seedActor(t, "admin", false, "full")
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
	a, b := seedActor(t, "admin", false, "full"), seedActor(t, "admin", false, "full")
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
	t.Setenv(testdb.EnvDatabaseURL, "")
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	if !env.Embedded {
		t.Fatal("migration 8 requires its own disposable cluster")
	}
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
	conn, err := sql.Open("pgx", urls.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, os.DirFS(dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	assertMigration08Version(t, ctx, provider, 7)
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
	a := seedActor(t, "admin", false, "full")
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
	if _, err = provider.UpTo(ctx, 8); err == nil || !strings.Contains(err.Error(), "legacy app conflicts with canonical registry") {
		t.Fatalf("conflicting upgrade: %v", err)
	}
	assertMigration08Version(t, ctx, provider, 7)
	err = db.WithIngressTx(ctx, pools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		var appID, status string
		if err := tx.QueryRow(ctx, `SELECT app_id, status FROM meta_app_registry WHERE id = $1`, id).Scan(&appID, &status); err != nil {
			return err
		}
		if appID != "legacy-upgrade" || status != "disabled" {
			t.Fatalf("conflicting canonical authority overwritten: %q %q", appID, status)
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM meta_app_registry_secrets WHERE registry_id = $1`, id).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("failed upgrade inserted a canonical secret")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithIngressTx(ctx, pools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM meta_app_registry WHERE id = $1 AND status = 'disabled'`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	assertMigration08Version(t, ctx, provider, 8)
	assertLegacySecret := func() {
		t.Helper()
		err := db.WithIngressTx(ctx, pools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
			var appID, status, version, scope, entity, field, aad string
			var tenantID, entityID uuid.UUID
			var ciphertext []byte
			err := tx.QueryRow(ctx, `SELECT r.app_id,r.status,s.key_version,s.ciphertext,s.aad_scope,s.aad_tenant_id,s.aad_entity,s.aad_entity_id,s.aad_field,s.aad FROM meta_app_registry r JOIN meta_app_registry_secrets s ON s.registry_id=r.id WHERE r.id=$1`, id).Scan(&appID, &status, &version, &ciphertext, &scope, &tenantID, &entity, &entityID, &field, &aad)
			if err != nil {
				return err
			}
			if appID != "legacy-upgrade" || status != "active" || version != secret.KeyVersion || !bytes.Equal(ciphertext, secret.Ciphertext) || scope != "legacy_tenant" || tenantID != a.tenant || entity != entityApp || entityID != id || field != fieldAppSecret || aad != secret.AAD {
				t.Fatal("legacy canonical identity, status, ciphertext or AAD changed")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertLegacySecret()
	// A compatible legacy binding can still be rolled back and upgraded again.
	if _, err = provider.DownTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	assertMigration08Version(t, ctx, provider, 7)
	assertLegacySecret()
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	assertMigration08Version(t, ctx, provider, 8)
	assertLegacySecret()
	app, err := svc.Server().ResolveApp(ctx, "legacy-upgrade")
	if err != nil || app.AppSecret != testSecret {
		t.Fatalf("legacy app: %+v %v", app, err)
	}
	b := seedActor(t, "admin", false, "full")
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
	assertLegacySecret()
}
