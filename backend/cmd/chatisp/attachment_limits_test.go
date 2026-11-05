package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attachments"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

// The installed runtime resolves attachment limits from the provider plan.
// max_storage_bytes (set by the platform API or bootstrap) is enough; a plan
// without any storage limit fails closed with a specific error.
func TestTenantAttachmentLimitsFromPlan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	t.Cleanup(env.Close)
	urls, _, err := prepareDevDatabase(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	store, closeStore := openStore(t, ctx, urls)
	defer closeStore()
	cases := []struct {
		name   string
		limits string
		want   attachments.Limits
		err    error
	}{
		{name: "sem limite", limits: "", err: attachments.ErrPlanUnset},
		{name: "só limites de atendentes", limits: `{"max_agents":3}`, err: attachments.ErrPlanUnset},
		{name: "max_storage_bytes do plano", limits: `{"max_storage_bytes":1048576}`, want: attachments.Limits{PlanBytes: 1048576, FileBytes: attachments.MaxFileBytes}},
		{name: "valores explícitos de anexo", limits: `{"max_storage_bytes":5,"attachment_plan_bytes":2048,"attachment_reserve_bytes":512,"attachment_file_bytes":1024}`, want: attachments.Limits{PlanBytes: 2048, IncomingReserveBytes: 512, FileBytes: 1024}},
		{name: "arquivo acima do teto técnico", limits: `{"max_storage_bytes":1073741824,"attachment_file_bytes":1073741824}`, want: attachments.Limits{PlanBytes: 1073741824, FileBytes: attachments.MaxFileBytes}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			var raw json.RawMessage
			if tc.limits != "" {
				raw = json.RawMessage(tc.limits)
			}
			if err := store.WithPlatform(ctx, func(ctx context.Context) error {
				_, err := store.CreateTenant(ctx, db.NewTenant{ID: id, Timezone: "America/Sao_Paulo", Status: "active", Limits: raw})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var got attachments.Limits
			var gotErr error
			if err := store.WithTenantTx(ctx, id, func(ctx context.Context, tx pgx.Tx) error {
				got, gotErr = tenantAttachmentLimits(ctx, tx, id)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if tc.err != nil {
				if !errors.Is(gotErr, tc.err) {
					t.Fatalf("erro: %v", gotErr)
				}
				return
			}
			if gotErr != nil || got != tc.want {
				t.Fatalf("limites: %+v %v", got, gotErr)
			}
		})
	}
}
