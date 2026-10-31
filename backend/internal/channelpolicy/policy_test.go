package channelpolicy_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	attendance "chatisp/internal/attendance/store"
	cp "chatisp/internal/channelpolicy"
	policyhttp "chatisp/internal/channelpolicy/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
)

func TestPolicyPostgres(t *testing.T) {
	ctx := context.Background()
	env, e := testdb.Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer env.Close()
	secrets, e := db.NewRoleSecrets()
	if e != nil {
		t.Fatal(e)
	}
	urls, e := db.Bootstrap(ctx, env.AdminURL, secrets)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := db.MigrationsDir()
	if e != nil {
		t.Fatal(e)
	}
	if e = db.ApplyMigrations(ctx, urls.Owner, dir); e != nil {
		t.Fatal(e)
	}
	pools, e := db.OpenPools(ctx, urls)
	if e != nil {
		t.Fatal(e)
	}
	defer pools.Close()
	admin, e := pgx.Connect(ctx, env.AdminURL)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	store := db.NewStore(pools.App, pools.Platform)
	tenant, user, member, inbox, connector, sector, conversation, contact, identity, waba, app := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := sectors.Principal{TenantID: tenant, UserID: user, MembershipID: member, Role: "admin"}
	if e = store.WithPlatform(ctx, func(c context.Context) error {
		if _, e := store.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
			return e
		}
		_, e := store.CreateUser(c, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := store.CreateMembership(c, db.NewMembership{ID: member, UserID: user, Role: p.Role, Status: "active"})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	run := func(f func(context.Context, pgx.Tx) error) {
		t.Helper()
		if e := store.WithTenantTx(ctx, tenant, f); e != nil {
			t.Fatal(e)
		}
	}
	run(func(c context.Context, tx pgx.Tx) error {
		queries := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled,version) VALUES($1,$2,'meta_cloud',$3,'connected',true,'1')`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Policy')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Policy')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, member}},
			{`INSERT INTO contacts(id,tenant_id) VALUES($1,$2)`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'P-1','202610',1)`, []any{conversation, tenant, inbox, sector, contact, identity}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{tenant, uuid.New(), waba, uuid.NewString(), connector, inbox}},
		}
		for _, q := range queries {
			if _, e := tx.Exec(c, q.q, q.a...); e != nil {
				return e
			}
		}
		return nil
	})
	svc := cp.Service{DB: store, Clock: func(context.Context, pgx.Tx) (time.Time, error) { return now, nil }}
	scope := cp.Scope{InboxID: inbox, ContactID: contact, Purpose: "service"}
	dispatch := cp.Dispatch{Scope: scope, ConversationID: conversation, Mode: "requested", Kind: "freeform"}
	decide := func(want string, allowed bool) {
		t.Helper()
		d, e := svc.Evaluate(ctx, p, dispatch)
		if e != nil {
			t.Fatal(e)
		}
		if d.Reason != want || d.Allowed != allowed || !d.EvaluatedAt.Equal(now) {
			t.Fatalf("decision: %+v want %s/%v", d, want, allowed)
		}
	}
	insertMessage := func(at time.Time, origin, direction, visibility string, historical bool) {
		t.Helper()
		run(func(c context.Context, tx pgx.Tx) error {
			_, e := tx.Exec(c, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,local_state,trusted_at,historical) VALUES($1,$2,$3,$4,$5,$6,$7,'received',$8,$9)`, tenant, uuid.New(), conversation, connector, direction, origin, visibility, at, historical)
			return e
		})
	}
	decide("window_closed", false)
	insertMessage(now.Add(-25*time.Hour), "customer", "inbound", "public", false)
	insertMessage(now, "customer", "inbound", "public", true)
	insertMessage(now, "customer", "outbound", "public", false)
	insertMessage(now, "bot", "inbound", "public", false)
	insertMessage(now, "note", "inbound", "internal", false)
	insertMessage(now.Add(100*time.Hour), "customer", "inbound", "public", false)
	decide("window_closed", false)
	base := now
	insertMessage(base.Add(-time.Hour), "customer", "inbound", "public", false)
	decide("window_open", true)
	t.Run("official window isolates external identities across attendance cycles", func(t *testing.T) {
		identityB, currentB, previousB := uuid.New(), uuid.New(), uuid.New()
		run(func(c context.Context, tx pgx.Tx) error {
			if _, e := tx.Exec(c, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identityB, tenant, contact, connector, identityB.String()); e != nil {
				return e
			}
			_, e := tx.Exec(c, `INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number) VALUES($1,$3,$4,$5,$6,$7,'open','P-B-current','202610',1),($2,$3,$4,$5,$6,$7,'closed','P-B-previous','202610',1)`, currentB, previousB, tenant, inbox, sector, contact, identityB)
			return e
		})
		request := dispatch
		request.ConversationID = currentB
		check := func(want string, allowed bool, end *time.Time) {
			t.Helper()
			d, e := svc.Evaluate(ctx, p, request)
			if e != nil {
				t.Fatal(e)
			}
			if d.Reason != want || d.Allowed != allowed || !d.EvaluatedAt.Equal(now) ||
				(end == nil && d.WindowEndsAt != nil) ||
				(end != nil && (d.WindowEndsAt == nil || !d.WindowEndsAt.Equal(*end))) {
				t.Fatalf("identity B decision: %+v want %s/%v end %v", d, want, allowed, end)
			}
		}
		// A has a recent eligible entry; B has never sent an eligible entry.
		check("window_closed", false, nil)
		addEntry := func(conv uuid.UUID, at time.Time) {
			t.Helper()
			run(func(c context.Context, tx pgx.Tx) error {
				_, e := tx.Exec(c, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,local_state,trusted_at) VALUES($1,$2,$3,$4,'inbound','customer','public','received',$5)`, tenant, uuid.New(), conv, connector, at)
				return e
			})
		}
		addEntry(currentB, now.Add(-25*time.Hour))
		expired := now.Add(-time.Hour)
		check("window_closed", false, &expired)
		// A prior closed attendance of B still opens B's channel window.
		addEntry(previousB, now.Add(-2*time.Hour))
		end := now.Add(22 * time.Hour)
		check("window_open", true, &end)
	})
	for _, tc := range []struct {
		operation string
		role      string
	}{
		{"evaluate", "admin"}, {"evaluate", "supervisor"},
		{"list", "admin"}, {"list", "supervisor"},
		{"sync reserve", "admin"}, {"sync apply", "admin"},
	} {
		t.Run("M2 rotation before policy/"+tc.operation+"/"+tc.role, func(t *testing.T) {
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			principal := p
			principal.Role = tc.role
			run(func(c context.Context, tx pgx.Tx) error {
				_, e := tx.Exec(c, `UPDATE memberships SET role=$3 WHERE tenant_id=$1 AND id=$2`, tenant, member, tc.role)
				return e
			})
			defer run(func(c context.Context, tx pgx.Tx) error {
				_, e := tx.Exec(c, `UPDATE memberships SET role='admin' WHERE tenant_id=$1 AND id=$2`, tenant, member)
				return e
			})
			service := svc
			service.Driver = fixtureDriver{}
			done := make(chan error, 1)
			start := func() {
				go func() {
					var err error
					switch tc.operation {
					case "evaluate":
						var d cp.Decision
						d, err = service.Evaluate(c, principal, dispatch)
						if err == nil && !d.Allowed {
							err = fmt.Errorf("dispatch denied after rotation: %+v", d)
						}
					case "list":
						_, err = service.ListTemplates(c, principal, inbox)
					default:
						err = service.SyncTemplates(c, principal, inbox)
					}
					done <- err
				}()
			}
			var release chan struct{}
			if tc.operation == "sync apply" {
				entered := make(chan struct{})
				release = make(chan struct{})
				service.Driver = delayedDriver{entered: entered, release: release}
				start()
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("sync reservation: %v", err)
				case <-c.Done():
					t.Fatal(c.Err())
				}
			}
			err := store.WithTenantTx(c, tenant, func(c context.Context, tx pgx.Tx) error {
				var number uuid.UUID
				var rotationPID int
				if e := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&rotationPID); e != nil {
					return e
				}
				// Same row-lock targets and order as configuration.lockPhone.
				// Hold the real number/connector locks across updateInbox and
				// bumpConnector, exactly as configuration.rotate does.
				if e := tx.QueryRow(c, `SELECT n.id FROM meta_phone_numbers n JOIN channel_connectors cc ON cc.tenant_id=n.tenant_id AND cc.id=n.connector_id WHERE n.tenant_id=$1 AND n.inbox_id=$2 FOR UPDATE OF n,cc`, tenant, inbox).Scan(&number); e != nil {
					return e
				}
				if release != nil {
					close(release)
				} else {
					start()
				}
				// Wait for an actual PG lock dependency, not a timing guess.
				// Previously policy held inbox here while waiting for connector.
				for {
					var waiting bool
					if e := admin.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE $1=ANY(pg_blocking_pids(a.pid)))`, rotationPID).Scan(&waiting); e != nil {
						return e
					}
					if waiting {
						break
					}
					select {
					case e := <-done:
						return fmt.Errorf("policy did not wait for rotation: %v", e)
					case <-time.After(10 * time.Millisecond):
					case <-c.Done():
						return c.Err()
					}
				}
				if _, e := tx.Exec(c, `SET LOCAL lock_timeout='1s'`); e != nil {
					return e
				}
				if _, e := tx.Exec(c, `UPDATE inboxes SET name='Rotated',updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, inbox); e != nil {
					return e
				}
				_, e := tx.Exec(c, `UPDATE channel_connectors SET version=(version::bigint+1)::text,status='connected',enabled=true WHERE tenant_id=$1 AND id=$2`, tenant, connector)
				return e
			})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-c.Done():
				t.Fatal(c.Err())
			}
		})
	}
	t.Run("ingress contact lock precedes dispatch conversation lock", func(t *testing.T) {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		finished := make(chan error, 1)
		pid := make(chan int, 1)
		err := store.WithTenantTx(c, tenant, func(c context.Context, tx pgx.Tx) error {
			var id uuid.UUID
			// ApplyInbound holds this contact lock before calling AcceptInbound.
			if e := tx.QueryRow(c, `SELECT id FROM contacts WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenant, contact).Scan(&id); e != nil {
				return e
			}
			go func() {
				finished <- store.WithTenantTx(c, tenant, func(c context.Context, tx pgx.Tx) error {
					var backendPID int
					if e := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&backendPID); e != nil {
						return e
					}
					pid <- backendPID
					d, e := svc.EvaluateDispatchTx(c, tx, p, dispatch)
					if e == nil && !d.Allowed {
						return fmt.Errorf("dispatch denied after ingress: %+v", d)
					}
					return e
				})
			}()
			var backendPID int
			select {
			case backendPID = <-pid:
			case <-c.Done():
				return c.Err()
			}
			// Observe the actual lock wait, not a scheduling delay. With the old
			// order dispatch already held the conversation here, causing deadlock.
			for {
				var waiting bool
				if e := admin.QueryRow(c, `SELECT cardinality(pg_blocking_pids($1))>0`, backendPID).Scan(&waiting); e != nil {
					return e
				}
				if waiting {
					break
				}
				select {
				case <-time.After(10 * time.Millisecond):
				case <-c.Done():
					return c.Err()
				}
			}
			_, e := (attendance.Repo{}).AcceptInbound(c, tx, attendance.InboundInput{
				TenantID: tenant, InboxID: inbox, ContactID: contact, IdentityID: identity,
				ConnectorID: connector, ExternalMessageID: uuid.NewString(), Body: "ingress",
				Period: "202610", TrustedAt: base.Add(-time.Hour), OpenedAt: base,
			})
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-finished:
			if e != nil {
				t.Fatal(e)
			}
		case <-c.Done():
			t.Fatal(c.Err())
		}
	})
	now = base.Add(23 * time.Hour)
	decide("window_closed", false)
	now = base
	// Proactive sends require an explicit grant even during an open window.
	dispatch.Mode = "proactive"
	decide("consent_required", false)
	grant := cp.ConsentInput{Scope: scope, Source: "customer_request", Evidence: "reference:event-123"}
	if _, e = svc.RecordConsent(ctx, p, grant); e != nil {
		t.Fatal(e)
	}
	decide("window_open", true)
	// Dispatch holds its scope lock through commit; a concurrent opt-out must wait.
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
			d, e := svc.EvaluateDispatchTx(c, tx, p, dispatch)
			if e != nil {
				return e
			}
			if !d.Allowed {
				return fmt.Errorf("dispatch denied")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case e := <-done:
		t.Fatalf("dispatch tx: %v", e)
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch tx timeout")
	}
	revoking := make(chan error, 1)
	concurrentRevoke := grant
	concurrentRevoke.Revoke = true
	go func() { _, e := svc.RecordConsent(ctx, p, concurrentRevoke); revoking <- e }()
	select {
	case e := <-revoking:
		close(release)
		t.Fatalf("optout escaped dispatch lock: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-revoking:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("optout timeout")
	}
	decide("consent_required", false)
	if _, e = svc.RecordConsent(ctx, p, grant); e != nil {
		t.Fatal(e)
	}
	dispatch.Purpose = "marketing"
	decide("consent_required", false)
	dispatch.Purpose = scope.Purpose
	pending, uncertain, other := uuid.New(), uuid.New(), uuid.New()
	run(func(c context.Context, tx pgx.Tx) error {
		for _, id := range []uuid.UUID{pending, uncertain} {
			if e := svc.RegisterPendingTx(c, tx, p, scope, id); e != nil {
				return e
			}
		}
		if e := cp.SetPendingStateTx(c, tx, p, scope, uncertain, "uncertain"); e != nil {
			return e
		}
		otherScope := scope
		otherScope.Purpose = "marketing"
		return svc.RegisterPendingTx(c, tx, p, otherScope, other)
	})
	revoke := grant
	revoke.Revoke = true
	// A rollback must undo both revocation and cancellation.
	rollback := errors.New("rollback")
	e = store.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		if _, e := svc.RecordConsentTx(c, tx, p, revoke); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	decide("window_open", true)
	ids, e := svc.RecordConsent(ctx, p, revoke)
	if e != nil {
		t.Fatal(e)
	}
	if len(ids) != 1 || ids[0] != pending {
		t.Fatalf("invalidated %v", ids)
	}
	decide("consent_required", false)
	run(func(c context.Context, tx pgx.Tx) error {
		var n int
		e := tx.QueryRow(c, `SELECT count(*) FROM channel_policy_pending WHERE tenant_id=$1 AND ((id=$2 AND state='uncertain') OR (id=$3 AND state='pending'))`, tenant, uncertain, other).Scan(&n)
		if e == nil && n != 2 {
			return fmt.Errorf("scope/state isolation: %d", n)
		}
		return e
	})
	dispatch.Mode = "requested"
	decide("window_open", true)
	now = base.Add(24 * time.Hour)
	dispatch.Kind = "template"
	dispatch.TemplateID = "welcome"
	template := cp.Template{ExternalID: "welcome", Name: "welcome", Language: "pt_BR", Category: "UTILITY", State: "APPROVED", Components: json.RawMessage(`[]`)}
	svc.Driver = cp.FakeCatalog{Items: []cp.Template{template}}
	if e = svc.SyncTemplates(ctx, p, inbox); e != nil {
		t.Fatal(e)
	}
	list, e := svc.ListTemplates(ctx, p, inbox)
	if e != nil || len(list) != 1 || list[0].Source != "fake" {
		t.Fatalf("fake catalog: %v %v", list, e)
	}
	decide("template_not_authorized", false)
	// Trusted driver interface fixture; no network calls occur.
	svc.Driver = fixtureDriver{[]cp.Template{template}}
	if e = svc.SyncTemplates(ctx, p, inbox); e != nil {
		t.Fatal(e)
	}
	decide("approved_template", true)
	template.State = "PAUSED"
	svc.Driver = fixtureDriver{[]cp.Template{template}}
	if e = svc.SyncTemplates(ctx, p, inbox); e != nil {
		t.Fatal(e)
	}
	decide("template_not_authorized", false)
	for _, tc := range []struct {
		name  string
		items []cp.Template
		err   error
	}{
		{"paused", []cp.Template{template}, nil},
		{"empty", nil, nil},
		{"failed", nil, errors.New("local driver failure")},
	} {
		t.Run("out of order catalog responses/"+tc.name, func(t *testing.T) {
			c, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			// Seed a known paused snapshot; even a failed newer request fences A.
			seed := svc
			seed.Driver = fixtureDriver{[]cp.Template{template}}
			if e := seed.SyncTemplates(c, p, inbox); e != nil {
				t.Fatal(e)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			old := template
			old.State = "APPROVED"
			slow := svc
			slow.Driver = delayedDriver{items: []cp.Template{old}, entered: entered, release: release}
			done := make(chan error, 1)
			go func() { done <- slow.SyncTemplates(c, p, inbox) }()
			select {
			case <-entered:
			case e := <-done:
				t.Fatalf("old sync failed before driver: %v", e)
			case <-c.Done():
				t.Fatal(c.Err())
			}
			newer := svc
			newer.Driver = delayedDriver{items: tc.items, err: tc.err}
			err := newer.SyncTemplates(c, p, inbox)
			if (err != nil) != (tc.err != nil) {
				t.Fatalf("newer sync: %v", err)
			}
			release <- struct{}{}
			select {
			case e := <-done:
				if !errors.Is(e, cp.ErrStaleCatalog) {
					t.Fatalf("obsolete response accepted: %v", e)
				}
			case <-c.Done():
				t.Fatal(c.Err())
			}
			decide("template_not_authorized", false)
			list, e := svc.ListTemplates(c, p, inbox)
			want := 1
			if tc.name == "empty" {
				want = 0
			}
			if e != nil || len(list) != want || (want == 1 && list[0].State != "PAUSED") {
				t.Fatalf("catalog regressed: %v %v", list, e)
			}
		})
	}
	// Restore a row for the cross-WABA isolation fixture below.
	svc.Driver = fixtureDriver{[]cp.Template{template}}
	if e = svc.SyncTemplates(ctx, p, inbox); e != nil {
		t.Fatal(e)
	}
	// Another WABA's approved template cannot authorize this channel.
	run(func(c context.Context, tx pgx.Tx) error {
		w := uuid.New()
		if _, e := tx.Exec(c, `INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, tenant, w, app, w.String()); e != nil {
			return e
		}
		_, e := tx.Exec(c, `INSERT INTO wa_templates SELECT tenant_id,$2,'other',name,language,category,'APPROVED',components,'driver',synced_at FROM wa_templates WHERE tenant_id=$1 AND waba_id=$3`, tenant, w, waba)
		return e
	})
	dispatch.TemplateID = "other"
	decide("template_not_authorized", false)
	// Catalog snapshot removes templates no longer present.
	svc.Driver = fixtureDriver{}
	if e = svc.SyncTemplates(ctx, p, inbox); e != nil {
		t.Fatal(e)
	}
	list, e = svc.ListTemplates(ctx, p, inbox)
	if e != nil || len(list) != 0 {
		t.Fatalf("snapshot: %v %v", list, e)
	}
	// RLS with another tenant context hides every policy table.
	if e = store.WithTenantTx(ctx, uuid.New(), func(c context.Context, tx pgx.Tx) error {
		for _, table := range []string{"contact_consents", "wa_templates", "channel_policy_pending", "channel_policy_catalog_sync"} {
			var n int
			if e := tx.QueryRow(c, `SELECT count(*) FROM `+table).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				return fmt.Errorf("RLS leak %s", table)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var forceCount int
	if e = admin.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relname IN ('contact_consents','wa_templates','channel_policy_pending','channel_policy_catalog_sync') AND relrowsecurity AND relforcerowsecurity`).Scan(&forceCount); e != nil || forceCount != 4 {
		t.Fatalf("FORCE RLS: %d %v", forceCount, e)
	}
	// HTTP uses real auth sessions and validates Origin and CSRF before mutation.
	ring, e := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if e != nil {
		t.Fatal(e)
	}
	authSvc, e := auth.New(auth.Options{Store: store, Keys: ring})
	if e != nil {
		t.Fatal(e)
	}
	token, csrf := uuid.NewString(), uuid.NewString()
	if e = store.WithAuth(ctx, func(c context.Context) error {
		return store.InsertSession(c, db.NewSession{ID: uuid.New(), TokenHash: auth.HashSecret(token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(csrf), Now: base, IdleExpiresAt: base.Add(time.Hour), AbsoluteExpiresAt: base.Add(time.Hour)})
	}); e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("policy", "1")
	cfg.CreateHooks = nil
	policyhttp.Register(humachi.New(router, cfg), policyhttp.Dependencies{Auth: authSvc, Policy: &svc, TrustedOrigins: []string{"https://panel.example"}})
	path := fmt.Sprintf("/api/panel/inboxes/%s/channel-policy", inbox)
	request := func(method, suffix, body, session, origin, csrfToken string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrfToken)
		r.AddCookie(&http.Cookie{Name: "__Host-session", Value: session})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("HTTP %s %s: %d want %d: %s", method, suffix, w.Code, want, w.Body.String())
		}
	}
	consentPath := "/contacts/" + contact.String() + "/consents"
	body := `{"purpose":"service","source":"customer","evidence":"reference:123","revoke":true}`
	request("GET", "/templates", "", "", "", "", 401)
	request("POST", consentPath, body, token, "https://evil.example", csrf, 403)
	request("POST", consentPath, body, token, "https://panel.example", "bad", 403)
	// Admin enrollment is enforced by the real auth gate; complete MFA for successful operations.
	request("POST", consentPath, body, token, "https://panel.example", csrf, 403)
	actor, e := authSvc.Authenticate(ctx, token, csrf, true)
	if e != nil {
		t.Fatal(e)
	}
	enrollment, e := authSvc.BeginEnrollment(ctx, actor)
	if e != nil {
		t.Fatal(e)
	}
	code, e := totp.GenerateCode(enrollment.Secret, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = authSvc.ConfirmEnrollment(ctx, actor, code); e != nil {
		t.Fatal(e)
	}
	request("POST", consentPath, body, token, "https://panel.example", csrf, 200)
	request("GET", "/templates", "", token, "", "", 200)
	// Role is reread transactionally: supervisor can read/record but cannot sync.
	run(func(c context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(c, `UPDATE memberships SET role='supervisor' WHERE tenant_id=$1 AND id=$2`, tenant, member)
		return e
	})
	request("POST", "/templates/sync", "", token, "https://panel.example", csrf, 403)
	request("GET", "/templates", "", token, "", "", 200)
	// Revoking sector access also revokes consent mutation for an agent.
	run(func(c context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(c, `UPDATE memberships SET role='agent' WHERE tenant_id=$1 AND id=$2`, tenant, member); e != nil {
			return e
		}
		_, e := tx.Exec(c, `DELETE FROM sector_memberships WHERE tenant_id=$1 AND membership_id=$2`, tenant, member)
		return e
	})
	request("POST", consentPath, body, token, "https://panel.example", csrf, 403)
	// Suspension must block even an administrator with valid session material.
	if _, e = admin.Exec(ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, tenant); e != nil {
		t.Fatal(e)
	}
	p.Role = "agent"
	if _, e = svc.RecordConsent(ctx, p, grant); !errors.Is(e, sectors.ErrForbidden) {
		t.Fatalf("suspension: %v", e)
	}
}

type fixtureDriver struct{ items []cp.Template }

func (f fixtureDriver) Templates(context.Context, string) ([]cp.Template, error) { return f.items, nil }
func (fixtureDriver) IsFake() bool                                               { return false }

type delayedDriver struct {
	items   []cp.Template
	err     error
	entered chan struct{}
	release chan struct{}
}

func (d delayedDriver) Templates(ctx context.Context, _ string) ([]cp.Template, error) {
	if d.entered != nil {
		close(d.entered)
		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return d.items, d.err
}
func (delayedDriver) IsFake() bool { return false }
