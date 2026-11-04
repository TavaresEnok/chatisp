package outwebhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestWebhookPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	if err = db.ApplyMigrations(ctx, urls.Owner, dir); err != nil {
		t.Fatal(err)
	}
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	store := db.NewStore(pools.App, pools.Platform)
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tenant, other := uuid.New(), uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active'),($2,'UTC','active')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("segredo-de-teste-webhook")
	sub := Subscription{
		ID: uuid.New(), TenantID: tenant, URL: "https://example.com/hook",
		Secret: secret, Enabled: true,
		Events: map[string]bool{EventMessageCreated: true},
	}
	stored, err := SealSubscription(ring, sub)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	source := uuid.New()
	delivery, err := Enqueue(sub, EventMessageCreated, source, source, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := InsertSubscription(ctx, tx, stored); err != nil {
			return err
		}
		loaded, err := LoadSubscription(ctx, tx, tenant, sub.ID)
		if err != nil {
			return err
		}
		plain, err := OpenSecret(ring, loaded)
		if err != nil {
			return err
		}
		if !bytes.Equal(plain, secret) {
			return errors.New("segredo")
		}
		first, err := InsertDelivery(ctx, tx, delivery)
		if err != nil {
			return err
		}
		clone := delivery
		clone.ID = uuid.New()
		clone.Body = append([]byte(nil), delivery.Body...)
		clone.Body[0] = 'X'
		again, err := InsertDelivery(ctx, tx, clone)
		if err != nil {
			return err
		}
		if again.ID != first.ID || !bytes.Equal(again.Body, delivery.Body) {
			return errors.New("id instável")
		}
		advanced := Advance(again, 500, nil, now)
		if advanced.ID != first.ID {
			return errors.New("retentativa trocou o id")
		}
		if err = UpdateDelivery(ctx, tx, advanced); err != nil {
			return err
		}
		reloaded, err := LoadDelivery(ctx, tx, tenant, first.ID)
		if err != nil {
			return err
		}
		if reloaded.Attempt != 1 || reloaded.Status != StatusPending || !bytes.Equal(reloaded.Body, delivery.Body) || reloaded.ID != first.ID {
			return errors.New("retentativa gravada")
		}
		changed := reloaded
		changed.Body = []byte(`{"outro":true}`)
		if err = UpdateDelivery(ctx, tx, changed); !errors.Is(err, ErrStored) {
			return errors.New("corpo foi substituído")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, other, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := LoadDelivery(ctx, tx, tenant, delivery.ID); !errors.Is(err, ErrStored) {
			return errors.New("vazou para outro tenant")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
