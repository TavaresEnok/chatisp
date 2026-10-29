package auth

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
)

var (
	testEnv   *testdb.Env
	testURLs  db.RoleURLs
	testPools *db.Pools
	testStore *db.Store
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

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
	pools, err := db.OpenPools(ctx, db.RoleURLs{
		App:      urls.App,
		Platform: urls.Platform,
		Ingress:  urls.Ingress,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao abrir pools: %s\n", err)
		env.Close()
		os.Exit(1)
	}

	testEnv = env
	testURLs = urls
	testPools = pools
	testStore = db.NewStore(pools.App, pools.Platform)

	code := m.Run()
	pools.Close()
	env.Close()
	os.Exit(code)
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) Advance(step time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(step)
	c.mu.Unlock()
}

func testPolicy() Policy {
	policy := DefaultPolicy
	policy.Idle = 10 * time.Minute
	policy.Absolute = 30 * time.Minute
	policy.InviteTTL = time.Minute
	policy.ResetTTL = time.Minute
	policy.LockoutThreshold = 3
	policy.LockoutBase = time.Minute
	policy.LockoutMax = time.Hour
	policy.LockoutWindow = 15 * time.Minute
	policy.ResetLimit = 2
	policy.TokenFailureLimit = 3
	return policy
}

type account struct {
	tenant     uuid.UUID
	user       uuid.UUID
	membership uuid.UUID
	email      string
}

func seedAccount(t *testing.T, role, password string) account {
	t.Helper()
	item := account{tenant: uuid.New(), user: uuid.New(), membership: uuid.New()}
	item.email = item.user.String() + "@example.com"
	ctx := context.Background()
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       item.tenant,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
		}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: item.user, Email: item.email, Status: "active"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenant(ctx, item.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     item.membership,
			UserID: item.user,
			Role:   role,
			Status: "active",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if password != "" {
		setPassword(t, item.user, password)
	}
	return item
}

func addMembership(t *testing.T, userID uuid.UUID, role string) account {
	t.Helper()
	item := account{tenant: uuid.New(), user: userID, membership: uuid.New()}
	ctx := context.Background()
	err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := testStore.CreateTenant(ctx, db.NewTenant{
			ID:       item.tenant,
			Timezone: "America/Sao_Paulo",
			Status:   "active",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithTenant(ctx, item.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{
			ID:     item.membership,
			UserID: userID,
			Role:   role,
			Status: "active",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func setPassword(t *testing.T, userID uuid.UUID, password string) {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.WithAuth(context.Background(), func(ctx context.Context) error {
		return testStore.SetPasswordHash(ctx, userID, hash)
	})
	if err != nil {
		t.Fatal(err)
	}
}
