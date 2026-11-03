package lifecycle_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	attendancestore "chatisp/internal/attendance/store"
	"chatisp/internal/distribution"
	"chatisp/internal/lifecycle"
	lifecycleapi "chatisp/internal/lifecycle/httpapi"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/presence"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLifecyclePostgres(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, other := uuid.New(), uuid.New()
	exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active'),($2,'UTC','active')`, tenant, other)
	clock := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	var seq int
	type actor struct {
		user, member, session uuid.UUID
		token, csrf           string
		p                     lifecycle.Principal
	}
	makeActor := func(tenantID uuid.UUID, role, email string, totp bool) actor {
		t.Helper()
		seq++
		a := actor{user: uuid.New(), member: uuid.New(), session: uuid.New(), token: uuid.NewString(), csrf: uuid.NewString()}
		if totp {
			exec(`INSERT INTO users(id,email,status,totp_ciphertext,totp_key_version,totp_aad_tenant,totp_confirmed_at) VALUES($1,$2,'active',$3,'v1',$4,$5)`, a.user, email, bytes.Repeat([]byte{7}, 32), tenantID, clock)
		} else {
			exec(`INSERT INTO users(id,email,status) VALUES($1,$2,'active')`, a.user, email)
		}
		exec(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,$4,'active')`, a.member, tenantID, a.user, role)
		a.p = lifecycle.Principal{UserID: a.user, TenantID: tenantID, MembershipID: a.member, Role: role}
		far := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := store.WithAuth(ctx, func(ctx context.Context) error {
			return store.InsertSession(ctx, db.NewSession{ID: a.session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: tenantID, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: clock, IdleExpiresAt: far, AbsoluteExpiresAt: far})
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	adminA := makeActor(tenant, "admin", "admin-life@example.test", true)
	agent := makeActor(tenant, "agent", "agent-life@example.test", false)
	otherAgent := makeActor(tenant, "agent", "other-life@example.test", false)
	outsider := makeActor(tenant, "agent", "outsider-life@example.test", false)
	foreign := makeActor(other, "agent", "foreign-life@example.test", false)
	_ = foreign
	type box struct{ sector, inbox, connector uuid.UUID }
	makeBox := func(name string) box {
		t.Helper()
		b := box{sector: uuid.New(), inbox: uuid.New(), connector: uuid.New()}
		exec(`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, b.connector, tenant, b.connector.String())
		exec(`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,$4)`, b.inbox, tenant, b.connector, name)
		exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,$3)`, tenant, b.sector, name)
		exec(`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, tenant, b.sector, b.inbox)
		return b
	}
	home := makeBox("Suporte")
	away := makeBox("Financeiro")
	exec(`INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3),($1,$2,$4),($1,$5,$6)`, tenant, home.sector, agent.member, otherAgent.member, away.sector, outsider.member)
	var n int
	openAssigned := func(b box, member uuid.UUID, status string) uuid.UUID {
		t.Helper()
		n++
		id, contact, identity := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'C')`, contact, tenant)
		exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, b.connector, identity.String())
		exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'human',$8,$9,'202611',1,$10,$10)`, id, tenant, b.inbox, b.sector, contact, identity, status, member, fmt.Sprintf("P-%d", n), clock)
		exec(`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,$4)`, tenant, uuid.New(), id, clock)
		return id
	}
	waiting := func(b box, member uuid.UUID, reply time.Time) uuid.UUID {
		t.Helper()
		n++
		id, contact, identity := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'C')`, contact, tenant)
		exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, b.connector, identity.String())
		exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,last_customer_message_at,last_eligible_human_reply_at,waiting_since,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'waiting_customer','human',$7,$8,'202611',1,$9,$10,$10,$11,$11)`, id, tenant, b.inbox, b.sector, contact, identity, member, fmt.Sprintf("W-%d", n), reply.Add(-time.Minute), reply, reply)
		exec(`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,$4)`, tenant, uuid.New(), id, reply)
		return id
	}
	openQueue := func(b box) uuid.UUID {
		t.Helper()
		n++
		id, contact, identity := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'C')`, contact, tenant)
		exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, b.connector, identity.String())
		exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'open',$7,'202611',1,$8,$8)`, id, tenant, b.inbox, b.sector, contact, identity, fmt.Sprintf("Q-%d", n), clock)
		exec(`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,$4)`, tenant, uuid.New(), id, clock)
		return id
	}
	versionOf := func(id uuid.UUID) int64 {
		t.Helper()
		var version int64
		if err := admin.QueryRow(ctx, `SELECT version FROM conversations WHERE id=$1`, id).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	stateOf := func(id uuid.UUID) (status, controller string, assignee uuid.UUID) {
		t.Helper()
		var member *uuid.UUID
		if err := admin.QueryRow(ctx, `SELECT status, controller, assignee_id FROM conversations WHERE id=$1`, id).Scan(&status, &controller, &member); err != nil {
			t.Fatal(err)
		}
		if member != nil {
			assignee = *member
		}
		return status, controller, assignee
	}
	closures := func(id uuid.UUID) int {
		t.Helper()
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM conversation_cycles WHERE tenant_id=$1 AND conversation_id=$2 AND close_result='no_response'`, tenant, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	queued := func(id uuid.UUID) int {
		t.Helper()
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM assignment_queue_intervals WHERE tenant_id=$1 AND conversation_id=$2 AND ended_at IS NULL`, tenant, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	psvc := presence.New(store)
	psvc.Now = func() time.Time { return clock }
	svc := lifecycle.New(store)
	svc.Now = func() time.Time { return clock }
	online := func(a actor) {
		t.Helper()
		if _, err := psvc.Heartbeat(ctx, presence.Principal{SessionID: a.session, UserID: a.user, TenantID: a.p.TenantID, MembershipID: a.member, Role: a.p.Role}, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}

	target := openAssigned(home, agent.member, "open")
	exec(`INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, tenant, agent.member)
	probe := func() error {
		t.Helper()
		return store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			return attendancestore.LockAssignmentCapacity(ctx, tx, tenant, agent.member, uuid.New())
		})
	}
	if !errors.Is(probe(), attendancestore.ErrCapacityFull) {
		t.Fatal("vaga deveria estar cheia antes do adiamento")
	}
	if _, err = svc.Snooze(ctx, otherAgent.p, target, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(target), Until: clock.Add(time.Hour), Reason: "alheio"}); !errors.Is(err, lifecycle.ErrForbidden) {
		t.Fatalf("outro agente %v", err)
	}
	cmd := lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(target), Until: clock.Add(2 * time.Hour), Reason: "aguardar retorno"}
	snoozed, err := svc.Snooze(ctx, agent.p, target, cmd)
	if err != nil || snoozed.State != "active" || snoozed.DestinationMembershipID != agent.member || snoozed.AssigneeID != nil {
		t.Fatalf("adiar %+v %v", snoozed, err)
	}
	status, controller, assignee := stateOf(target)
	if status != "open" || controller != "menu" || assignee != uuid.Nil || closures(target) != 0 || queued(target) != 0 {
		t.Fatalf("adiamento alterou a conversa %s %s %s fila=%d", status, controller, assignee, queued(target))
	}
	if err = probe(); err != nil {
		t.Fatalf("vaga liberada %v", err)
	}
	again, err := svc.Snooze(ctx, agent.p, target, cmd)
	if err != nil || again.Version != snoozed.Version {
		t.Fatalf("replay %+v %v", again, err)
	}
	svc.Now = func() time.Time { return cmd.Until.Add(time.Minute) }
	late, err := svc.Snooze(ctx, agent.p, target, cmd)
	svc.Now = func() time.Time { return clock }
	if err != nil || late.ID != snoozed.ID || late.Version != snoozed.Version {
		t.Fatalf("replay após o prazo %+v %v", late, err)
	}
	svc.Now = func() time.Time { return cmd.Until.Add(time.Minute) }
	if _, err = svc.Snooze(ctx, agent.p, target, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(target), Until: cmd.Until, Reason: "vencido"}); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("comando novo vencido %v", err)
	}
	svc.Now = func() time.Time { return clock }
	var events int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='snoozed'`, tenant, target).Scan(&events); err != nil || events != 1 {
		t.Fatalf("eventos de adiamento %d %v", events, err)
	}
	if _, err = svc.Snooze(ctx, agent.p, target, lifecycle.SnoozeCommand{RequestID: cmd.RequestID, InboxID: home.inbox, ExpectedVersion: cmd.ExpectedVersion, Until: cmd.Until, Reason: "outro motivo"}); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("hash %v", err)
	}
	idemConv := openAssigned(home, agent.member, "open")
	idemCmd := lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(idemConv), Until: clock.Add(2 * time.Hour), Reason: "retry concorrente"}
	idemRelease := make(chan struct{})
	idemHeld := make(chan struct{})
	idemHoldErr := make(chan error, 1)
	go func() {
		idemHoldErr <- store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-idem-hold */`, tenant, idemConv); err != nil {
				return err
			}
			close(idemHeld)
			select {
			case <-idemRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
	}()
	select {
	case <-idemHeld:
	case err := <-idemHoldErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	type snoozeResult struct {
		body lifecycle.Snooze
		err  error
	}
	idemResults := make(chan snoozeResult, 2)
	for range 2 {
		go func() {
			body, err := svc.Snooze(ctx, agent.p, idemConv, idemCmd)
			idemResults <- snoozeResult{body, err}
		}()
	}
	waitActivity := func(query string, want int) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1`, query).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(15 * time.Millisecond):
			}
		}
		t.Fatalf("espera de lock ausente: %s", query)
	}
	waitActivity("%lifecycle-command:%", 1)
	waitActivity("%lifecycle-conv%", 1)
	close(idemRelease)
	if err = <-idemHoldErr; err != nil {
		t.Fatal(err)
	}
	firstIdem := <-idemResults
	secondIdem := <-idemResults
	if firstIdem.err != nil || secondIdem.err != nil || firstIdem.body.ID == uuid.Nil || firstIdem.body.ID != secondIdem.body.ID || firstIdem.body.Version != secondIdem.body.Version {
		t.Fatalf("retry concorrente %+v %+v", firstIdem, secondIdem)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='snoozed'`, tenant, idemConv).Scan(&events); err != nil || events != 1 {
		t.Fatalf("eventos do retry concorrente %d %v", events, err)
	}
	distinct := openAssigned(home, agent.member, "open")
	distinctVersion := versionOf(distinct)
	distinctRelease := make(chan struct{})
	distinctHeld := make(chan struct{})
	distinctHoldErr := make(chan error, 1)
	go func() {
		distinctHoldErr <- store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-distinct-hold */`, tenant, distinct); err != nil {
				return err
			}
			close(distinctHeld)
			select {
			case <-distinctRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
	}()
	select {
	case <-distinctHeld:
	case err := <-distinctHoldErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	distinctResults := make(chan snoozeResult, 2)
	for _, reason := range []string{"chave a", "chave b"} {
		cmd := lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: distinctVersion, Until: clock.Add(2 * time.Hour), Reason: reason}
		go func(cmd lifecycle.SnoozeCommand) {
			body, err := svc.Snooze(ctx, agent.p, distinct, cmd)
			distinctResults <- snoozeResult{body, err}
		}(cmd)
	}
	waitActivity("%lifecycle-conv%", 2)
	close(distinctRelease)
	if err = <-distinctHoldErr; err != nil {
		t.Fatal(err)
	}
	accepted, conflicts := 0, 0
	for range 2 {
		result := <-distinctResults
		var pg *pgconn.PgError
		if errors.As(result.err, &pg) && pg.Code == "40P01" {
			t.Fatalf("deadlock entre chaves diferentes: %v", result.err)
		}
		switch {
		case result.err == nil && result.body.ID != uuid.Nil:
			accepted++
		case errors.Is(result.err, lifecycle.ErrConflict):
			conflicts++
		default:
			t.Fatalf("chaves diferentes %+v", result)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("chaves diferentes aceitos=%d conflitos=%d", accepted, conflicts)
	}
	status, controller, assignee = stateOf(distinct)
	if status != "open" || controller != "menu" || assignee != uuid.Nil || closures(distinct) != 0 || queued(distinct) != 0 {
		t.Fatalf("adiamento concorrente %s %s %s fila=%d", status, controller, assignee, queued(distinct))
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active'`, tenant, distinct).Scan(&events); err != nil || events != 1 {
		t.Fatalf("adiamentos ativos %d %v", events, err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='snoozed'`, tenant, distinct).Scan(&events); err != nil || events != 1 {
		t.Fatalf("eventos das chaves diferentes %d %v", events, err)
	}
	foreignConv := openAssigned(away, outsider.member, "open")
	if _, err = svc.Snooze(ctx, agent.p, foreignConv, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: away.inbox, ExpectedVersion: 1, Until: clock.Add(time.Hour), Reason: "setor"}); !errors.Is(err, lifecycle.ErrForbidden) {
		t.Fatalf("outro setor %v", err)
	}
	sibling := openQueue(home)
	online(agent)
	var picked distribution.Result
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		picked, err = distribution.Pick(ctx, tx, tenant, home.sector, home.inbox, clock)
		return err
	})
	if err != nil || picked.ConversationID != sibling || picked.Status != distribution.StatusAssigned {
		t.Fatalf("pick da adiada %+v %v", picked, err)
	}
	if _, _, assignee = stateOf(target); assignee != uuid.Nil || queued(target) != 0 {
		t.Fatalf("pick tomou a adiada %s", assignee)
	}
	exec(`UPDATE conversations SET assignee_id=NULL, controller='menu', updated_at=$2 WHERE tenant_id=$1 AND id=$3`, tenant, clock, sibling)

	early := openAssigned(home, agent.member, "open")
	earlyCmd := lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(early), Until: clock.Add(3 * time.Hour), Reason: "cliente vai enviar documento"}
	if _, err = svc.Snooze(ctx, agent.p, early, earlyCmd); err != nil {
		t.Fatal(err)
	}
	var earlyRel lifecycle.Release
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		earlyRel, err = lifecycle.OnCustomerMessage(ctx, tx, tenant, early, clock.Add(10*time.Second))
		return err
	})
	if err != nil || !earlyRel.Cleared || !earlyRel.Assigned || earlyRel.MembershipID != agent.member || earlyRel.Status == "closed" {
		t.Fatalf("mensagem %+v %v", earlyRel, err)
	}
	status, controller, assignee = stateOf(early)
	if status != "open" || controller != "human" || assignee != agent.member || closures(early) != 0 {
		t.Fatalf("retorno antecipado %s %s %s", status, controller, assignee)
	}
	var snoozeState string
	if err = admin.QueryRow(ctx, `SELECT state FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at DESC LIMIT 1`, tenant, early).Scan(&snoozeState); err != nil || snoozeState != "cleared_by_customer" {
		t.Fatalf("estado do adiamento %s %v", snoozeState, err)
	}

	offlineConv := openAssigned(home, otherAgent.member, "open")
	if _, err = svc.Snooze(ctx, otherAgent.p, offlineConv, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(offlineConv), Until: clock.Add(time.Hour), Reason: "sem presença"}); err != nil {
		t.Fatal(err)
	}
	var due []lifecycle.Release
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		due, err = lifecycle.ReturnDue(ctx, tx, tenant, clock.Add(time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	foundDue := false
	for _, item := range due {
		if item.ConversationID == offlineConv {
			foundDue = true
			if item.Assigned || item.Status == "closed" || item.Resolution != "queued" {
				t.Fatalf("prazo offline %+v", item)
			}
		}
		if item.Status == "closed" {
			t.Fatalf("prazo encerrou %+v", item)
		}
	}
	if !foundDue {
		t.Fatal("prazo não devolveu a conversa")
	}
	status, _, assignee = stateOf(offlineConv)
	if status == "closed" || assignee != uuid.Nil || queued(offlineConv) != 1 || closures(offlineConv) != 0 {
		t.Fatalf("fila do prazo %s %s intervalos=%d", status, assignee, queued(offlineConv))
	}

	full := openAssigned(home, agent.member, "open")
	fullUntil := clock.Add(30 * time.Minute)
	if _, err = svc.Snooze(ctx, agent.p, full, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(full), Until: fullUntil, Reason: "sem vaga"}); err != nil {
		t.Fatal(err)
	}
	clock = fullUntil
	online(agent)
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		due, err = lifecycle.ReturnDue(ctx, tx, tenant, fullUntil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _, assignee = stateOf(full)
	if status == "closed" || assignee != uuid.Nil || queued(full) != 1 {
		t.Fatalf("sem vaga %s %s fila=%d", status, assignee, queued(full))
	}

	raceConv := openAssigned(home, otherAgent.member, "open")
	if _, err = svc.Snooze(ctx, otherAgent.p, raceConv, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(raceConv), Until: clock.Add(90 * time.Minute), Reason: "corrida"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	held := make(chan struct{})
	holdErr := make(chan error, 1)
	jobErr := make(chan error, 1)
	go func() {
		holdErr <- store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-hold */`, tenant, raceConv); err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := lifecycle.OnCustomerMessage(ctx, tx, tenant, raceConv, clock.Add(20*time.Minute))
			return err
		})
	}()
	select {
	case <-held:
	case err := <-holdErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		for {
			var rels []lifecycle.Release
			err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				rels, err = lifecycle.ReturnDue(ctx, tx, tenant, clock.Add(2*time.Hour))
				return err
			})
			if err != nil || len(rels) == 0 {
				jobErr <- err
				return
			}
			if !rels[0].Cleared {
				continue
			}
		}
	}()
	waitLock(t, ctx, admin)
	close(release)
	if err = <-holdErr; err != nil {
		t.Fatal(err)
	}
	if err = <-jobErr; err != nil {
		t.Fatal(err)
	}
	status, _, _ = stateOf(raceConv)
	if status == "closed" || closures(raceConv) != 0 {
		t.Fatalf("B8-11 encerrou %s", status)
	}
	var active int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 AND state='active'`, tenant, raceConv).Scan(&active); err != nil || active != 0 {
		t.Fatalf("adiamento ativo depois da mensagem %d %v", active, err)
	}

	dueAt := clock.Add(time.Minute)
	psvc.Now = func() time.Time { return dueAt }
	online(otherAgent)
	psvc.Now = func() time.Time { return clock }
	first := openAssigned(home, otherAgent.member, "open")
	second := openAssigned(home, otherAgent.member, "open")
	if _, err = svc.Snooze(ctx, otherAgent.p, first, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(first), Until: dueAt, Reason: "lote a"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Snooze(ctx, otherAgent.p, second, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(second), Until: dueAt, Reason: "lote b"}); err != nil {
		t.Fatal(err)
	}
	low, high := first, second
	if bytes.Compare(high[:], low[:]) < 0 {
		low, high = high, low
	}
	release = make(chan struct{})
	held = make(chan struct{})
	proceed := make(chan struct{})
	go func() {
		holdErr <- store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-hold */`, tenant, high); err != nil {
				return err
			}
			close(held)
			select {
			case <-proceed:
			case <-ctx.Done():
				return ctx.Err()
			}
			lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := tx.Exec(lockCtx, `SELECT pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:' || $1::text || ':' || $2::text, 0))`, tenant, otherAgent.member)
			return err
		})
	}()
	select {
	case <-held:
	case err := <-holdErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, err := svc.ReturnDue(ctx, tenant, dueAt)
		jobErr <- err
	}()
	waitUntil := time.Now().Add(20 * time.Second)
	for {
		var state string
		if err = admin.QueryRow(ctx, `SELECT state FROM conversation_snoozes WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at DESC LIMIT 1`, tenant, low).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != "active" {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatal("a primeira devolução não commitou enquanto a segunda conversa seguia travada")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(15 * time.Millisecond):
		}
	}
	waitLock(t, ctx, admin)
	var inverted int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity a WHERE a.wait_event_type='Lock' AND a.query LIKE '%lifecycle-conv%' AND EXISTS (SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.locktype='advisory' AND l.granted AND l.mode='ExclusiveLock')`).Scan(&inverted); err != nil {
		t.Fatal(err)
	}
	if inverted != 0 {
		t.Fatal("devolução segurou capacidade enquanto esperava a conversa seguinte")
	}
	close(proceed)
	if err = <-holdErr; err != nil {
		t.Fatal(err)
	}
	if err = <-jobErr; err != nil {
		t.Fatal(err)
	}
	_, _, assignee = stateOf(low)
	if assignee != otherAgent.member || closures(low) != 0 {
		t.Fatalf("primeira devolução %s", assignee)
	}
	_, _, assignee = stateOf(high)
	if assignee != otherAgent.member || closures(high) != 0 {
		t.Fatalf("segunda devolução %s", assignee)
	}

	settings, err := svc.PutInactivity(ctx, adminA.p, lifecycle.InactivityCommand{RequestID: uuid.New(), ExpectedVersion: 0, WarningAfterSeconds: 60, CloseAfterSeconds: 60})
	if err != nil || settings.Version != 1 || settings.WarningAfterSeconds != 60 {
		t.Fatalf("prazos %+v %v", settings, err)
	}
	if _, err = svc.PutInactivity(ctx, agent.p, lifecycle.InactivityCommand{RequestID: uuid.New(), ExpectedVersion: 1, WarningAfterSeconds: 60, CloseAfterSeconds: 60}); !errors.Is(err, lifecycle.ErrForbidden) {
		t.Fatalf("agente configura %v", err)
	}
	anchor := clock
	uncertain := waiting(home, agent.member, anchor)
	closing := waiting(home, agent.member, anchor)
	blocked := waiting(home, agent.member, anchor)
	stale := waiting(home, agent.member, anchor)
	missed := waiting(home, agent.member, anchor)
	effects, err := svc.RunInactivity(ctx, tenant, anchor.Add(time.Minute))
	if err != nil || len(effects) != 5 {
		t.Fatalf("avisos %+v %v", effects, err)
	}
	var pending int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM lifecycle_warning_requests WHERE tenant_id=$1 AND state='pending' AND body=$2`, tenant, lifecycle.WarningBody).Scan(&pending); err != nil || pending != 5 {
		t.Fatalf("pedidos pendentes %d %v", pending, err)
	}
	closeAt := anchor.Add(2 * time.Minute)
	if _, err = svc.RunInactivity(ctx, tenant, closeAt); err != nil {
		t.Fatal(err)
	}
	if closures(closing) != 0 || closures(uncertain) != 0 {
		t.Fatal("pedido sem confirmação encerrou")
	}
	confirm := func(id uuid.UUID, generation int64, outcome string) lifecycle.Effect {
		t.Helper()
		var got lifecycle.Effect
		err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			got, err = lifecycle.ConfirmWarning(ctx, tx, tenant, lifecycle.WarningReceipt{ConversationID: id, Generation: generation, Outcome: outcome}, anchor.Add(time.Minute))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	byID := map[uuid.UUID]lifecycle.Effect{}
	for _, item := range effects {
		byID[item.ConversationID] = item
	}
	if got := confirm(uncertain, byID[uncertain].Generation, lifecycle.WarningUncertain); got.Action != "uncertain" {
		t.Fatalf("confirmação incerta %+v", got)
	}
	againUncertain := confirm(uncertain, byID[uncertain].Generation, lifecycle.WarningUncertain)
	if againUncertain.Action != "uncertain" {
		t.Fatalf("replay incerto %+v", againUncertain)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := lifecycle.ConfirmWarning(ctx, tx, tenant, lifecycle.WarningReceipt{ConversationID: uncertain, Generation: byID[uncertain].Generation, Outcome: lifecycle.WarningConfirmed}, anchor.Add(time.Minute))
		return err
	}); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("resultado trocado %v", err)
	}
	for _, id := range []uuid.UUID{closing, blocked, stale} {
		got := confirm(id, byID[id].Generation, lifecycle.WarningConfirmed)
		if got.Action != "confirmed" {
			t.Fatalf("confirmação %+v", got)
		}
		replayed := confirm(id, byID[id].Generation, lifecycle.WarningConfirmed)
		if replayed.Action != "confirmed" {
			t.Fatalf("replay da confirmação %+v", replayed)
		}
	}
	var closeDue time.Time
	if err = admin.QueryRow(ctx, `SELECT close_due_at FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, closing).Scan(&closeDue); err != nil {
		t.Fatal(err)
	}
	_ = confirm(closing, byID[closing].Generation, lifecycle.WarningConfirmed)
	var closeDueAgain time.Time
	if err = admin.QueryRow(ctx, `SELECT close_due_at FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, closing).Scan(&closeDueAgain); err != nil || !closeDueAgain.Equal(closeDue) {
		t.Fatalf("replay moveu o prazo %s %s %v", closeDue, closeDueAgain, err)
	}
	if _, err = admin.Exec(ctx, `UPDATE conversations SET last_customer_message_at=$3, updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenant, missed, anchor.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := confirm(missed, byID[missed].Generation, lifecycle.WarningConfirmed); got.Action != "uncertain" {
		t.Fatalf("mensagem nova confirmou %+v", got)
	}
	var missedState, missedReq string
	if err = admin.QueryRow(ctx, `SELECT warning_state FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, missed).Scan(&missedState); err != nil || missedState != "uncertain" {
		t.Fatalf("marcador da mensagem nova %s %v", missedState, err)
	}
	var missedReceipt string
	if err = admin.QueryRow(ctx, `SELECT state, receipt_outcome FROM lifecycle_warning_requests WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3`, tenant, missed, byID[missed].Generation).Scan(&missedReq, &missedReceipt); err != nil || missedReq != "uncertain" || missedReceipt != lifecycle.WarningConfirmed {
		t.Fatalf("pedido da mensagem nova %s %s %v", missedReq, missedReceipt, err)
	}
	if again := confirm(missed, byID[missed].Generation, lifecycle.WarningConfirmed); again.Action != "uncertain" {
		t.Fatalf("replay do confirmed obsoleto %+v", again)
	}
	var missedDue *time.Time
	if err = admin.QueryRow(ctx, `SELECT close_due_at FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, missed).Scan(&missedDue); err != nil || missedDue != nil {
		t.Fatalf("replay armou prazo %v %v", missedDue, err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := lifecycle.ConfirmWarning(ctx, tx, tenant, lifecycle.WarningReceipt{ConversationID: missed, Generation: byID[missed].Generation, Outcome: lifecycle.WarningUncertain}, anchor.Add(time.Minute))
		return err
	}); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("recibo trocado após mensagem nova %v", err)
	}
	obsolete := waiting(home, agent.member, anchor)
	obsoleteEffects, err := svc.RunInactivity(ctx, tenant, anchor.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var obsoleteGen int64
	for _, item := range obsoleteEffects {
		if item.ConversationID == obsolete {
			obsoleteGen = item.Generation
		}
	}
	if obsoleteGen < 1 {
		t.Fatal("geração obsoleta não foi pedida")
	}
	if _, err = svc.Snooze(ctx, agent.p, obsolete, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(obsolete), Until: clock.Add(4 * time.Hour), Reason: "gera ciclo novo"}); err != nil {
		t.Fatal(err)
	}
	var nextGen int64
	var nextState string
	if err = admin.QueryRow(ctx, `SELECT generation, warning_state FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, obsolete).Scan(&nextGen, &nextState); err != nil || nextGen <= obsoleteGen || nextState == "sent" {
		t.Fatalf("geração nova %d %s %v", nextGen, nextState, err)
	}
	if got := confirm(obsolete, obsoleteGen, lifecycle.WarningConfirmed); got.Action != "uncertain" {
		t.Fatalf("geração obsoleta armou %+v", got)
	}
	if got := confirm(obsolete, obsoleteGen, lifecycle.WarningConfirmed); got.Action != "uncertain" {
		t.Fatalf("replay da geração obsoleta %+v", got)
	}
	var obsoleteReceipt, obsoleteState string
	if err = admin.QueryRow(ctx, `SELECT state, receipt_outcome FROM lifecycle_warning_requests WHERE tenant_id=$1 AND conversation_id=$2 AND generation=$3`, tenant, obsolete, obsoleteGen).Scan(&obsoleteState, &obsoleteReceipt); err != nil || obsoleteState != "uncertain" || obsoleteReceipt != lifecycle.WarningConfirmed {
		t.Fatalf("pedido obsoleto %s %s %v", obsoleteState, obsoleteReceipt, err)
	}
	if err = admin.QueryRow(ctx, `SELECT warning_state FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, obsolete).Scan(&nextState); err != nil || nextState == "sent" {
		t.Fatalf("geração nova marcada %s %v", nextState, err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := lifecycle.ConfirmWarning(ctx, tx, tenant, lifecycle.WarningReceipt{ConversationID: obsolete, Generation: obsoleteGen, Outcome: lifecycle.WarningUncertain}, anchor.Add(time.Minute))
		return err
	}); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("recibo trocado na geração obsoleta %v", err)
	}
	exec(`INSERT INTO conversation_snoozes(tenant_id,id,conversation_id,snoozed_until,reason,destination_membership_id,destination_sector_id,state,created_by_membership_id,created_at) VALUES($1,$2,$3,$4,'ainda adiada',$5,$6,'active',$5,$7)`, tenant, uuid.New(), blocked, anchor.Add(4*time.Hour), agent.member, home.sector, anchor)
	release = make(chan struct{})
	held = make(chan struct{})
	go func() {
		holdErr <- store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM conversations WHERE tenant_id=$1 AND id=$2 FOR UPDATE /* lifecycle-hold */`, tenant, stale); err != nil {
				return err
			}
			close(held)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := tx.Exec(ctx, `UPDATE conversations SET last_customer_message_at=$3, status='open', waiting_since=NULL, version=version+1, updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenant, stale, anchor.Add(90*time.Second))
			return err
		})
	}()
	select {
	case <-held:
	case err := <-holdErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		var runErr error
		effects, runErr = svc.RunInactivity(ctx, tenant, closeAt)
		jobErr <- runErr
	}()
	waitLock(t, ctx, admin)
	close(release)
	if err = <-holdErr; err != nil {
		t.Fatal(err)
	}
	if err = <-jobErr; err != nil {
		t.Fatal(err)
	}
	status, _, _ = stateOf(uncertain)
	if status != "waiting_customer" || closures(uncertain) != 0 {
		t.Fatalf("aviso incerto encerrou %s", status)
	}
	var warningState string
	if err = admin.QueryRow(ctx, `SELECT warning_state FROM inactivity_watches WHERE tenant_id=$1 AND conversation_id=$2`, tenant, uncertain).Scan(&warningState); err != nil || warningState != "uncertain" {
		t.Fatalf("marcador incerto %s %v", warningState, err)
	}
	status, _, _ = stateOf(closing)
	if status != "closed" || closures(closing) != 1 {
		t.Fatalf("encerramento %s ciclos=%d", status, closures(closing))
	}
	var result, summary string
	if err = admin.QueryRow(ctx, `SELECT close_result, close_summary FROM conversation_cycles WHERE tenant_id=$1 AND conversation_id=$2 AND closed_at IS NOT NULL`, tenant, closing).Scan(&result, &summary); err != nil || result != "no_response" || summary != "Encerrado sem retorno do cliente." {
		t.Fatalf("síntese %s %s %v", result, summary, err)
	}
	status, _, _ = stateOf(blocked)
	if status == "closed" || closures(blocked) != 0 {
		t.Fatalf("adiamento não impediu o encerramento %s", status)
	}
	status, _, _ = stateOf(stale)
	if status != "open" || closures(stale) != 0 {
		t.Fatalf("B8-08 encerrou %s ciclos=%d efeitos=%+v", status, closures(stale), effects)
	}
	if _, err = svc.RunInactivity(ctx, tenant, closeAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if closures(closing) != 1 || closures(stale) != 0 || closures(uncertain) != 0 || closures(missed) != 0 {
		t.Fatal("job repetido alterou ciclos")
	}

	conn, err := pools.App.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var visible int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("RLS sem escopo %d %v", visible, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM lifecycle_warning_requests`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("RLS do aviso sem escopo %d %v", visible, err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, other.String()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("RLS outro tenant %d %v", visible, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM lifecycle_warning_requests`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("RLS do aviso em outro tenant %d %v", visible, err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM conversation_snoozes`).Scan(&visible); err != nil || visible == 0 {
		t.Fatalf("RLS do tenant %d %v", visible, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM lifecycle_warning_requests`).Scan(&visible); err != nil || visible == 0 {
		t.Fatalf("RLS do aviso no tenant %d %v", visible, err)
	}
	err = store.WithTenantTx(ctx, other, func(ctx context.Context, tx pgx.Tx) error {
		_, err := lifecycle.ReturnDue(ctx, tx, tenant, clock.Add(5*time.Hour))
		return err
	})
	if !errors.Is(err, lifecycle.ErrForbidden) {
		t.Fatalf("escopo trocado %v", err)
	}

	exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, tenant)
	if _, err = svc.GetInactivity(ctx, adminA.p); !errors.Is(err, lifecycle.ErrForbidden) {
		t.Fatalf("suspenso %v", err)
	}
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)

	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	lifecycleapi.Register(humachi.New(router, cfg), lifecycleapi.Dependencies{Auth: identity, Lifecycle: svc, TrustedOrigins: []string{"https://panel.example"}})
	call := func(method, path, token, origin, csrf string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	httpConv := openAssigned(home, agent.member, "open")
	call("PUT", "/api/panel/conversations/"+httpConv.String()+"/snooze", "", "https://panel.example", "", lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: 1, Until: clock.Add(time.Hour), Reason: "sem sessao"}, 401)
	call("PUT", "/api/panel/conversations/"+httpConv.String()+"/snooze", agent.token, "https://evil.example", agent.csrf, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(httpConv), Until: clock.Add(time.Hour), Reason: "origem"}, 403)
	call("PUT", "/api/panel/conversations/"+httpConv.String()+"/snooze", agent.token, "https://panel.example", "wrong", lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(httpConv), Until: clock.Add(time.Hour), Reason: "csrf"}, 403)
	body := call("PUT", "/api/panel/conversations/"+httpConv.String()+"/snooze", agent.token, "https://panel.example", agent.csrf, lifecycle.SnoozeCommand{RequestID: uuid.New(), InboxID: home.inbox, ExpectedVersion: versionOf(httpConv), Until: clock.Add(time.Hour), Reason: "pelo painel"}, 200)
	var got lifecycle.Snooze
	if err = json.Unmarshal(body, &got); err != nil || got.ConversationID != httpConv || got.State != "active" {
		t.Fatalf("http %+v %v %s", got, err, body)
	}
	call("GET", "/api/panel/conversations/"+httpConv.String()+"/snooze?inbox_id="+home.inbox.String(), agent.token, "", "", nil, 200)
	call("GET", "/api/panel/lifecycle/inactivity", agent.token, "", "", nil, 200)
	call("PUT", "/api/panel/lifecycle/inactivity", agent.token, "https://panel.example", agent.csrf, lifecycle.InactivityCommand{RequestID: uuid.New(), ExpectedVersion: 1, WarningAfterSeconds: 120, CloseAfterSeconds: 120}, 403)
	call("PUT", "/api/panel/lifecycle/inactivity", adminA.token, "https://panel.example", adminA.csrf, lifecycle.InactivityCommand{RequestID: uuid.New(), ExpectedVersion: 1, WarningAfterSeconds: 120, CloseAfterSeconds: 120}, 200)
	exec(`UPDATE sessions SET revoked_at=now() WHERE id=$1`, agent.session)
	call("GET", "/api/panel/lifecycle/inactivity", agent.token, "", "", nil, 401)
}

func waitLock(t *testing.T, ctx context.Context, admin *pgx.Conn) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%lifecycle-conv%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(15 * time.Millisecond):
		}
	}
	t.Fatal("o job não esperou o lock da conversa")
}
