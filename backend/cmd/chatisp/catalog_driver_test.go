package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/channelpolicy"
	"chatisp/internal/platform/config"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/sectors"
)

func TestRuntimeSyncPreservesPopulatedCatalog(t *testing.T) {
	ctx := context.Background()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
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
	environ := []string{
		"CHATISP_ENV=development",
		"CHATISP_APP_DATABASE_URL=" + urls.App,
		"CHATISP_PLATFORM_DATABASE_URL=" + urls.Platform,
		"CHATISP_INGRESS_DATABASE_URL=" + urls.Ingress,
		"CHATISP_DATA_KEYS=1:dHJvcXVlLWNoYXZlLWJhc2U2NC0zMmJ5dGVzISEhISE=",
		"CHATISP_DATA_KEY_VERSION=1",
		"CHATISP_META_VERIFY_TOKEN=token-teste",
		"CHATISP_LOG_LEVEL=error",
	}
	cfg, err := config.LoadRuntime(environ)
	if err != nil {
		t.Fatal(err)
	}
	svcs, err := setupRuntimeServices(ctx, cfg, config.ModeAPI, logging.New("error", io.Discard), environ)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svcs.Cleanup)
	if svcs.ChannelPolicy == nil || svcs.ChannelPolicy.Driver != nil {
		t.Fatal("composição instalou um driver de catálogo")
	}
	store := svcs.ChannelPolicy.DB
	tenant, user, member := uuid.New(), uuid.New(), uuid.New()
	inbox, connector, waba, app := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := store.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := store.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		_, err := store.CreateUser(ctx, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTenant(ctx, tenant, func(ctx context.Context) error {
		_, err := store.CreateMembership(ctx, db.NewMembership{ID: member, UserID: user, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled) VALUES($1,$2,'meta_cloud',$3,'connected',true)`, connector, tenant, connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, inbox, tenant, connector); err != nil {
			return err
		}
		aad := "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"
		if _, err := tx.Exec(ctx, `SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, app.String(), app, []byte{1}, aad); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, tenant, waba, app, "waba-preservada"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO wa_templates(tenant_id,waba_id,external_id,name,language,category,state,components,sync_source,synced_at) VALUES($1,$2,'tpl-preservado','preservado','pt_BR','utility','APPROVED','[]'::jsonb,'driver',clock_timestamp())`, tenant, waba)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := sectors.Principal{TenantID: tenant, UserID: user, MembershipID: member, Role: "admin"}
	if err := svcs.ChannelPolicy.SyncTemplates(ctx, admin, inbox); !errors.Is(err, channelpolicy.ErrUnsupported) {
		t.Fatalf("sincronização sem driver: %v", err)
	}
	empty := channelpolicy.Service{DB: store, Driver: channelpolicy.FakeCatalog{}}
	if err := empty.SyncTemplates(ctx, admin, inbox); !errors.Is(err, channelpolicy.ErrUnsupported) {
		t.Fatalf("catálogo falso vazio: %v", err)
	}
	conn, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var templates, generations int
	var state, source string
	if err := conn.QueryRow(ctx, `SELECT count(*), COALESCE(max(state),''), COALESCE(max(sync_source),'') FROM wa_templates WHERE tenant_id=$1 AND waba_id=$2 AND external_id='tpl-preservado'`, tenant, waba).Scan(&templates, &state, &source); err != nil {
		t.Fatal(err)
	}
	if templates != 1 || state != "APPROVED" || source != "driver" {
		t.Fatalf("catálogo alterado: n=%d state=%s source=%s", templates, state, source)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_policy_catalog_sync WHERE tenant_id=$1`, tenant).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if generations != 0 {
		t.Fatalf("sincronização recusada reservou geração: %d", generations)
	}
}
