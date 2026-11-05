package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"chatisp/internal/contactdirectory"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
)

// The same phone number in two providers is two unrelated contacts: nothing is
// merged and neither provider can read the other's record.
func TestSameNumberInTwoProvidersStaysSeparate(t *testing.T) {
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
	store := db.NewStore(pools.App, pools.Platform)
	seed := func() auth.Principal {
		p := auth.Principal{TenantID: uuid.New(), UserID: uuid.New(), MembershipID: uuid.New(), Role: "admin"}
		if err := store.WithPlatform(ctx, func(c context.Context) error {
			if _, e := store.CreateTenant(c, db.NewTenant{ID: p.TenantID, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
				return e
			}
			_, e := store.CreateUser(c, db.NewUser{ID: p.UserID, Email: fmt.Sprintf("%s@example.com", p.UserID), Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.WithTenant(ctx, p.TenantID, func(c context.Context) error {
			_, e := store.CreateMembership(c, db.NewMembership{ID: p.MembershipID, UserID: p.UserID, Role: "admin", Status: "active"})
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := seed(), seed()
	svc := contactdirectory.Service{DB: store, Visibility: testVisibility{uuid.New()}}
	const phone = "+5511988887777"
	ca, err := svc.Create(ctx, a, contactdirectory.Input{Name: "Cliente A", Phone: phone})
	if err != nil {
		t.Fatal(err)
	}
	cb, err := svc.Create(ctx, b, contactdirectory.Input{Name: "Cliente B", Phone: phone})
	if err != nil {
		t.Fatalf("o mesmo número em outro provedor deveria ser aceito: %v", err)
	}
	if ca.ID == cb.ID {
		t.Fatal("os dois provedores compartilham o mesmo contato")
	}
	if _, err := svc.Get(ctx, a, cb.ID); err == nil {
		t.Fatal("o provedor A leu o contato do provedor B")
	}
	if _, err := svc.Get(ctx, b, ca.ID); err == nil {
		t.Fatal("o provedor B leu o contato do provedor A")
	}
	for _, who := range []struct {
		p    auth.Principal
		name string
	}{{a, "Cliente A"}, {b, "Cliente B"}} {
		page, err := svc.List(ctx, who.p, phone, "name", 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].Name != who.name {
			t.Fatalf("lista de %s: %+v", who.name, page.Items)
		}
	}
}
