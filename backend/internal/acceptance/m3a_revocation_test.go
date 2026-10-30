package acceptance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Copy real binding fields while giving the closed fixture its own protocol.
// This exercises the production INSERT trigger, FKs and app-role FORCE RLS.
func m3aCopyConversation(ctx context.Context, tx pgx.Tx, tenant, source, id, sector uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO conversations
	 (tenant_id,id,summary,inbox_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number,status,sector_id)
	 SELECT tenant_id,$3::uuid,summary,inbox_id,contact_id,contact_identity_id,($3::uuid)::text,protocol_period,1,'closed',$4
	 FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, source, id, sector)
	return err
}

func testM3ARejectAssignment(t *testing.T, ctx context.Context, store *db.Store, tenant, source, valid, invalid uuid.UUID) {
	t.Helper()
	for _, update := range []bool{false, true} {
		err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
			id := uuid.New()
			if !update {
				return m3aCopyConversation(c, tx, tenant, source, id, invalid)
			}
			if err := m3aCopyConversation(c, tx, tenant, source, id, valid); err != nil {
				return err
			}
			_, err := tx.Exec(c, `UPDATE conversations SET sector_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, invalid)
			return err
		})
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23503" {
			t.Fatalf("invalid assignment update=%v sector=%s: %v", update, invalid, err)
		}
	}
}

func testM3AHistoricalIntegrity(t *testing.T, ctx context.Context, store *db.Store, tenant, sector, conversation uuid.UUID) {
	t.Helper()
	// A no-op assignment and ordinary update must still work on preserved rows.
	if err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(c, `UPDATE conversations SET sector_id=sector_id,summary=summary WHERE tenant_id=$1 AND id=$2`, tenant, conversation)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Once all operational references are removed, history still prevents the
	// durable sector from being deleted. The failed transaction restores links.
	err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2`,
			`DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2`,
			`DELETE FROM sectors WHERE tenant_id=$1 AND id=$2`,
		} {
			if _, err := tx.Exec(c, q, tenant, sector); err != nil {
				return err
			}
		}
		return nil
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23503" || pg.ConstraintName != "conversations_sector_fkey" {
		t.Fatalf("durable historical sector FK: %v", err)
	}
}

func testM3AAssignmentRaces(t *testing.T, parent context.Context, store *db.Store, adminURL string, tenant, box, valid, revoked, source uuid.UUID) {
	t.Helper()
	admin, err := pgx.Connect(parent, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(parent)
	for _, update := range []bool{false, true} {
		for _, removalFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("assignment-race/update=%v/removal-first=%v", update, removalFirst), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 10*time.Second)
				defer cancel()
				id := uuid.New()
				if update {
					if err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
						return m3aCopyConversation(c, tx, tenant, source, id, valid)
					}); err != nil {
						t.Fatal(err)
					}
				}
				assign := func(c context.Context, tx pgx.Tx) error {
					if !update {
						return m3aCopyConversation(c, tx, tenant, source, id, revoked)
					}
					_, err := tx.Exec(c, `UPDATE conversations SET sector_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, revoked)
					return err
				}
				remove := func(c context.Context, tx pgx.Tx) error {
					// Deliberately use SQL without the service's stronger exclusive
					// tenant gate: the DB invariant must itself survive this race.
					_, err := tx.Exec(c, `DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3`, tenant, revoked, box)
					return err
				}
				first, second := assign, remove
				if removalFirst {
					first, second = remove, assign
				}
				done := make(chan error, 1)
				err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
					if err := first(c, tx); err != nil {
						return err
					}
					var holder int
					if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
						return err
					}
					pid := make(chan int, 1)
					go func() {
						done <- store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
							var waiter int
							if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&waiter); err != nil {
								return err
							}
							pid <- waiter
							return second(c, tx)
						})
					}()
					var waiter int
					select {
					case waiter = <-pid:
					case err := <-done:
						return fmt.Errorf("waiter startup: %v", err)
					case <-ctx.Done():
						return ctx.Err()
					}
					for {
						var blocked bool
						if err := admin.QueryRow(c, `SELECT $1::int = ANY(pg_blocking_pids($2))`, holder, waiter).Scan(&blocked); err != nil {
							return err
						}
						if blocked {
							t.Logf("PG lock evidence: backend %d blocks %d", holder, waiter)
							return nil // Commit first, then inspect the waiter's result.
						}
						select {
						case err := <-done:
							return fmt.Errorf("operation did not wait for lock: %v", err)
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(5 * time.Millisecond):
						}
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				err = <-done
				if removalFirst {
					var pg *pgconn.PgError
					if !errors.As(err, &pg) || pg.Code != "23503" {
						t.Fatalf("assignment after deletion: %v", err)
					}
				} else if err != nil {
					t.Fatalf("deletion after assignment: %v", err)
				}
				if err := store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
					var count int
					if err := tx.QueryRow(c, `SELECT count(*) FROM conversations WHERE tenant_id=$1 AND id=$2 AND sector_id=$3`, tenant, id, revoked).Scan(&count); err != nil {
						return err
					}
					want := 1
					if removalFirst {
						want = 0
					}
					if count != want {
						return fmt.Errorf("historical assignment count=%d want=%d", count, want)
					}
					_, err := tx.Exec(c, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, tenant, revoked, box)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
