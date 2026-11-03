package triage_test

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

	attendance "chatisp/internal/attendance/service"
	"chatisp/internal/businesshours"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/testdb"
	"chatisp/internal/presence"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
	api "chatisp/internal/triage/httpapi"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresAndHTTP(t *testing.T) {
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
	tenant, user, member := uuid.New(), uuid.New(), uuid.New()
	inbox, connector := uuid.New(), uuid.New()
	support, finance, oncall := uuid.New(), uuid.New(), uuid.New()
	token, csrf := uuid.NewString(), uuid.NewString()
	sessionID := uuid.New()
	exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active')`, tenant)
	exec(`INSERT INTO users(id,email,status,totp_ciphertext,totp_key_version,totp_aad_tenant,totp_confirmed_at) VALUES($1,$2,'active',$3,'v1',$4,now())`, user, user.String()+"@example.test", bytes.Repeat([]byte{7}, 32), tenant)
	exec(`INSERT INTO memberships(id,tenant_id,user_id,role,status) VALUES($1,$2,$3,'admin','active')`, member, tenant, user)
	exec(`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin) VALUES($1,$2,'meta_cloud',$3)`, connector, tenant, connector.String())
	exec(`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa')`, inbox, tenant, connector)
	exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte'),($1,$3,'Financeiro'),($1,$4,'Plantão')`, tenant, support, finance, oncall)
	exec(`INSERT INTO sector_inboxes VALUES($1,$2,$5),($1,$3,$5),($1,$4,$5)`, tenant, support, finance, oncall, inbox)
	exec(`INSERT INTO inbox_sector_defaults VALUES($1,$2,$3)`, tenant, inbox, support)
	hours := businesshours.Config{Intervals: []businesshours.Interval{{Day: 1, Start: 540, End: 1080}}, Welcome: "Olá {protocolo}", OutsideHours: "Fora do expediente.", NobodyAvailable: "Ninguém disponível.", OnCallSectorID: &oncall}
	rawHours, _ := json.Marshal(hours)
	exec(`INSERT INTO inbox_business_hours(tenant_id,inbox_id,version,configured,config) VALUES($1,$2,1,true,$3)`, tenant, inbox, rawHours)
	now := time.Now()
	if err = store.WithAuth(ctx, func(ctx context.Context) error {
		return store.InsertSession(ctx, db.NewSession{ID: sessionID, TokenHash: auth.HashSecret(token), UserID: user, TenantID: tenant, MembershipID: member, CSRFHash: auth.HashSecret(csrf), Now: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	openAt := time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC)
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		snap, err := triage.ReadDistributionQueue(ctx, tx, tenant, support, inbox, openAt)
		if err != nil || snap.Reason != triage.ReasonReady || !snap.AutoAssign {
			t.Fatalf("fila %+v %v", snap, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var cursors int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM distribution_cursors WHERE tenant_id=$1`, tenant).Scan(&cursors); err != nil || cursors != 0 {
		t.Fatalf("cursor %d %v", cursors, err)
	}

	principal := triage.Principal{TenantID: tenant, UserID: user, MembershipID: member, Role: "admin"}
	svc := triage.New(store)
	menu, err := svc.Get(ctx, principal, inbox)
	if err != nil || menu.Version != 0 || menu.Configured || menu.MaxAttempts != 3 || len(menu.Options) != 0 {
		t.Fatalf("vazio %+v %v", menu, err)
	}
	supportID, financeID := support, finance
	badSector := uuid.New()
	base := triage.Command{RequestID: uuid.New(), MaxAttempts: 2, Options: []triage.Option{
		{Key: "1", Label: "Suporte", Destination: triage.DestSector, SectorID: &supportID},
		{Key: "2", Label: "Financeiro", Destination: triage.DestSector, SectorID: &financeID, Reason: "financeiro"},
		{Key: "0", Label: "Atendente", Destination: triage.DestHuman},
	}}
	orphan := base
	orphan.RequestID = uuid.New()
	orphan.Options = []triage.Option{
		{Key: "1", Label: "Sumido", Destination: triage.DestSector, SectorID: &badSector},
		{Key: "0", Label: "Atendente", Destination: triage.DestHuman},
	}
	if _, err = svc.Put(ctx, principal, inbox, orphan); !errors.Is(err, triage.ErrInvalid) {
		t.Fatalf("setor solto %v", err)
	}
	noHuman := base
	noHuman.RequestID = uuid.New()
	noHuman.Options = base.Options[:2]
	if _, err = svc.Put(ctx, principal, inbox, noHuman); !errors.Is(err, triage.ErrInvalid) {
		t.Fatalf("sem humano %v", err)
	}
	menu, err = svc.Put(ctx, principal, inbox, base)
	if err != nil || menu.Version != 1 || !menu.Configured || menu.MaxAttempts != 2 || len(menu.Options) != 3 {
		t.Fatalf("put %+v %v", menu, err)
	}
	replay, err := svc.Put(ctx, principal, inbox, base)
	if err != nil || replay.Version != 1 {
		t.Fatalf("replay %+v %v", replay, err)
	}
	changed := base
	changed.Options[1].Reason = "outro"
	if _, err = svc.Put(ctx, principal, inbox, changed); !errors.Is(err, triage.ErrConflict) {
		t.Fatalf("hash %v", err)
	}
	stale := base
	stale.RequestID = uuid.New()
	if _, err = svc.Put(ctx, principal, inbox, stale); !errors.Is(err, triage.ErrConflict) {
		t.Fatalf("versão %v", err)
	}
	var audits int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='triage_menu.put'`, tenant).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit %d %v", audits, err)
	}
	agent := principal
	agent.Role = "agent"
	if _, err = svc.Get(ctx, agent, inbox); !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("agente %v", err)
	}
	forged := principal
	forged.MembershipID = uuid.New()
	if _, err = svc.Get(ctx, forged, inbox); !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("matrícula %v", err)
	}

	seq := 0
	newConv := func(protocol, status, controller string) uuid.UUID {
		t.Helper()
		seq++
		contact, identity, conversation := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, contact, tenant)
		exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, connector, identity.String())
		exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'202611',1)`, conversation, tenant, inbox, support, contact, identity, status, controller, protocol)
		return conversation
	}
	decide := func(conversation uuid.UUID, text string, port triage.Port, instant time.Time, input uuid.UUID) triage.Outcome {
		t.Helper()
		if input == uuid.Nil {
			input = uuid.New()
		}
		var out triage.Outcome
		err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			out, err = triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: conversation, Text: text, Instant: instant, InputID: input, Port: port})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ready := &triage.FixedPort{Reason: triage.ReasonReady, AutoAssign: true}
	firstID := uuid.New()
	choice := newConv("P-1001", "open", "menu")
	first := decide(choice, "zzz", ready, openAt, firstID)
	if first.Phase != triage.PhaseMenu || first.Attempts != 0 || first.ControllerEpoch != 1 || first.Version != 1 || !first.WelcomeSent || ready.Calls != 0 || !has(first, triage.KindSendMenu) || !has(first, triage.KindSendText) {
		t.Fatalf("menu %+v calls %d", first, ready.Calls)
	}
	if textOf(first, triage.KindSendText) != "Olá P-1001" || !strings.Contains(fallback(first), "0 - Atendente") || !humanOption(first) {
		t.Fatalf("fallback %+v", first)
	}
	again := decide(choice, "zzz", ready, openAt, firstID)
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(again)
	if !bytes.Equal(left, right) {
		t.Fatalf("replay %s %s", left, right)
	}
	picked := decide(choice, "2 - financeiro", ready, openAt, uuid.Nil)
	if picked.Phase != triage.PhaseRouted || picked.Attempts != 0 || picked.ControllerEpoch != 1 || sectorOf(picked, triage.KindRouteSector) != finance || textOf(picked, triage.KindRecordReason) != "" || reasonOf(picked) != "financeiro" || !has(picked, triage.KindForwardHuman) || has(picked, triage.KindSendMenu) || has(picked, triage.KindSendText) || has(picked, triage.KindOnCall) {
		t.Fatalf("financeiro %+v", picked)
	}
	if ready.Calls != 1 {
		t.Fatalf("port %d", ready.Calls)
	}
	quiet := decide(choice, "1", ready, openAt, uuid.Nil)
	if quiet.Phase != triage.PhaseRouted || len(quiet.Actions) != 0 || quiet.Attempts != 0 || quiet.ControllerEpoch != 1 {
		t.Fatalf("resolvida %+v", quiet)
	}
	var eventsBefore int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM triage_events WHERE tenant_id=$1 AND conversation_id=$2`, tenant, choice).Scan(&eventsBefore); err != nil || eventsBefore < 2 {
		t.Fatalf("histórico %d %v", eventsBefore, err)
	}
	exec(`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,$4)`, tenant, uuid.New(), choice, openAt)
	panel := attendance.New(store, nil)
	actor := attendance.Principal{UserID: user, MembershipID: member, TenantID: tenant, Role: "admin"}
	closedCycle, err := panel.Close(ctx, actor, attendance.CloseInput{
		InboxID: inbox, ConversationID: choice, ExpectedVersion: 1, ClientRequestID: "triage-close",
		Reason: "fim do ciclo", Result: "resolved", Summary: "ciclo um", At: openAt,
	})
	if err != nil || closedCycle.Conversation.Status != "closed" || closedCycle.Conversation.CycleNumber != 1 {
		t.Fatalf("fechar para reabrir %v %+v", err, closedCycle.Conversation)
	}
	reopened, err := panel.Reopen(ctx, actor, attendance.ReopenInput{
		InboxID: inbox, ConversationID: choice, ExpectedVersion: closedCycle.Conversation.Version,
		ClientRequestID: "triage-reopen", At: openAt.Add(time.Minute),
	})
	if err != nil || reopened.Status != "open" || reopened.Controller != "menu" || reopened.CycleNumber != 2 || reopened.ControllerEpoch <= 1 || reopened.Protocol != "P-1001" {
		t.Fatalf("reabertura %+v %v", reopened, err)
	}
	replayedChoice := decide(choice, "zzz", ready, openAt, firstID)
	replayedRaw, _ := json.Marshal(replayedChoice)
	if !bytes.Equal(left, replayedRaw) {
		t.Fatalf("idempotência após reabertura %s", replayedRaw)
	}
	restart := decide(choice, "de novo", ready, openAt, uuid.Nil)
	if restart.Phase != triage.PhaseMenu || restart.Attempts != 0 || !restart.WelcomeSent || has(restart, triage.KindSendText) || !has(restart, triage.KindSendMenu) || !humanOption(restart) || restart.ControllerEpoch != reopened.ControllerEpoch || restart.Version != reopened.Version {
		t.Fatalf("novo ciclo %+v época %d versão %d", restart, reopened.ControllerEpoch, reopened.Version)
	}
	restartHuman := decide(choice, "humano", ready, openAt, uuid.Nil)
	if restartHuman.Phase != triage.PhaseRouted || restartHuman.Attempts != 0 || len(restartHuman.Actions) == 0 || !has(restartHuman, triage.KindForwardHuman) || sectorOf(restartHuman, triage.KindRouteSector) != support || has(restartHuman, triage.KindSendMenu) {
		t.Fatalf("humano no novo ciclo %+v", restartHuman)
	}
	var eventsAfter int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM triage_events WHERE tenant_id=$1 AND conversation_id=$2`, tenant, choice).Scan(&eventsAfter); err != nil || eventsAfter <= eventsBefore {
		t.Fatalf("histórico preservado %d %d %v", eventsBefore, eventsAfter, err)
	}

	human := decide(newConv("P-1002", "open", "menu"), "falar com humano", ready, openAt, uuid.Nil)
	if human.Phase != triage.PhaseRouted || human.Attempts != 0 || sectorOf(human, triage.KindForwardHuman) != support || textOf(human, triage.KindSendText) != "Olá P-1002" || has(human, triage.KindSendMenu) {
		t.Fatalf("humano %+v", human)
	}
	after := newConv("P-1003", "open", "menu")
	bad := decide(after, "??", ready, openAt, uuid.Nil)
	if bad.Attempts != 0 || bad.Phase != triage.PhaseMenu || !has(bad, triage.KindSendMenu) {
		t.Fatalf("tentativa %+v", bad)
	}
	againBad := decide(after, "nope", ready, openAt, uuid.Nil)
	if againBad.Attempts != 1 || againBad.Phase != triage.PhaseMenu {
		t.Fatalf("segunda tentativa %+v", againBad)
	}
	direct := decide(after, "0", ready, openAt, uuid.Nil)
	if direct.Phase != triage.PhaseRouted || direct.Attempts != 1 || sectorOf(direct, triage.KindRouteSector) != support || !has(direct, triage.KindForwardHuman) || has(direct, triage.KindSendMenu) {
		t.Fatalf("humano no meio %+v", direct)
	}

	limit := newConv("P-1004", "triage", "menu")
	step := decide(limit, "asdf", ready, openAt, uuid.Nil)
	if step.Phase != triage.PhaseMenu || step.Attempts != 0 || !strings.Contains(fallback(step), "2 - Financeiro") {
		t.Fatalf("limite 1 %+v", step)
	}
	step = decide(limit, "nope", ready, openAt, uuid.Nil)
	if step.Phase != triage.PhaseMenu || step.Attempts != 1 || has(step, triage.KindForwardHuman) {
		t.Fatalf("limite 2 %+v", step)
	}
	done := decide(limit, "ainda", ready, openAt, uuid.Nil)
	if done.Phase != triage.PhaseExhausted || done.Attempts != 2 || has(done, triage.KindSendMenu) || sectorOf(done, triage.KindRouteSector) != support || sectorOf(done, triage.KindForwardHuman) != support || has(done, triage.KindSendText) {
		t.Fatalf("estouro %+v", done)
	}
	exec(`UPDATE triage_menus SET max_attempts=1 WHERE tenant_id=$1 AND inbox_id=$2`, tenant, inbox)
	one := newConv("P-1004a", "open", "menu")
	greet := decide(one, "oi", ready, openAt, uuid.Nil)
	if greet.Phase != triage.PhaseMenu || greet.Attempts != 0 || !greet.WelcomeSent || !has(greet, triage.KindSendMenu) || has(greet, triage.KindForwardHuman) {
		t.Fatalf("limite unitário %+v", greet)
	}
	spent := decide(one, "xyz", ready, openAt, uuid.Nil)
	if spent.Phase != triage.PhaseExhausted || spent.Attempts != 1 || has(spent, triage.KindSendMenu) || !has(spent, triage.KindForwardHuman) {
		t.Fatalf("estouro unitário %+v", spent)
	}
	asked := decide(newConv("P-1004b", "open", "menu"), "humano", ready, openAt, uuid.Nil)
	if asked.Phase != triage.PhaseRouted || asked.Attempts != 0 || has(asked, triage.KindSendMenu) || !has(asked, triage.KindForwardHuman) {
		t.Fatalf("humano na primeira %+v", asked)
	}
	exec(`UPDATE triage_menus SET max_attempts=2 WHERE tenant_id=$1 AND inbox_id=$2`, tenant, inbox)

	exec(`DELETE FROM sector_inboxes WHERE tenant_id=$1 AND sector_id=$2 AND inbox_id=$3`, tenant, finance, inbox)
	revoked := decide(newConv("P-1005", "open", "menu"), "2", ready, openAt, uuid.Nil)
	if revoked.Phase != triage.PhaseMenu || revoked.Attempts != 0 || has(revoked, triage.KindRouteSector) || !humanOption(revoked) {
		t.Fatalf("revogada %+v", revoked)
	}
	exec(`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, tenant, finance, inbox)

	nobodyPort := &triage.FixedPort{Reason: triage.ReasonNobodyAvailable}
	nobody := decide(newConv("P-1006", "open", "menu"), "1", nobodyPort, openAt, uuid.Nil)
	if nobody.Phase != triage.PhaseRouted || textOf(nobody, triage.KindNobodyAvailable) != "Ninguém disponível." || sectorOf(nobody, triage.KindRouteSector) != support || !has(nobody, triage.KindForwardHuman) || nobodyPort.Calls != 1 {
		t.Fatalf("ninguém %+v", nobody)
	}
	fullPort := &triage.FixedPort{Reason: triage.ReasonCapacityFull}
	full := decide(newConv("P-1007", "open", "menu"), "1", fullPort, openAt, uuid.Nil)
	if has(full, triage.KindNobodyAvailable) || !has(full, triage.KindForwardHuman) || fullPort.Calls != 1 {
		t.Fatalf("capacidade %+v", full)
	}

	closed := decide(newConv("P-1008", "open", "menu"), "oi", ready, closedAt, uuid.Nil)
	if closed.Phase != triage.PhaseOnCall || textOf(closed, triage.KindOutsideHours) != "Fora do expediente." || sectorOf(closed, triage.KindOnCall) != oncall || sectorOf(closed, triage.KindRouteSector) != oncall || !has(closed, triage.KindForwardHuman) || has(closed, triage.KindSendMenu) || textOf(closed, triage.KindSendText) != "Olá P-1008" {
		t.Fatalf("plantão %+v", closed)
	}
	hours.OnCallSectorID = nil
	rawHours, _ = json.Marshal(hours)
	exec(`UPDATE inbox_business_hours SET config=$3 WHERE tenant_id=$1 AND inbox_id=$2`, tenant, inbox, rawHours)
	outside := decide(newConv("P-1009", "open", "menu"), "oi", ready, closedAt, uuid.Nil)
	if outside.Phase != triage.PhaseOutside || has(outside, triage.KindOnCall) || sectorOf(outside, triage.KindRouteSector) != support || !has(outside, triage.KindForwardHuman) || textOf(outside, triage.KindOutsideHours) == "" {
		t.Fatalf("fora %+v", outside)
	}

	exec(`INSERT INTO sector_memberships VALUES($1,$2,$3)`, tenant, support, member)
	live := triage.Availability{}
	inspectAt := func(sector uuid.UUID) triage.Snapshot {
		t.Helper()
		var snap triage.Snapshot
		err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			snap, err = live.Inspect(ctx, tx, tenant, sector, inbox, openAt)
			if err != nil {
				return err
			}
			var autoLocks, capacityLocks int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND classid=hashtext('chatisp-distribution-auto')::oid`).Scan(&autoLocks); err != nil {
				return err
			}
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND objsubid=2`).Scan(&capacityLocks); err != nil {
				return err
			}
			if autoLocks != 0 || capacityLocks != 0 {
				return errors.New("availability segurou advisory de distribuição ou de vaga")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	if snap := inspectAt(support); snap.Reason != triage.ReasonNobodyAvailable || !snap.AutoAssign {
		t.Fatalf("sem presença %+v", snap)
	}
	if snap := inspectAt(uuid.New()); snap.Reason != triage.ReasonSectorInactive {
		t.Fatalf("setor ausente %+v", snap)
	}
	bare := uuid.New()
	exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Solto')`, tenant, bare)
	if snap := inspectAt(bare); snap.Reason != triage.ReasonInboxUnlinked {
		t.Fatalf("sem vínculo %+v", snap)
	}
	realNobody := decide(newConv("P-1013", "open", "menu"), "1", live, openAt, uuid.Nil)
	if realNobody.Phase != triage.PhaseRouted || textOf(realNobody, triage.KindNobodyAvailable) != "Ninguém disponível." || !has(realNobody, triage.KindForwardHuman) || sectorOf(realNobody, triage.KindRouteSector) != support {
		t.Fatalf("ninguém real %+v", realNobody)
	}
	clocked := presence.New(store)
	clocked.Now = func() time.Time { return openAt }
	attendant := presence.Principal{SessionID: sessionID, UserID: user, TenantID: tenant, MembershipID: member, Role: "admin"}
	if _, err = clocked.Heartbeat(ctx, attendant, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if snap := inspectAt(support); snap.Reason != triage.ReasonReady || !snap.AutoAssign {
		t.Fatalf("disponível %+v", snap)
	}
	realReady := decide(newConv("P-1014", "open", "menu"), "1", live, openAt, uuid.Nil)
	if realReady.Phase != triage.PhaseRouted || has(realReady, triage.KindNobodyAvailable) || !has(realReady, triage.KindForwardHuman) || sectorOf(realReady, triage.KindRouteSector) != support {
		t.Fatalf("vaga real %+v", realReady)
	}
	exec(`INSERT INTO distribution_sector_auto(tenant_id,sector_id,enabled,version) VALUES($1,$2,false,1)`, tenant, support)
	if snap := inspectAt(support); snap.Reason != triage.ReasonReady || snap.AutoAssign {
		t.Fatalf("automática desligada %+v", snap)
	}
	exec(`DELETE FROM distribution_sector_auto WHERE tenant_id=$1 AND sector_id=$2`, tenant, support)
	exec(`INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,1)`, tenant, member)
	fullContact, fullIdentity, fullConv := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Lotado')`, fullContact, tenant)
	exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, fullIdentity, tenant, fullContact, connector, fullIdentity.String())
	exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,'P-1090','202611',1)`, fullConv, tenant, inbox, support, fullContact, fullIdentity, member)
	if snap := inspectAt(support); snap.Reason != triage.ReasonCapacityFull || !snap.AutoAssign {
		t.Fatalf("lotado %+v", snap)
	}
	realFull := decide(newConv("P-1015", "open", "menu"), "1", live, openAt, uuid.Nil)
	if has(realFull, triage.KindNobodyAvailable) || !has(realFull, triage.KindForwardHuman) || realFull.Phase != triage.PhaseRouted {
		t.Fatalf("lotado na decisão %+v", realFull)
	}
	exec(`DELETE FROM conversations WHERE id=$1`, fullConv)
	exec(`DELETE FROM assignment_capacities WHERE tenant_id=$1 AND membership_id=$2`, tenant, member)
	if _, err = clocked.SetManual(ctx, attendant, presence.ManualCommand{RequestID: uuid.New(), State: presence.ManualBusy}); err != nil {
		t.Fatal(err)
	}
	if snap := inspectAt(support); snap.Reason != triage.ReasonNobodyAvailable {
		t.Fatalf("ocupado %+v", snap)
	}
	busyRoute := decide(newConv("P-1016", "open", "menu"), "1", live, openAt, uuid.Nil)
	if textOf(busyRoute, triage.KindNobodyAvailable) != "Ninguém disponível." || !has(busyRoute, triage.KindForwardHuman) {
		t.Fatalf("ocupado na decisão %+v", busyRoute)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		view, err := triage.ReadDistributionQueue(ctx, tx, tenant, support, inbox, openAt)
		if err != nil {
			return err
		}
		if view.Reason != triage.ReasonReady {
			t.Fatalf("fila não informa ausência: %+v", view)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = clocked.SetManual(ctx, attendant, presence.ManualCommand{RequestID: uuid.New(), State: presence.ManualAvailable}); err != nil {
		t.Fatal(err)
	}

	watcher, err := pgx.Connect(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close(ctx)
	waitConversation := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			if err := watcher.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%triage-conversation%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatal("decisão não esperou o lock da conversa")
	}
	race := func(protocol string, mutate func(tx pgx.Tx, conversation uuid.UUID) error, epoch, version int64) {
		t.Helper()
		conversation := newConv(protocol, "open", "menu")
		hold, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = hold.Rollback(ctx)
			}
		}()
		var locked uuid.UUID
		if err = hold.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversation).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			var out triage.Outcome
			err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				out, err = triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: conversation, Text: "oi", Instant: openAt, InputID: uuid.New()})
				if err != nil {
					return err
				}
				if out.Phase != triage.PhaseSkipped || len(out.Actions) != 0 || out.ControllerEpoch != epoch || out.Version != version {
					return fmt.Errorf("decisão obsoleta: fase %s ações %d época %d versão %d", out.Phase, len(out.Actions), out.ControllerEpoch, out.Version)
				}
				return nil
			})
			done <- err
		}()
		waitConversation()
		if err = mutate(hold, conversation); err != nil {
			t.Fatal(err)
		}
		if err = hold.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		committed = true
		if err = <-done; err != nil {
			t.Fatal(err)
		}
		var decisions int
		if err = admin.QueryRow(ctx, `SELECT count(*) FROM triage_decisions WHERE conversation_id=$1`, conversation).Scan(&decisions); err != nil || decisions != 0 {
			t.Fatalf("decisão gravada para posse antiga %d %v", decisions, err)
		}
		exec(`UPDATE conversations SET controller='menu', assignee_id=NULL, status='closed' WHERE id=$1`, conversation)
	}
	race("P-1201", func(tx pgx.Tx, conversation uuid.UUID) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET controller='human', assignee_id=$2, controller_epoch=controller_epoch+1, version=version+1 WHERE id=$1`, conversation, member)
		return err
	}, 2, 2)
	race("P-1202", func(tx pgx.Tx, conversation uuid.UUID) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET status='closed', version=version+1 WHERE id=$1`, conversation)
		return err
	}, 1, 2)
	held := newConv("P-1301", "open", "menu")
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		out, err := triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: held, Text: "oi", Instant: openAt, InputID: uuid.New()})
		if err != nil {
			return err
		}
		if out.Phase != triage.PhaseMenu || out.Attempts != 0 || out.ControllerEpoch != 1 || out.Version != 1 || !has(out, triage.KindSendMenu) {
			t.Fatalf("época %+v", out)
		}
		rival, err := admin.Begin(ctx)
		if err != nil {
			return err
		}
		defer rival.Rollback(ctx)
		if _, err = rival.Exec(ctx, `SET LOCAL lock_timeout='200ms'`); err != nil {
			return err
		}
		_, err = rival.Exec(ctx, `UPDATE conversations SET summary='presa' WHERE id=$1`, held)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "55P03" {
			t.Fatalf("tomada não esperou o lock: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ambiguous := func(label, text string) {
		t.Helper()
		raw, err := json.Marshal([]triage.Option{
			{Key: "1", Label: label, Destination: triage.DestSector, SectorID: &financeID},
			{Key: "0", Label: "Atendente", Destination: triage.DestHuman},
		})
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE triage_menus SET options=$3 WHERE tenant_id=$1 AND inbox_id=$2`, tenant, inbox, raw)
		got := decide(newConv("P-"+label+"-"+text, "open", "menu"), text, ready, openAt, uuid.Nil)
		if got.Phase != triage.PhaseRouted || got.Attempts != 0 || sectorOf(got, triage.KindRouteSector) != support || sectorOf(got, triage.KindForwardHuman) != support || !has(got, triage.KindForwardHuman) || has(got, triage.KindRecordReason) {
			t.Fatalf("saída humana %s %q %+v", label, text, got)
		}
	}
	ambiguous("0", "0")
	ambiguous("humano", "humano")
	ambiguous("Atendente", "atendente")
	kept, err := json.Marshal(base.Options)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE triage_menus SET options=$3 WHERE tenant_id=$1 AND inbox_id=$2`, tenant, inbox, kept)
	removed, err := svc.Delete(ctx, principal, inbox, uuid.New(), 1)
	if err != nil || removed.Configured || removed.Version != 2 || len(removed.Options) != 0 {
		t.Fatalf("delete %+v %v", removed, err)
	}
	plain := decide(newConv("P-1010", "open", "menu"), "oi", ready, openAt, uuid.Nil)
	if plain.Phase != triage.PhaseRouted || plain.Attempts != 0 || has(plain, triage.KindSendMenu) || sectorOf(plain, triage.KindForwardHuman) != support || textOf(plain, triage.KindSendText) != "Olá P-1010" {
		t.Fatalf("sem menu %+v", plain)
	}
	skipped := decide(newConv("P-1011", "closed", "menu"), "oi", ready, openAt, uuid.Nil)
	if skipped.Phase != triage.PhaseSkipped || len(skipped.Actions) != 0 {
		t.Fatalf("encerrada %+v", skipped)
	}
	bot := decide(newConv("P-1012", "open", "bot"), "oi", ready, openAt, uuid.Nil)
	if bot.Phase != triage.PhaseSkipped || len(bot.Actions) != 0 {
		t.Fatalf("bot %+v", bot)
	}
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triage.Decide(ctx, tx, triage.Input{TenantID: uuid.New(), InboxID: inbox, ConversationID: choice, Text: "1", Instant: openAt, InputID: uuid.New()})
		if !errors.Is(err, sectors.ErrForbidden) {
			t.Fatalf("escopo %v", err)
		}
		_, err = triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: uuid.New(), Text: "1", Instant: openAt, InputID: uuid.New()})
		if !errors.Is(err, sectors.ErrNotFound) {
			t.Fatalf("ausente %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var messages, attempts, assignees int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1`, tenant).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("mensagens %d %v", messages, err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1`, tenant).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("saídas %d %v", attempts, err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversations WHERE tenant_id=$1 AND assignee_id IS NOT NULL`, tenant).Scan(&assignees); err != nil || assignees != 0 {
		t.Fatalf("responsável %d %v", assignees, err)
	}
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM distribution_cursors WHERE tenant_id=$1`, tenant).Scan(&cursors); err != nil || cursors != 0 {
		t.Fatalf("cursor final %d %v", cursors, err)
	}

	for _, table := range []string{"triage_menus", "triage_states", "triage_commands", "triage_decisions", "triage_events"} {
		var count int
		if err = pools.App.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("sem escopo %s %d %v", table, count, err)
		}
		err = store.WithTenantTx(ctx, uuid.New(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count)
		})
		if err != nil || count != 0 {
			t.Fatalf("outro tenant %s %d %v", table, count, err)
		}
	}

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
	api.Register(humachi.New(router, cfg), api.Dependencies{Auth: identity, Triage: svc, TrustedOrigins: []string{"https://panel.example"}})
	path := "/api/panel/inboxes/" + inbox.String() + "/triage-menu"
	request := func(method, path, session, origin, csrfToken string, body any, want int) []byte {
		t.Helper()
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrfToken)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: session})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	request("GET", path, "", "", "", nil, 401)
	body := request("GET", path, token, "", "", nil, 200)
	var seen triage.Menu
	if err = json.Unmarshal(body, &seen); err != nil || seen.Version != 2 || seen.Configured {
		t.Fatalf("get %+v %v %s", seen, err, body)
	}
	next := base
	next.RequestID = uuid.New()
	next.ExpectedVersion = 2
	request("PUT", path, token, "https://evil.example", csrf, next, 403)
	request("PUT", path, token, "https://panel.example", "wrong", next, 403)
	request("PUT", path, token, "https://panel.example", csrf, triage.Command{RequestID: uuid.New(), ExpectedVersion: 2, MaxAttempts: 9, Options: base.Options}, 422)
	body = request("PUT", path, token, "https://panel.example", csrf, next, 200)
	if err = json.Unmarshal(body, &seen); err != nil || seen.Version != 3 || !seen.Configured {
		t.Fatalf("http put %+v %v", seen, err)
	}
	request("PUT", path, token, "https://panel.example", csrf, next, 200)
	next.RequestID = uuid.New()
	request("PUT", path, token, "https://panel.example", csrf, next, 409)
	body = request("DELETE", path, token, "https://panel.example", csrf, map[string]any{"request_id": uuid.New(), "expected_version": 3}, 200)
	if err = json.Unmarshal(body, &seen); err != nil || seen.Configured || seen.Version != 4 {
		t.Fatalf("http delete %+v %v", seen, err)
	}
	for _, role := range []string{"supervisor", "agent"} {
		exec(`UPDATE memberships SET role=$1 WHERE tenant_id=$2 AND id=$3`, role, tenant, member)
		want := 200
		if role == "agent" {
			want = 403
		}
		request("GET", path, token, "", "", nil, want)
		request("PUT", path, token, "https://panel.example", csrf, next, 403)
		request("DELETE", path, token, "https://panel.example", csrf, map[string]any{"request_id": uuid.New(), "expected_version": 4}, 403)
	}
	exec(`UPDATE memberships SET role='admin',status='disabled' WHERE tenant_id=$1 AND id=$2`, tenant, member)
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE memberships SET status='active' WHERE tenant_id=$1 AND id=$2`, tenant, member)
	exec(`UPDATE tenants SET status='suspended' WHERE id=$1`, tenant)
	request("GET", path, token, "", "", nil, 403)
	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := triage.Decide(ctx, tx, triage.Input{TenantID: tenant, InboxID: inbox, ConversationID: choice, Text: "1", Instant: openAt, InputID: uuid.New()})
		return err
	})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatalf("suspenso %v", err)
	}
	exec(`UPDATE tenants SET status='active' WHERE id=$1`, tenant)
	exec(`UPDATE users SET totp_confirmed_at=NULL WHERE id=$1`, user)
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE users SET totp_confirmed_at=now(),is_superadmin=true WHERE id=$1`, user)
	request("GET", path, token, "", "", nil, 403)
	exec(`UPDATE sessions SET revoked_at=now() WHERE token_hash=$1`, auth.HashSecret(token))
	request("GET", path, token, "", "", nil, 401)
}

func has(out triage.Outcome, kind string) bool {
	for _, action := range out.Actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}

func textOf(out triage.Outcome, kind string) string {
	for _, action := range out.Actions {
		if action.Kind == kind {
			return action.Text
		}
	}
	return ""
}

func fallback(out triage.Outcome) string {
	for _, action := range out.Actions {
		if action.Kind == triage.KindSendMenu {
			return action.Fallback
		}
	}
	return ""
}

func sectorOf(out triage.Outcome, kind string) uuid.UUID {
	for _, action := range out.Actions {
		if action.Kind == kind && action.SectorID != nil {
			return *action.SectorID
		}
	}
	return uuid.Nil
}

func reasonOf(out triage.Outcome) string {
	for _, action := range out.Actions {
		if action.Kind == triage.KindRecordReason {
			return action.Reason
		}
	}
	return ""
}

func humanOption(out triage.Outcome) bool {
	for _, action := range out.Actions {
		for _, option := range action.Options {
			if option.Destination == triage.DestHuman {
				return true
			}
		}
	}
	return false
}
