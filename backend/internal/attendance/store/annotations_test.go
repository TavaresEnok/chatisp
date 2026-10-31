package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAnnotationsMigrationBackfillsExistingMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	pools, err := db.OpenPools(ctx, urls)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	s := db.NewStore(pools.App, pools.Platform)
	dir, err := db.MigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range entries {
		if f.Name() >= "00014" || filepath.Ext(f.Name()) != ".sql" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, f.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(tmp, f.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, tmp); err != nil {
		t.Fatal(err)
	}
	tenant, user, member, connector, inbox, contact, identity, conv, m1, m2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err = s.WithPlatform(ctx, func(c context.Context) error {
		if _, e := s.CreateTenant(c, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active", Limits: json.RawMessage(`{}`)}); e != nil {
			return e
		}
		_, e := s.CreateUser(c, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.WithTenant(ctx, tenant, func(c context.Context) error {
		_, e := s.CreateMembership(c, db.NewMembership{ID: member, UserID: user, Role: "agent", Status: "active"})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		statements := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, []any{inbox, tenant, connector}},
			{`WITH s AS (INSERT INTO sectors (tenant_id,id,name,legacy_default) VALUES ($1,$2,'Padrão',true)), i AS (INSERT INTO sector_inboxes VALUES ($1,$2,$3)), m AS (INSERT INTO sector_memberships VALUES ($1,$2,$4)) INSERT INTO inbox_sector_defaults VALUES ($1,$3,$2)`, []any{tenant, uuid.New(), inbox, member}},
			{`INSERT INTO contacts(id,tenant_id) VALUES($1,$2)`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,summary,inbox_id,contact_id,contact_identity_id,protocol,protocol_period,cycle_number) VALUES($1,$2,'legado',$3,$4,$5,'P-1','202610',1)`, []any{conv, tenant, inbox, contact, identity}},
			{`INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility) VALUES($1,$2,$4,$5,'inbound','customer','public'),($1,$3,$4,$5,'inbound','customer','public')`, []any{tenant, m1, m2, conv, connector}},
			{`INSERT INTO realtime_events(tenant_id,seq,id,event_type,aggregate_type,aggregate_id,version,payload) VALUES($1,7,$2,'conversation.message','conversation',$3,1,jsonb_build_object('message_id',$4::text))`, []any{tenant, uuid.New(), conv, m1}},
		}
		for _, x := range statements {
			if _, e := tx.Exec(c, x.q, x.a...); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "00014_m3_annotations.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(tmp, "00014_m3_annotations.sql"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = db.ApplyMigrations(ctx, urls.Owner, tmp); err != nil {
		t.Fatal(err)
	}
	if err = s.WithTenantTx(ctx, tenant, func(c context.Context, tx pgx.Tx) error {
		var a, b int64
		if e := tx.QueryRow(c, `SELECT seq FROM annotation_message_cursors WHERE tenant_id=$1 AND message_id=$2`, tenant, m1).Scan(&a); e != nil {
			return e
		}
		if e := tx.QueryRow(c, `SELECT seq FROM annotation_message_cursors WHERE tenant_id=$1 AND message_id=$2`, tenant, m2).Scan(&b); e != nil {
			return e
		}
		if a != 7 || b != 0 {
			t.Fatalf("backfill %d %d", a, b)
		}
		state, e := AnnotationRead(c, tx, tenant, user, conv)
		if e != nil {
			return e
		}
		if state.UnreadCount != 2 {
			t.Fatalf("unread %d", state.UnreadCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var forced int
	if err = pools.Owner.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE oid IN ('messages'::regclass,'realtime_events'::regclass) AND relforcerowsecurity`).Scan(&forced); err != nil || forced != 2 {
		t.Fatalf("RLS: %d %v", forced, err)
	}
}

// Wait for a specific backend to be blocked by another transaction, rather
// than relying on scheduling delays to establish the interleaving.
func waitAnnotationBlocked(ctx context.Context, pid int32) error {
	for {
		var blocked bool
		if err := testPools.App.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type annotationReadInterleaveTx struct {
	pgx.Tx
	beforeTags func() error
}

func (tx *annotationReadInterleaveTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.beforeTags != nil && strings.Contains(sql, "FROM annotation_tags t JOIN") {
		fn := tx.beforeTags
		tx.beforeTags = nil
		if err := fn(); err != nil {
			return nil, err
		}
	}
	return tx.Tx.Query(ctx, sql, args...)
}

func TestAnnotationConcurrentCatalogueAndNote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w := seedWorld(t)
	a := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	b := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	var tag AnnotationTag
	if err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(c, `INSERT INTO conversations(tenant_id,id) VALUES($1,$2),($1,$3)`, w.tenant, a, b); err != nil {
			return err
		}
		var err error
		tag, err = CreateAnnotationTag(c, tx, w.tenant, "original")
		if err != nil {
			return err
		}
		for _, id := range []uuid.UUID{a, b} {
			if _, err = AnnotationChange(c, tx, w.tenant, id, w.membership, "tag_add", tag.ID, "", ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	proceed := make(chan struct{})
	noteDone := make(chan error, 1)
	go func() {
		noteDone <- testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
			if _, _, err := lockAnnotation(c, tx, w.tenant, b); err != nil {
				return err
			}
			close(locked)
			select {
			case <-proceed:
			case <-c.Done():
				return c.Err()
			}
			_, err := AnnotationChange(c, tx, w.tenant, b, w.membership, "note", uuid.Nil, "privada", "concurrent-note")
			return err
		})
	}()
	<-locked
	pid := make(chan int32, 1)
	catalogueDone := make(chan error, 1)
	go func() {
		catalogueDone <- testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
			var p int32
			if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&p); err != nil {
				return err
			}
			pid <- p
			_, err := ManageAnnotationTag(c, tx, w.tenant, w.membership, tag.ID, "renamed", false)
			return err
		})
	}()
	if err := waitAnnotationBlocked(ctx, <-pid); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-noteDone; err != nil {
		t.Fatal(err)
	}
	if err := <-catalogueDone; err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		for _, tc := range []struct {
			id      uuid.UUID
			version int64
			notes   int
		}{{a, 3, 0}, {b, 4, 1}} {
			s, err := AnnotationRead(c, tx, w.tenant, w.user, tc.id)
			if err != nil {
				return err
			}
			if s.Version != tc.version || len(s.Events) != int(tc.version-1) || len(s.Notes) != tc.notes || len(s.Tags) != 1 || s.Tags[0].Name != "renamed" {
				return fmt.Errorf("inconsistent state: %+v", s)
			}
		}
		var count int
		if err := tx.QueryRow(c, `SELECT count(*) FROM realtime_events WHERE tenant_id=$1 AND event_type='conversation.annotation'`, w.tenant).Scan(&count); err != nil {
			return err
		}
		if count != 5 {
			return fmt.Errorf("realtime events: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAnnotationConcurrentCatalogueAndAssume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w := seedWorld(t)
	a := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	var opened InboundResult
	var tag AnnotationTag
	if err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		var err error
		opened, err = (Repo{}).AcceptInbound(c, tx, InboundInput{TenantID: w.tenant, InboxID: w.inbox, ContactID: w.contact, IdentityID: w.identity, ConnectorID: w.connector, ExternalMessageID: "catalogue-assume", Body: "oi", Period: "202610", TrustedAt: at, OpenedAt: at})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(c, `INSERT INTO conversations(tenant_id,id) VALUES($1,$2)`, w.tenant, a); err != nil {
			return err
		}
		tag, err = CreateAnnotationTag(c, tx, w.tenant, "original")
		if err != nil {
			return err
		}
		for _, id := range []uuid.UUID{a, opened.Conversation.ID} {
			if _, err = AnnotationChange(c, tx, w.tenant, id, w.membership, "tag_add", tag.ID, "", ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	locked, proceed := make(chan struct{}), make(chan struct{})
	operationalDone := make(chan error, 1)
	go func() {
		operationalDone <- testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
			var id uuid.UUID
			if err := tx.QueryRow(c, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, w.tenant, opened.Conversation.ID).Scan(&id); err != nil {
				return err
			}
			close(locked)
			select {
			case <-proceed:
			case <-c.Done():
				return c.Err()
			}
			_, err := (Repo{}).Assume(c, tx, AssumeInput{TenantID: w.tenant, ConversationID: id, MembershipID: w.membership, ExpectedVersion: opened.Conversation.Version, At: at.Add(time.Minute)})
			return err
		})
	}()
	<-locked
	pid := make(chan int32, 1)
	catalogueDone := make(chan error, 1)
	go func() {
		catalogueDone <- testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
			var p int32
			if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&p); err != nil {
				return err
			}
			pid <- p
			_, err := ManageAnnotationTag(c, tx, w.tenant, w.membership, tag.ID, "renamed", false)
			return err
		})
	}()
	if err := waitAnnotationBlocked(ctx, <-pid); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-operationalDone; err != nil {
		t.Fatal(err)
	}
	if err := <-catalogueDone; err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		for _, id := range []uuid.UUID{a, opened.Conversation.ID} {
			s, err := AnnotationRead(c, tx, w.tenant, w.user, id)
			if err != nil {
				return err
			}
			if s.Version != 3 || len(s.Events) != 2 || s.Events[1].Kind != "tag_rename" || s.Events[1].Version != 3 || len(s.Tags) != 1 || s.Tags[0].Name != "renamed" {
				return fmt.Errorf("inconsistent metadata: %+v", s)
			}
		}
		var version int64
		if err := tx.QueryRow(c, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, w.tenant, opened.Conversation.ID).Scan(&version); err != nil {
			return err
		}
		if version != opened.Conversation.Version+1 {
			return fmt.Errorf("operational version: %d", version)
		}
		var annotations, assumes int
		if err := tx.QueryRow(c, `SELECT count(*) FILTER (WHERE event_type='conversation.annotation'), count(*) FILTER (WHERE event_type='conversation.assumed') FROM realtime_events WHERE tenant_id=$1`, w.tenant).Scan(&annotations, &assumes); err != nil {
			return err
		}
		if annotations != 4 || assumes != 1 {
			return fmt.Errorf("realtime annotations=%d assumes=%d", annotations, assumes)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAnnotationReadSerializesMissingAggregate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w := seedWorld(t)
	id := uuid.New()
	if err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(c, `INSERT INTO conversations(tenant_id,id) VALUES($1,$2)`, w.tenant, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pid := make(chan int32, 1)
	done := make(chan error, 1)
	err := testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		// Interleave a writer after the actual version query and before tags.
		wrapped := &annotationReadInterleaveTx{Tx: tx}
		wrapped.beforeTags = func() error {
			go func() {
				done <- testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
					var p int32
					if err := tx.QueryRow(c, `SELECT pg_backend_pid()`).Scan(&p); err != nil {
						return err
					}
					pid <- p
					if _, err := AnnotationChange(c, tx, w.tenant, id, w.membership, "priority", uuid.Nil, "urgent", ""); err != nil {
						return err
					}
					_, err := AnnotationChange(c, tx, w.tenant, id, w.membership, "note", uuid.Nil, "nova", "snapshot-note")
					return err
				})
			}()
			return waitAnnotationBlocked(ctx, <-pid)
		}
		s, err := AnnotationRead(c, wrapped, w.tenant, w.user, id)
		if err != nil {
			return err
		}
		if s.Version != 1 || s.Priority != "normal" || len(s.Events) != 0 || len(s.Notes) != 0 {
			return fmt.Errorf("torn initial state: %+v", s)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = testStore.WithTenantTx(ctx, w.tenant, func(c context.Context, tx pgx.Tx) error {
		s, err := AnnotationRead(c, tx, w.tenant, w.user, id)
		if err != nil {
			return err
		}
		if s.Version != 3 || s.Priority != "urgent" || len(s.Notes) != 1 || len(s.Events) != 2 || s.Events[1].Version != 3 {
			return fmt.Errorf("torn final state: %+v", s)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
