package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTenantStatusGateRollbackReleases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tenant := seedTenant(t, "agent").tenant
	held := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	rollback := errors.New("rollback")
	go func() {
		finished <- testStore.WithTenantTx(ctx, tenant, func(c context.Context, _ pgx.Tx) error {
			if err := testStore.LockTenant(c); err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
				return rollback
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-held
	result := make(chan error, 1)
	go func() {
		result <- testStore.WithPlatform(ctx, func(c context.Context) error { _, err := testStore.SetTenantStatus(c, tenant, "suspended"); return err })
	}()
	admin := adminConn(t)
	defer admin.Close(context.Background())
	for {
		var blocked bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT pg_advisory_xact_lock%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	if err := <-finished; !errors.Is(err, rollback) {
		t.Fatalf("rollback: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithPlatform(ctx, func(c context.Context) error {
		row, err := testStore.GetTenantByID(c, tenant)
		if err != nil {
			return err
		}
		if row.Status != "suspended" {
			t.Errorf("status=%s", row.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTenantStatusGateLinearizationSuspensionWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tenant := seedTenant(t, "agent").tenant
	held := make(chan struct{})
	release := make(chan struct{})
	suspensionDone := make(chan error, 1)

	go func() {
		suspensionDone <- testStore.WithPlatform(ctx, func(c context.Context) error {
			if _, err := testStore.SetTenantStatus(c, tenant, "suspended"); err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	<-held
	var observedStatus string
	tenantTxDone := make(chan error, 1)
	go func() {
		tenantTxDone <- testStore.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
			if err := testStore.LockTenant(c); err != nil {
				return err
			}
			return tx.QueryRow(c, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&observedStatus)
		})
	}()

	admin := adminConn(t)
	defer admin.Close(context.Background())
	for {
		var blocked bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT pg_advisory_xact_lock%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}

	close(release)
	if err := <-suspensionDone; err != nil {
		t.Fatalf("suspension failed: %v", err)
	}
	if err := <-tenantTxDone; err != nil {
		t.Fatalf("tenant tx failed: %v", err)
	}
	if observedStatus != "suspended" {
		t.Fatalf("expected observed status 'suspended' under lock after suspension won, got %q", observedStatus)
	}
}

func TestTenantStatusGateLinearizationTenantTxWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tenant := seedTenant(t, "agent").tenant
	held := make(chan struct{})
	release := make(chan struct{})
	tenantTxDone := make(chan error, 1)

	var observedStatus string
	go func() {
		tenantTxDone <- testStore.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
			if err := testStore.LockTenant(c); err != nil {
				return err
			}
			if err := tx.QueryRow(c, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&observedStatus); err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	<-held
	suspensionDone := make(chan error, 1)
	go func() {
		suspensionDone <- testStore.WithPlatform(ctx, func(c context.Context) error {
			_, err := testStore.SetTenantStatus(c, tenant, "suspended")
			return err
		})
	}()

	admin := adminConn(t)
	defer admin.Close(context.Background())
	for {
		var blocked bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT pg_advisory_xact_lock%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}

	if observedStatus != "active" {
		t.Fatalf("expected observed status 'active' before suspension commit, got %q", observedStatus)
	}

	close(release)
	if err := <-tenantTxDone; err != nil {
		t.Fatalf("tenant tx failed: %v", err)
	}
	if err := <-suspensionDone; err != nil {
		t.Fatalf("suspension failed: %v", err)
	}

	if err := testStore.WithPlatform(ctx, func(c context.Context) error {
		row, err := testStore.GetTenantByID(c, tenant)
		if err != nil {
			return err
		}
		if row.Status != "suspended" {
			t.Fatalf("expected final status 'suspended', got %q", row.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
