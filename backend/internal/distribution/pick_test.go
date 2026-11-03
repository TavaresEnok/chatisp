package distribution_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"chatisp/internal/distribution"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/presence"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPickRejectsBlank(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	if _, err := distribution.Pick(ctx, nil, id, id, id, now); !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("pick %v", err)
	}
	if _, err := distribution.Queue(ctx, nil, id, id, id, now); !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("queue %v", err)
	}
	if _, err := distribution.PutWaitAlert(ctx, nil, id, 0, -1); !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("limiar %v", err)
	}
	if _, err := distribution.PutWaitAlert(ctx, nil, id, 0, distribution.MaxWaitAlertSeconds+1); !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("teto %v", err)
	}
	if _, err := distribution.PutSectorAuto(ctx, nil, id, id, 0, false); !errors.Is(err, distribution.ErrInvalid) {
		t.Fatalf("setor %v", err)
	}
}

func TestDistributionPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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

	f := &fix{t: t, ctx: ctx, admin: admin, store: store, pools: pools, tenant: uuid.New()}
	f.exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, f.tenant)
	f.clock = time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	f.svc = presence.New(store)
	f.svc.Now = func() time.Time { return f.clock }

	t.Run("round-robin", func(t *testing.T) {
		f.t = t
		box := f.box("Justa")
		ids := f.members(2)
		first := f.actor(ids[0], "agent")
		second := f.actor(ids[1], "agent")
		f.join(box.sector, first.member)
		f.join(box.sector, second.member)
		f.online(first)
		f.online(second)
		older := f.conv(box, "open", "", f.clock.Add(-3*time.Minute), nil)
		middle := f.conv(box, "open", "", f.clock.Add(-2*time.Minute), nil)
		newest := f.conv(box, "waiting_customer", "", f.clock.Add(-time.Minute), nil)
		want := []struct {
			conv   uuid.UUID
			member uuid.UUID
		}{{older, first.member}, {middle, second.member}, {newest, first.member}}
		for i, item := range want {
			got := f.pick(box.sector, box.inbox)
			if got.Status != distribution.StatusAssigned || got.Reason != distribution.ReasonAssigned || got.ConversationID != item.conv || got.MembershipID != item.member {
				t.Fatalf("volta %d %+v", i, got)
			}
			if f.assignee(item.conv) != item.member {
				t.Fatalf("responsável %d %s", i, f.assignee(item.conv))
			}
		}
		if f.cursor(box.sector) != first.member {
			t.Fatalf("cursor %s", f.cursor(box.sector))
		}
		var status, controller, kind string
		if err := admin.QueryRow(ctx, `SELECT c.status, c.controller, e.kind FROM conversations c JOIN conversation_events e ON e.tenant_id=c.tenant_id AND e.conversation_id=c.id WHERE c.tenant_id=$1 AND c.id=$2 AND e.kind LIKE 'distributed:%'`, f.tenant, newest).Scan(&status, &controller, &kind); err != nil {
			t.Fatal(err)
		}
		wantKind := "distributed:" + box.sector.String() + ":" + box.sector.String() + ":00000000-0000-0000-0000-000000000000:" + first.member.String()
		if status != "waiting_customer" || controller != "human" || kind != wantKind {
			t.Fatalf("histórico %s %s %s", status, controller, kind)
		}
	})

	t.Run("skip busy and full", func(t *testing.T) {
		f.t = t
		box := f.box("Salto")
		ids := f.members(3)
		busy := f.actor(ids[0], "agent")
		full := f.actor(ids[1], "agent")
		free := f.actor(ids[2], "agent")
		f.join(box.sector, busy.member)
		f.join(box.sector, full.member)
		f.join(box.sector, free.member)
		f.online(busy)
		f.online(full)
		f.online(free)
		if _, err := f.svc.SetManual(ctx, busy.p, presence.ManualCommand{RequestID: uuid.New(), State: presence.ManualBusy}); err != nil {
			t.Fatal(err)
		}
		held := f.conv(box, "open", "", f.clock.Add(-time.Hour), &full.member)
		f.exec(`INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, f.tenant, full.member)
		waiting := f.conv(box, "open", "", f.clock.Add(-time.Minute), nil)
		got := f.pick(box.sector, box.inbox)
		if got.Status != distribution.StatusAssigned || got.MembershipID != free.member || got.ConversationID != waiting {
			t.Fatalf("salto %+v", got)
		}
		if f.assignee(held) != full.member || f.cursor(box.sector) != free.member {
			t.Fatal("cursor ou vaga presa")
		}
	})

	t.Run("sector boundary", func(t *testing.T) {
		f.t = t
		box := f.box("Origem")
		other := uuid.New()
		f.exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Outro')`, f.tenant, other)
		f.exec(`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, f.tenant, other, box.inbox)
		ids := f.members(2)
		outsider := f.actor(ids[0], "agent")
		insider := f.actor(ids[1], "agent")
		f.join(other, outsider.member)
		f.join(box.sector, insider.member)
		f.online(outsider)
		f.online(insider)
		waiting := f.conv(box, "open", "", f.clock, nil)
		got := f.pick(box.sector, box.inbox)
		if got.MembershipID != insider.member || got.ConversationID != waiting || f.assignee(waiting) != insider.member {
			t.Fatalf("união %+v", got)
		}
	})

	t.Run("queue order and alert", func(t *testing.T) {
		f.t = t
		box := f.box("Fila")
		urgent := f.conv(box, "open", "urgent", f.clock.Add(-time.Hour), nil)
		normalOld := f.conv(box, "open", "", f.clock.Add(-900*time.Second), nil)
		normalNew := f.conv(box, "open", "", f.clock.Add(-899*time.Second), nil)
		triage := f.conv(box, "triage", "", f.clock.Add(-2*time.Hour), nil)
		closed := f.conv(box, "closed", "", f.clock.Add(-2*time.Hour), nil)
		view := f.queue(box.sector, box.inbox)
		if view.Reason != distribution.ReasonReady || !view.AutoAssign || view.Settings.Configured || view.Settings.WaitAlertSeconds != distribution.DefaultWaitAlertSeconds || len(view.Items) != 3 {
			t.Fatalf("fila %+v", view)
		}
		if view.Items[0].ConversationID != urgent || view.Items[0].Priority != "urgent" || view.Items[0].Alert {
			t.Fatalf("urgente %+v", view.Items[0])
		}
		if view.Items[1].ConversationID != normalOld || !view.Items[1].Alert || view.Items[1].Priority != "normal" {
			t.Fatalf("normal antigo %+v", view.Items[1])
		}
		if view.Items[2].ConversationID != normalNew || view.Items[2].Alert {
			t.Fatalf("normal recente %+v", view.Items[2])
		}
		for _, hidden := range []uuid.UUID{triage, closed} {
			for _, item := range view.Items {
				if item.ConversationID == hidden {
					t.Fatalf("fora da fila %s", hidden)
				}
			}
		}
		var settings distribution.Settings
		err := store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := distribution.PutWaitAlert(ctx, tx, f.tenant, 0, -1); !errors.Is(err, distribution.ErrInvalid) {
				return fmt.Errorf("negativo %w", err)
			}
			var err error
			settings, err = distribution.PutWaitAlert(ctx, tx, f.tenant, 0, 1000)
			return err
		})
		if err != nil || settings.Version != 1 || settings.WaitAlertSeconds != 1000 {
			t.Fatalf("gravação %+v %v", settings, err)
		}
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := distribution.PutWaitAlert(ctx, tx, f.tenant, 0, 600)
			return err
		})
		if !errors.Is(err, distribution.ErrConflict) {
			t.Fatalf("versão %v", err)
		}
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			settings, err = distribution.PutWaitAlert(ctx, tx, f.tenant, 1, 600)
			return err
		})
		if err != nil || settings.Version != 2 || settings.WaitAlertSeconds != 600 {
			t.Fatalf("troca %+v %v", settings, err)
		}
		view = f.queue(box.sector, box.inbox)
		if !view.Items[1].Alert || !view.Items[2].Alert || view.Items[0].Alert {
			t.Fatalf("limiar 600 %+v", view.Items)
		}
	})

	t.Run("nobody and empty", func(t *testing.T) {
		f.t = t
		box := f.box("Vazio")
		quiet := f.actor(uuid.Nil, "agent")
		f.join(box.sector, quiet.member)
		waiting := f.conv(box, "open", "", f.clock, nil)
		got := f.pick(box.sector, box.inbox)
		if got.Status != distribution.StatusQueued || got.Reason != distribution.ReasonNobodyAvailable || got.ConversationID != waiting || f.assignee(waiting) != uuid.Nil {
			t.Fatalf("ninguém %+v", got)
		}
		if f.cursor(box.sector) != uuid.Nil {
			t.Fatal("cursor andou sem reserva")
		}
		alone := f.box("Sem fila")
		ready := f.actor(uuid.Nil, "agent")
		f.join(alone.sector, ready.member)
		f.online(ready)
		got = f.pick(alone.sector, alone.inbox)
		if got.Status != distribution.StatusQueued || got.Reason != distribution.ReasonEmpty || got.ConversationID != uuid.Nil {
			t.Fatalf("vazia %+v", got)
		}
		idleSector := uuid.New()
		f.exec(`INSERT INTO sectors(tenant_id,id,name,active) VALUES($1,$2,'Inativo',false)`, f.tenant, idleSector)
		got = f.pick(idleSector, alone.inbox)
		if got.Reason != distribution.ReasonSectorInactive {
			t.Fatalf("inativo %+v", got)
		}
		got = f.pick(alone.sector, uuid.New())
		if got.Reason != distribution.ReasonInboxUnlinked {
			t.Fatalf("sem caixa %+v", got)
		}
	})

	t.Run("last slot", func(t *testing.T) {
		f.t = t
		// Distinct sector cursors must not serialize the pair. The same attendant
		// has one vacancy, and the winner keeps the transaction open until the
		// other session is waiting on that capacity advisory.
		left := f.box("Ultima A")
		right := f.box("Ultima B")
		agent := f.actor(uuid.Nil, "agent")
		f.join(left.sector, agent.member)
		f.join(right.sector, agent.member)
		f.online(agent)
		f.exec(`INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, f.tenant, agent.member)
		leftConv := f.conv(left, "open", "", f.clock.Add(-2*time.Minute), nil)
		rightConv := f.conv(right, "open", "", f.clock.Add(-time.Minute), nil)
		sectors := map[uuid.UUID]uuid.UUID{leftConv: left.sector, rightConv: right.sector}
		type outcome struct {
			res distribution.Result
			err error
		}
		start := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		stop := func() { once.Do(func() { close(release) }) }
		done := make(chan outcome, 2)
		var wg sync.WaitGroup
		for _, item := range []box{left, right} {
			wg.Add(1)
			go func(sector, inbox uuid.UUID) {
				defer wg.Done()
				<-start
				var res distribution.Result
				err := store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					res, err = distribution.Pick(ctx, tx, f.tenant, sector, inbox, f.clock)
					select {
					case <-release:
						return err
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				done <- outcome{res, err}
			}(item.sector, item.inbox)
		}
		defer func() {
			stop()
			wg.Wait()
		}()
		close(start)
		f.waitCapacityOverlap()
		stop()
		assigned, queued := 0, 0
		var assignedConv, queuedConv uuid.UUID
		for i := 0; i < 2; i++ {
			item := <-done
			if item.err != nil {
				t.Fatal(item.err)
			}
			switch item.res.Reason {
			case distribution.ReasonAssigned:
				assigned++
				assignedConv = item.res.ConversationID
				if item.res.Status != distribution.StatusAssigned || item.res.MembershipID != agent.member {
					t.Fatalf("vencedor %+v", item.res)
				}
			case distribution.ReasonCapacityFull:
				queued++
				queuedConv = item.res.ConversationID
				if item.res.Status != distribution.StatusQueued {
					t.Fatalf("fila %+v", item.res)
				}
			default:
				t.Fatalf("motivo %+v", item.res)
			}
		}
		if assigned != 1 || queued != 1 {
			t.Fatalf("aceitas %d fila %d", assigned, queued)
		}
		if sectors[assignedConv] == uuid.Nil || sectors[queuedConv] == uuid.Nil || assignedConv == queuedConv {
			t.Fatalf("conversas atribuída %s fila %s", assignedConv, queuedConv)
		}
		if f.assignee(assignedConv) != agent.member || f.assignee(queuedConv) != uuid.Nil {
			t.Fatal("as duas saíram da fila ou nenhuma foi reservada")
		}
		if f.cursor(sectors[assignedConv]) != agent.member || f.cursor(sectors[queuedConv]) != uuid.Nil {
			t.Fatal("cursor andou sem reserva ou ficou parado no setor atribuído")
		}
		var openQueued, closedAssigned int
		if err := admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE conversation_id=$2 AND ended_at IS NULL), count(*) FILTER (WHERE conversation_id=$3 AND ended_at IS NOT NULL) FROM assignment_queue_intervals WHERE tenant_id=$1`, f.tenant, queuedConv, assignedConv).Scan(&openQueued, &closedAssigned); err != nil {
			t.Fatal(err)
		}
		if openQueued != 1 || closedAssigned != 1 {
			t.Fatalf("intervalos aberta %d encerrada %d", openQueued, closedAssigned)
		}
	})

	t.Run("sector auto", func(t *testing.T) {
		f.t = t
		off := f.box("Manual")
		on := f.box("Auto")
		agent := f.actor(uuid.Nil, "agent")
		f.join(off.sector, agent.member)
		f.join(on.sector, agent.member)
		f.online(agent)
		waitingOff := f.conv(off, "open", "", f.clock.Add(-time.Minute), nil)
		waitingOn := f.conv(on, "open", "", f.clock.Add(-time.Minute), nil)
		var mode distribution.SectorAuto
		err := store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			mode, err = distribution.PutSectorAuto(ctx, tx, f.tenant, off.sector, 0, false)
			return err
		})
		if err != nil || !mode.Configured || mode.Enabled || mode.Version != 1 {
			t.Fatalf("desliga %+v %v", mode, err)
		}
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := distribution.PutSectorAuto(ctx, tx, f.tenant, uuid.New(), 0, false)
			return err
		})
		if !errors.Is(err, distribution.ErrInvalid) {
			t.Fatalf("setor ausente %v", err)
		}
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := distribution.PutSectorAuto(ctx, tx, f.tenant, off.sector, 0, true)
			return err
		})
		if !errors.Is(err, distribution.ErrConflict) {
			t.Fatalf("versão %v", err)
		}
		got := f.pick(off.sector, off.inbox)
		if got.Status != distribution.StatusQueued || got.Reason != distribution.ReasonAutoDisabled || got.ConversationID != uuid.Nil || f.assignee(waitingOff) != uuid.Nil {
			t.Fatalf("desligado %+v", got)
		}
		var cursors int
		if err = admin.QueryRow(ctx, `SELECT count(*) FROM distribution_cursors WHERE tenant_id=$1 AND sector_id=$2`, f.tenant, off.sector).Scan(&cursors); err != nil || cursors != 0 {
			t.Fatalf("cursor %d %v", cursors, err)
		}
		view := f.queue(off.sector, off.inbox)
		if view.Reason != distribution.ReasonReady || view.AutoAssign || len(view.Items) != 1 || view.Items[0].ConversationID != waitingOff {
			t.Fatalf("fila preservada %+v", view)
		}
		got = f.pick(on.sector, on.inbox)
		if got.Status != distribution.StatusAssigned || got.ConversationID != waitingOn || got.MembershipID != agent.member || f.assignee(waitingOff) != uuid.Nil {
			t.Fatalf("vizinho %+v", got)
		}
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			mode, err = distribution.PutSectorAuto(ctx, tx, f.tenant, off.sector, 1, true)
			return err
		})
		if err != nil || !mode.Enabled || mode.Version != 2 {
			t.Fatalf("liga %+v %v", mode, err)
		}
		got = f.pick(off.sector, off.inbox)
		if got.Status != distribution.StatusAssigned || got.ConversationID != waitingOff || got.MembershipID != agent.member {
			t.Fatalf("religado %+v", got)
		}
	})

	t.Run("priority waits for the conversation lock", func(t *testing.T) {
		f.t = t
		f.racePriority("promove a mais nova", false, "urgent")
		f.racePriority("rebaixa a urgente", true, "normal")
	})

	t.Run("scope", func(t *testing.T) {
		f.t = t
		for _, table := range []string{"distribution_cursors", "distribution_settings", "distribution_sector_auto"} {
			var forced bool
			if err := admin.QueryRow(ctx, `SELECT c.relrowsecurity AND c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, table).Scan(&forced); err != nil || !forced {
				t.Fatalf("rls %s %v %v", table, forced, err)
			}
			var n int
			if err := pools.App.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
				t.Fatalf("sem escopo %s %d %v", table, n, err)
			}
			err := store.WithTenantTx(ctx, uuid.New(), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
			})
			if err != nil || n != 0 {
				t.Fatalf("outro tenant %s %d %v", table, n, err)
			}
		}
		err := store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := distribution.Pick(ctx, tx, uuid.New(), uuid.New(), uuid.New(), f.clock)
			return err
		})
		if !errors.Is(err, distribution.ErrForbidden) {
			t.Fatalf("escopo %v", err)
		}
		f.exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant)
		err = store.WithTenantTx(ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := distribution.Pick(ctx, tx, f.tenant, uuid.New(), uuid.New(), f.clock)
			return err
		})
		if !errors.Is(err, sectors.ErrForbidden) {
			t.Fatalf("suspenso %v", err)
		}
		f.exec(`UPDATE tenants SET status='active' WHERE id=$1`, f.tenant)
	})
}

type fix struct {
	t      *testing.T
	ctx    context.Context
	admin  *pgx.Conn
	store  *db.Store
	pools  *db.Pools
	tenant uuid.UUID
	clock  time.Time
	svc    presence.Service
	n      int
	seq    int
}

type actor struct {
	user, member, session uuid.UUID
	p                     presence.Principal
}

type box struct {
	sector, inbox, connector uuid.UUID
}

func (f *fix) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.admin.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) members(n int) []uuid.UUID {
	f.t.Helper()
	out := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		f.seq++
		out[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012x", f.seq))
	}
	return out
}

func (f *fix) actor(member uuid.UUID, role string) actor {
	f.t.Helper()
	if member == uuid.Nil {
		member = uuid.New()
	}
	a := actor{user: uuid.New(), member: member, session: uuid.New()}
	f.exec(`INSERT INTO users(id,email,status) VALUES($1,$2,'active')`, a.user, a.user.String()+"@example.test")
	f.exec(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,$4,'active')`, a.member, f.tenant, a.user, role)
	a.p = presence.Principal{SessionID: a.session, UserID: a.user, TenantID: f.tenant, MembershipID: a.member, Role: role}
	far := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := f.store.WithAuth(f.ctx, func(ctx context.Context) error {
		return f.store.InsertSession(ctx, db.NewSession{ID: a.session, TokenHash: auth.HashSecret(uuid.NewString()), UserID: a.user, TenantID: f.tenant, MembershipID: a.member, CSRFHash: auth.HashSecret(uuid.NewString()), Now: f.clock, IdleExpiresAt: far, AbsoluteExpiresAt: far})
	}); err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f *fix) online(a actor) {
	f.t.Helper()
	if _, err := f.svc.Heartbeat(f.ctx, a.p, uuid.New()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) box(name string) box {
	f.t.Helper()
	b := box{sector: uuid.New(), inbox: uuid.New(), connector: uuid.New()}
	f.exec(`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, b.connector, f.tenant, b.connector.String())
	f.exec(`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,$4)`, b.inbox, f.tenant, b.connector, name)
	f.exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,$3)`, f.tenant, b.sector, name)
	f.exec(`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, f.tenant, b.sector, b.inbox)
	return b
}

func (f *fix) join(sector, member uuid.UUID) {
	f.t.Helper()
	f.exec(`INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3)`, f.tenant, sector, member)
}

func (f *fix) conv(b box, status, priority string, at time.Time, assignee *uuid.UUID) uuid.UUID {
	f.t.Helper()
	f.n++
	id, contact, identity := uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'C')`, contact, f.tenant)
	f.exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, f.tenant, contact, b.connector, identity.String())
	protocol := fmt.Sprintf("P-%d", f.n)
	if assignee == nil {
		f.exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'202611',1,$9,$9)`, id, f.tenant, b.inbox, b.sector, contact, identity, status, protocol, at)
	} else {
		f.exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'human',$8,$9,'202611',1,$10,$10)`, id, f.tenant, b.inbox, b.sector, contact, identity, status, *assignee, protocol, at)
	}
	if priority == "urgent" {
		f.exec(`INSERT INTO conversation_annotations(tenant_id,conversation_id,priority) VALUES($1,$2,'urgent')`, f.tenant, id)
	}
	return id
}

func (f *fix) racePriority(name string, laterStartsUrgent bool, next string) {
	f.t.Helper()
	box := f.box(name)
	agent := f.actor(uuid.Nil, "agent")
	f.join(box.sector, agent.member)
	f.online(agent)
	earlier := f.conv(box, "open", "", f.clock.Add(-2*time.Minute), nil)
	laterPriority := ""
	if laterStartsUrgent {
		laterPriority = "urgent"
	}
	later := f.conv(box, "open", laterPriority, f.clock.Add(-time.Minute), nil)
	want := later
	if next == "normal" {
		want = earlier
	}
	var got distribution.Result
	var pickErr error
	f.duringShare(later, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO conversation_annotations(tenant_id,conversation_id,priority) VALUES($1,$2,$3) ON CONFLICT(tenant_id,conversation_id) DO UPDATE SET priority=EXCLUDED.priority`, f.tenant, later, next)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("prioridade %d", tag.RowsAffected())
		}
		return nil
	}, func() {
		pickErr = f.store.WithTenantTx(f.ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = distribution.Pick(ctx, tx, f.tenant, box.sector, box.inbox, f.clock)
			return err
		})
	})
	if pickErr != nil {
		f.t.Fatal(pickErr)
	}
	if got.Status != distribution.StatusAssigned || got.ConversationID != want || got.MembershipID != agent.member || got.Priority != next {
		f.t.Fatalf("%s %+v", name, got)
	}
	if f.assignee(want) != agent.member || f.assignee(otherOf(earlier, later, want)) != uuid.Nil {
		f.t.Fatalf("%s responsável", name)
	}
}

func otherOf(a, b, chosen uuid.UUID) uuid.UUID {
	if chosen == a {
		return b
	}
	return a
}

func (f *fix) duringShare(hold uuid.UUID, mutate func(context.Context, pgx.Tx) error, blocked func()) {
	f.t.Helper()
	release := make(chan struct{})
	locked := make(chan struct{})
	errCh := make(chan error, 1)
	var once sync.Once
	stop := func() { once.Do(func() { close(release) }) }
	go func() {
		tx, err := f.admin.Begin(f.ctx)
		if err != nil {
			errCh <- err
			return
		}
		defer tx.Rollback(f.ctx)
		if _, err = tx.Exec(f.ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR SHARE`, f.tenant, hold); err != nil {
			errCh <- err
			return
		}
		close(locked)
		select {
		case <-release:
		case <-f.ctx.Done():
			errCh <- f.ctx.Err()
			return
		}
		if err = mutate(f.ctx, tx); err != nil {
			errCh <- err
			return
		}
		errCh <- tx.Commit(f.ctx)
	}()
	select {
	case <-locked:
	case err := <-errCh:
		f.t.Fatal(err)
	case <-f.ctx.Done():
		f.t.Fatal(f.ctx.Err())
	}
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		blocked()
	}()
	f.waitConversationLock()
	stop()
	if err := <-errCh; err != nil {
		f.t.Fatal(err)
	}
	select {
	case <-done:
	case <-f.ctx.Done():
		f.t.Fatal(f.ctx.Err())
	}
}

func (f *fix) waitCapacityOverlap() {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	pattern := "%pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:%"
	for time.Now().Before(deadline) {
		var n int
		if err := f.admin.QueryRow(f.ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1`, pattern).Scan(&n); err != nil {
			f.t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	f.t.Fatal("a disputa não esperou o advisory de capacidade")
}

func (f *fix) waitConversationLock() {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		// A FOR UPDATE behind FOR SHARE waits on the holder's transactionid, not on an ungranted tuple lock.
		if err := f.pools.App.QueryRow(f.ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype='transactionid'`).Scan(&n); err != nil {
			f.t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	f.t.Fatal("Pick não esperou o lock da conversa")
}

func (f *fix) pick(sector, inbox uuid.UUID) distribution.Result {
	f.t.Helper()
	var res distribution.Result
	err := f.store.WithTenantTx(f.ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = distribution.Pick(ctx, tx, f.tenant, sector, inbox, f.clock)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fix) queue(sector, inbox uuid.UUID) distribution.QueueView {
	f.t.Helper()
	var view distribution.QueueView
	err := f.store.WithTenantTx(f.ctx, f.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = distribution.Queue(ctx, tx, f.tenant, sector, inbox, f.clock)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return view
}

func (f *fix) assignee(id uuid.UUID) uuid.UUID {
	f.t.Helper()
	var raw *uuid.UUID
	if err := f.admin.QueryRow(f.ctx, `SELECT assignee_id FROM conversations WHERE tenant_id=$1 AND id=$2`, f.tenant, id).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	if raw == nil {
		return uuid.Nil
	}
	return *raw
}

func (f *fix) cursor(sector uuid.UUID) uuid.UUID {
	f.t.Helper()
	var missing bool
	var id uuid.UUID
	if err := f.admin.QueryRow(f.ctx, `SELECT last_membership_id IS NULL, COALESCE(last_membership_id, '00000000-0000-0000-0000-000000000000'::uuid) FROM distribution_cursors WHERE tenant_id=$1 AND sector_id=$2`, f.tenant, sector).Scan(&missing, &id); err != nil {
		f.t.Fatal(err)
	}
	if missing {
		return uuid.Nil
	}
	return id
}
