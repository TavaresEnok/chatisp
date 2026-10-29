package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db"
)

func TestWithTenantTxPinsScopeAndRollsBack(t *testing.T) {
	ctx := context.Background()
	if err := testStore.WithTenantTx(ctx, uuid.Nil, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, db.ErrTenantContextRequired) {
		t.Fatalf("tenant vazio: %v", err)
	}

	var tenantID uuid.UUID
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		row, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       uuid.New(),
			Timezone: "America/Sao_Paulo",
			Status:   "active",
			Limits:   json.RawMessage(`{}`),
		})
		if err != nil {
			return err
		}
		tenantID = row.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	kept := uuid.New()
	if err := testStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		got, err := testStore.GetTenant(ctx)
		if err != nil {
			return err
		}
		if got.ID != tenantID {
			t.Fatalf("tenant da transação: %s", got.ID)
		}
		var same string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.tenant_id', true)`).Scan(&same); err != nil {
			return err
		}
		if same != tenantID.String() {
			t.Fatalf("app.tenant_id: %s", same)
		}
		return testStore.InsertConversation(ctx, kept, "placeholder")
	}); err != nil {
		t.Fatal(err)
	}

	dropped := uuid.New()
	if err := testStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, _ pgx.Tx) error {
		if err := testStore.InsertConversation(ctx, dropped, "descartado"); err != nil {
			return err
		}
		return errors.New("desfaz")
	}); err == nil || err.Error() != "desfaz" {
		t.Fatalf("rollback: %v", err)
	}

	if err := testStore.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		rows, err := testStore.ListConversationSummaries(ctx)
		if err != nil {
			return err
		}
		seenKept, seenDropped := false, false
		for _, row := range rows {
			if row == "placeholder" {
				seenKept = true
			}
			if row == "descartado" {
				seenDropped = true
			}
		}
		if !seenKept || seenDropped {
			t.Fatalf("resumos: %v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWithIngressTxRejectsAppPool(t *testing.T) {
	ctx := context.Background()
	err := db.WithIngressTx(ctx, testPools.App, func(context.Context, pgx.Tx) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "papel de banco recusado") {
		t.Fatalf("pool da aplicação: %v", err)
	}

	var scope, role string
	var tenantSetting *string
	if err := db.WithIngressTx(ctx, testPools.Ingress, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT current_user, current_setting('app.access_scope', true), current_setting('app.tenant_id', true)`).Scan(&role, &scope, &tenantSetting); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if role != db.RoleIngress || scope != "ingress" || tenantSetting != nil {
		t.Fatalf("ingresso role=%s scope=%s tenant=%v", role, scope, tenantSetting)
	}
}
