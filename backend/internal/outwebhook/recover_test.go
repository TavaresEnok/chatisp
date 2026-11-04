package outwebhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/platform/urlguard"
)

func TestRecoverSourcesDeliversOnceAfterRiverAppears(t *testing.T) {
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

	tenant, user, otherUser := uuid.New(), uuid.New(), uuid.New()
	member, otherMember := uuid.New(), uuid.New()
	connector, inbox, sector, contact, identity, conv := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `INSERT INTO users(id,email,status) VALUES($1,$2,'active'),($3,$4,'active')`, user, user.String()+"@example.com", otherUser, otherUser.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,'agent','active'),($4,$2,$5,'agent','active')`, member, tenant, user, otherMember, otherUser); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled) VALUES($1,$2,'meta_cloud',$3,'connected',true)`, connector, tenant, connector.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'WhatsApp')`, inbox, tenant, connector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, tenant, sector); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sector_inboxes VALUES($1,$2,$3)`, tenant, sector, inbox); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, contact, tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, identity.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number)
			VALUES($1,$2,$3,$4,$5,$6,'open','menu',$7,'202611',1)`,
			conv, tenant, inbox, sector, contact, identity, "R-"+conv.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3, controller='human' WHERE tenant_id=$1 AND id=$2`, tenant, conv, member); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, conv, otherMember)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, conv, otherMember)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var assigned int
	var created int
	if err = admin.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='conversation.created'),
		       count(*) FILTER (WHERE event_type='conversation.assigned')
		FROM webhook_event_sources WHERE tenant_id=$1`, tenant).Scan(&created, &assigned); err != nil {
		t.Fatal(err)
	}
	if created != 1 || assigned != 2 {
		t.Fatalf("criação %d atribuições %d", created, assigned)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE conversations SET status='closed' WHERE tenant_id=$1 AND id=$2`, tenant, conv); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET status='closed' WHERE tenant_id=$1 AND id=$2`, tenant, conv); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET status='open' WHERE tenant_id=$1 AND id=$2`, tenant, conv); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET status='closed' WHERE tenant_id=$1 AND id=$2`, tenant, conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var closed int
	var distinctAssigned int
	var distinctClosed int
	var foreign int
	if err = admin.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type='conversation.closed'),
		       count(DISTINCT source_id) FILTER (WHERE event_type='conversation.assigned'),
		       count(DISTINCT source_id) FILTER (WHERE event_type='conversation.closed'),
		       count(*) FILTER (WHERE conversation_id <> $2 OR (source_id = $2 AND event_type IN ('conversation.assigned','conversation.closed')))
		FROM webhook_event_sources WHERE tenant_id=$1`, tenant, conv).Scan(&closed, &distinctAssigned, &distinctClosed, &foreign); err != nil {
		t.Fatal(err)
	}
	if closed != 2 || distinctAssigned != 2 || distinctClosed != 2 || foreign != 0 {
		t.Fatalf("encerramentos %d atribuições distintas %d encerramentos distintos %d fora da conversa %d", closed, distinctAssigned, distinctClosed, foreign)
	}
	var sources int
	var unmarked int
	if err = admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE enqueued_at IS NULL) FROM webhook_event_sources WHERE tenant_id=$1`, tenant).Scan(&sources, &unmarked); err != nil {
		t.Fatal(err)
	}
	if sources != 5 || unmarked != 5 {
		t.Fatalf("origens %d sem job %d", sources, unmarked)
	}
	var river bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='river')`).Scan(&river); err != nil {
		t.Fatal(err)
	}
	if river {
		t.Fatal("o teste precisa capturar antes do schema River")
	}
	n, err := RecoverSources(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("recuperação sem River devolveu %d", n)
	}

	jobsPassword, err := randomHex(24)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureJobsRole(ctx, env.AdminURL, jobsPassword); err != nil {
		t.Fatal(err)
	}
	if err = jobs.Migrate(ctx, pools.Owner); err != nil {
		t.Fatal(err)
	}
	n, err = RecoverSources(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("recuperou %d", n)
	}
	again, err := RecoverSources(ctx, store)
	if err != nil || again != 0 {
		t.Fatalf("segunda recuperação %d %v", again, err)
	}
	var jobsN int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE kind=$1`, JobKind).Scan(&jobsN); err != nil {
		t.Fatal(err)
	}
	if jobsN != 5 {
		t.Fatalf("jobs %d", jobsN)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT public.webhook_enqueue($1,'conversation.created',$2)`, tenant, conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM river.river_job WHERE kind=$1`, JobKind).Scan(&jobsN); err != nil {
		t.Fatal(err)
	}
	if jobsN != 5 {
		t.Fatalf("enqueue repetido criou job extra: %d", jobsN)
	}
}

func TestConcurrentDeliveryPostsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	tenant := uuid.New()
	admin, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, `INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, 32)
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(key), "v1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	rawSecret := make([]byte, 32)
	if _, err = rand.Read(rawSecret); err != nil {
		t.Fatal(err)
	}
	sub := Subscription{
		ID: uuid.New(), TenantID: tenant, URL: "http://127.0.0.1/hook", Secret: rawSecret, Enabled: true,
		Events: map[string]bool{EventMessageCreated: true},
	}
	stored, err := SealSubscription(ring, sub)
	if err != nil {
		t.Fatal(err)
	}
	source := uuid.New()
	now := time.Now().UTC()
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := InsertSubscription(ctx, tx, stored); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at, enqueued_at)
			VALUES ($1,$2,$3,$3,$4,$4)`, tenant, EventMessageCreated, source, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	poster := PosterFunc(func(context.Context, string, []byte, http.Header) (int, error) {
		posts.Add(1)
		once.Do(func() { close(started) })
		<-release
		return http.StatusNoContent, nil
	})
	deps := Deps{Store: store, Ring: ring, Policy: policy, Mode: "test", Poster: poster, Now: func() time.Time { return now.Add(time.Second) }}
	errCh := make(chan error, 2)
	go func() { errCh <- DeliverSource(ctx, deps, tenant, EventMessageCreated, source) }()
	go func() { errCh <- DeliverSource(ctx, deps, tenant, EventMessageCreated, source) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhuma entrega começou")
	}
	time.Sleep(150 * time.Millisecond)
	if posts.Load() != 1 {
		t.Fatalf("posts concorrentes %d", posts.Load())
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			var retry ErrRetryAfter
			if !errorsAsRetry(err, &retry) && err != nil {
				t.Fatal(err)
			}
		}
	}
	if posts.Load() != 1 {
		t.Fatalf("posts após liberar %d", posts.Load())
	}
	if err = DeliverSource(ctx, deps, tenant, EventMessageCreated, source); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("sucesso reenviou %d", posts.Load())
	}
	var status string
	var body []byte
	var id uuid.UUID
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, status, body FROM webhook_deliveries WHERE tenant_id=$1`, tenant).Scan(&id, &status, &body)
	}); err != nil {
		t.Fatal(err)
	}
	if status != StatusDelivered {
		t.Fatalf("status %s", status)
	}
	stale := Delivery{ID: id, TenantID: tenant, Body: body, Status: StatusPending, NextAt: now, Attempt: 1}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return UpdateDelivery(ctx, tx, stale)
	}); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM webhook_deliveries WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&after)
	}); err != nil {
		t.Fatal(err)
	}
	if after != StatusDelivered {
		t.Fatalf("delivered regrediu para %s", after)
	}

	lateSource := uuid.New()
	base := time.Now().UTC().Truncate(time.Second)
	var shift atomic.Int64
	var later atomic.Int32
	lateStarted := make(chan struct{})
	lateRelease := make(chan struct{})
	var lateOnce sync.Once
	latePoster := PosterFunc(func(context.Context, string, []byte, http.Header) (int, error) {
		if shift.Load() == 0 {
			shift.Store(int64(claimLease + time.Second))
			return http.StatusNoContent, nil
		}
		if later.Add(1) == 1 {
			lateOnce.Do(func() { close(lateStarted) })
		}
		<-lateRelease
		return http.StatusNoContent, nil
	})
	one := Subscription{
		ID: uuid.New(), TenantID: tenant, URL: "http://127.0.0.1/one", Secret: rawSecret, Enabled: true,
		Events: map[string]bool{EventMessageCreated: true},
	}
	two := Subscription{
		ID: uuid.New(), TenantID: tenant, URL: "http://127.0.0.1/two", Secret: rawSecret, Enabled: true,
		Events: map[string]bool{EventMessageCreated: true},
	}
	storedOne, err := SealSubscription(ring, one)
	if err != nil {
		t.Fatal(err)
	}
	storedTwo, err := SealSubscription(ring, two)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := SetEnabled(ctx, tx, tenant, sub.ID, false); err != nil {
			return err
		}
		if err := InsertSubscription(ctx, tx, storedOne); err != nil {
			return err
		}
		if err := InsertSubscription(ctx, tx, storedTwo); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at, enqueued_at)
			VALUES ($1,$2,$3,$3,$4,$4)`, tenant, EventMessageCreated, lateSource, base)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lateDeps := Deps{
		Store: store, Ring: ring, Policy: policy, Mode: "test", Poster: latePoster,
		Now: func() time.Time { return base.Add(time.Duration(shift.Load())) },
	}
	errChLate := make(chan error, 2)
	go func() { errChLate <- DeliverSource(ctx, lateDeps, tenant, EventMessageCreated, lateSource) }()
	select {
	case <-lateStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("a segunda assinatura não foi chamada")
	}
	go func() { errChLate <- DeliverSource(ctx, lateDeps, tenant, EventMessageCreated, lateSource) }()
	time.Sleep(200 * time.Millisecond)
	if later.Load() != 1 {
		t.Fatalf("posts posteriores %d", later.Load())
	}
	close(lateRelease)
	for i := 0; i < 2; i++ {
		if err := <-errChLate; err != nil {
			var retry ErrRetryAfter
			if !errorsAsRetry(err, &retry) {
				t.Fatal(err)
			}
		}
	}
	if later.Load() != 1 {
		t.Fatalf("posts posteriores após liberar %d", later.Load())
	}
}

func errorsAsRetry(err error, target *ErrRetryAfter) bool {
	if err == nil {
		return false
	}
	retry, ok := err.(ErrRetryAfter)
	if !ok {
		return false
	}
	*target = retry
	return true
}
