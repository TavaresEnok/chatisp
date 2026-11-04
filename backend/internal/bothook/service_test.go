package bothook_test

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

	"chatisp/internal/bothook"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type scriptFake struct {
	calls int
	next  func() bothook.FakeResult
}

func (f *scriptFake) Handle(context.Context, bothook.Delivery) (bothook.FakeResult, error) {
	f.calls++
	if f.next == nil {
		return bothook.FakeResult{Received: true}, nil
	}
	return f.next(), nil
}

func TestParseToken(t *testing.T) {
	if _, _, err := bothook.ParseToken("v1.nao-e-uuid.segredo"); !errors.Is(err, bothook.ErrUnauthenticated) {
		t.Fatal(err)
	}
}

func TestBotHookPostgres(t *testing.T) {
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
			t.Fatalf("%s: %v", q, err)
		}
	}
	tenant, other := uuid.New(), uuid.New()
	exec(`INSERT INTO tenants(id,timezone,status) VALUES($1,'America/Sao_Paulo','active'),($2,'UTC','active')`, tenant, other)
	clock := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	var seq int
	type actor struct {
		user, member, session uuid.UUID
		token, csrf           string
		p                     bothook.Principal
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
		a.p = bothook.Principal{UserID: a.user, TenantID: tenantID, MembershipID: a.member, Role: role}
		far := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := store.WithAuth(ctx, func(ctx context.Context) error {
			return store.InsertSession(ctx, db.NewSession{ID: a.session, TokenHash: auth.HashSecret(a.token), UserID: a.user, TenantID: tenantID, MembershipID: a.member, CSRFHash: auth.HashSecret(a.csrf), Now: clock, IdleExpiresAt: far, AbsoluteExpiresAt: far})
		}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	adminA := makeActor(tenant, "admin", "admin-hook@example.test", true)
	agent := makeActor(tenant, "agent", "agent-hook@example.test", false)
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
	var n int
	openMenu := func() uuid.UUID {
		t.Helper()
		n++
		id, contact, identity := uuid.New(), uuid.New(), uuid.New()
		exec(`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'C')`, contact, tenant)
		exec(`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, contact, home.connector, identity.String())
		exec(`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'open','menu',$7,'202611',1,$8,$8)`, id, tenant, home.inbox, home.sector, contact, identity, fmt.Sprintf("B-%d", n), clock)
		exec(`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,$4)`, tenant, uuid.New(), id, clock)
		return id
	}
	readConv := func(id uuid.UUID) (controller, status string, epoch int64, assignee *uuid.UUID) {
		t.Helper()
		if err := admin.QueryRow(ctx, `SELECT controller, status, controller_epoch, assignee_id FROM conversations WHERE id=$1`, id).Scan(&controller, &status, &epoch, &assignee); err != nil {
			t.Fatal(err)
		}
		return
	}
	fake := &scriptFake{}
	svc := bothook.New(store)
	svc.Now = func() time.Time { return clock }
	svc.Fake = fake

	installID := uuid.New()
	inst, err := svc.Install(ctx, adminA.p, home.inbox, bothook.InstallCommand{RequestID: installID, DeadlineSeconds: 60})
	if err != nil || !inst.TokenRevealed || inst.Token == "" || inst.Simulation != bothook.SimulationInProcess || !inst.Enabled {
		t.Fatalf("install %+v %v", inst, err)
	}
	tenantID, secret, err := bothook.ParseToken(inst.Token)
	if err != nil || tenantID != tenant || secret == "" || secret == inst.TokenPrefix {
		t.Fatalf("token %v prefix %s", err, inst.TokenPrefix)
	}
	var stored []byte
	if err = admin.QueryRow(ctx, `SELECT token_hash FROM bot_hooks WHERE tenant_id=$1 AND inbox_id=$2`, tenant, home.inbox).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !auth.SecretEqual(stored, auth.HashSecret(secret)) {
		t.Fatal("hash do token")
	}
	replay, err := svc.Install(ctx, adminA.p, home.inbox, bothook.InstallCommand{RequestID: installID, DeadlineSeconds: 60})
	if err != nil || replay.Token != "" || replay.TokenRevealed || replay.TokenPrefix != inst.TokenPrefix {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if _, err = svc.Install(ctx, adminA.p, home.inbox, bothook.InstallCommand{RequestID: uuid.New(), DeadlineSeconds: 60}); !errors.Is(err, bothook.ErrConflict) {
		t.Fatal(err)
	}
	rotateID := uuid.New()
	rotated, err := svc.Rotate(ctx, adminA.p, home.inbox, bothook.RotateCommand{RequestID: rotateID, ExpectedVersion: inst.Version})
	if err != nil || !rotated.TokenRevealed || rotated.Token == inst.Token {
		t.Fatalf("rotate %+v %v", rotated, err)
	}
	repeated, err := svc.Rotate(ctx, adminA.p, home.inbox, bothook.RotateCommand{RequestID: rotateID, ExpectedVersion: inst.Version})
	if err != nil || repeated.Token != "" || repeated.TokenRevealed || repeated.TokenPrefix != rotated.TokenPrefix {
		t.Fatalf("replay da rotação %+v %v", repeated, err)
	}
	token := rotated.Token

	quiet := openMenu()
	session, err := svc.Offer(ctx, tenant, quiet)
	if err != nil || session.State != "received" || session.Receipt != "received" || session.Notice == "" {
		t.Fatalf("recibo %+v %v", session, err)
	}
	if fake.calls != 1 {
		t.Fatalf("fake %d", fake.calls)
	}
	if _, err = svc.Offer(ctx, tenant, quiet); err != nil || fake.calls != 1 {
		t.Fatalf("segunda oferta %d %v", fake.calls, err)
	}
	controller, status, epoch, assignee := readConv(quiet)
	if controller != "bot" || status != "triage" || assignee != nil || epoch != session.GrantedEpoch {
		t.Fatalf("posse %s %s %d %v", controller, status, epoch, assignee)
	}
	if _, err = svc.Reply(ctx, inst.Token, quiet, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: session.GrantedEpoch, Body: "token antigo"}); !errors.Is(err, bothook.ErrUnauthenticated) {
		t.Fatalf("token antigo %v", err)
	}
	if released, err := svc.ExpireDue(ctx, tenant, session.DeadlineAt.Add(-time.Second)); err != nil || released != 0 {
		t.Fatalf("antes do prazo %d %v", released, err)
	}
	if released, err := svc.ExpireDue(ctx, tenant, session.DeadlineAt); err != nil || released != 1 {
		t.Fatalf("prazo %d %v", released, err)
	}
	controller, status, _, assignee = readConv(quiet)
	if controller != "menu" || status != "open" || assignee != nil {
		t.Fatalf("fila após prazo %s %s %v", controller, status, assignee)
	}
	var gotState, gotReceipt string
	if err = admin.QueryRow(ctx, `SELECT state, COALESCE(receipt,'') FROM bot_sessions WHERE id=$1`, session.ID).Scan(&gotState, &gotReceipt); err != nil {
		t.Fatal(err)
	}
	if gotState != "expired" || gotReceipt != "received" {
		t.Fatalf("recibo não é resolução %s %s", gotState, gotReceipt)
	}

	progress := openMenu()
	progressSession, err := svc.Offer(ctx, tenant, progress)
	if err != nil {
		t.Fatal(err)
	}
	originalDeadline := progressSession.DeadlineAt
	clock = clock.Add(30 * time.Second)
	replyID := uuid.New()
	reply, err := svc.Reply(ctx, token, progress, bothook.ReplyCommand{RequestID: replyID, Epoch: progressSession.GrantedEpoch, Body: "  posso ajudar  "})
	if err != nil || reply.State != "queued" || reply.Epoch != progressSession.GrantedEpoch {
		t.Fatalf("resposta %+v %v", reply, err)
	}
	repeatedReply, err := svc.Reply(ctx, token, progress, bothook.ReplyCommand{RequestID: replyID, Epoch: progressSession.GrantedEpoch, Body: "posso ajudar"})
	if err != nil || repeatedReply.MessageID != reply.MessageID {
		t.Fatalf("idempotência %+v %v", repeatedReply, err)
	}
	if _, err = svc.Reply(ctx, token, progress, bothook.ReplyCommand{RequestID: replyID, Epoch: progressSession.GrantedEpoch, Body: "outro texto"}); !errors.Is(err, bothook.ErrConflict) {
		t.Fatal(err)
	}
	var messages int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND origin='bot' AND visibility='public'`, progress).Scan(&messages); err != nil || messages != 1 {
		t.Fatalf("mensagens %d %v", messages, err)
	}
	if released, err := svc.ExpireDue(ctx, tenant, originalDeadline); err != nil || released != 0 {
		t.Fatalf("resposta estendeu o prazo %d %v", released, err)
	}

	held := openMenu()
	exec(`UPDATE conversations SET controller='human', assignee_id=$2, version=version+1 WHERE id=$1`, held, agent.member)
	_, _, beforeEpoch, _ := readConv(held)
	if _, err = svc.Offer(ctx, tenant, held); !errors.Is(err, bothook.ErrConflict) {
		t.Fatal(err)
	}
	if controller, _, epoch, _ = readConv(held); controller != "human" || epoch != beforeEpoch {
		t.Fatalf("humano perdeu a posse %s %d", controller, epoch)
	}

	fake.next = func() bothook.FakeResult { return bothook.FakeResult{Action: "uncertain"} }
	unsure := openMenu()
	unsureSession, err := svc.Offer(ctx, tenant, unsure)
	if err != nil || unsureSession.State != "uncertain" || unsureSession.Notice != "Resultado do gancho incerto. A conversa voltou para a fila humana." {
		t.Fatalf("incerto %+v %v", unsureSession, err)
	}
	controller, status, _, assignee = readConv(unsure)
	if controller != "menu" || status != "open" || assignee != nil {
		t.Fatalf("recuperação %s %s %v", controller, status, assignee)
	}
	notices, err := svc.Notices(ctx, adminA.p, home.inbox)
	if err != nil {
		t.Fatal(err)
	}
	var sawUncertain bool
	for _, item := range notices.Notices {
		if item.ID == unsureSession.ID && item.State == "uncertain" && item.Notice == unsureSession.Notice {
			sawUncertain = true
		}
	}
	if !sawUncertain {
		t.Fatalf("aviso ausente %+v", notices.Notices)
	}

	fake.next = func() bothook.FakeResult { return bothook.FakeResult{Received: false} }
	down := openMenu()
	downSession, err := svc.Offer(ctx, tenant, down)
	if err != nil || downSession.State != "unavailable" {
		t.Fatalf("indisponível %+v %v", downSession, err)
	}
	if controller, _, _, _ = readConv(down); controller != "menu" {
		t.Fatal(controller)
	}

	fake.next = nil
	queuedConv := openMenu()
	queuedSession, err := svc.Offer(ctx, tenant, queuedConv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Reply(ctx, token, queuedConv, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: queuedSession.GrantedEpoch, Body: "ainda automático"}); err != nil {
		t.Fatal(err)
	}
	sw, err := svc.SetSwitch(ctx, adminA.p, home.inbox, bothook.SwitchCommand{RequestID: uuid.New(), ExpectedVersion: rotated.Version, Enabled: false})
	if err != nil || sw.Enabled || sw.Released < 1 {
		t.Fatalf("interruptor %+v %v", sw, err)
	}
	var attempt, local string
	if err = admin.QueryRow(ctx, `SELECT a.state, m.local_state FROM outbound_attempts a JOIN messages m ON m.tenant_id=a.tenant_id AND m.id=a.message_id WHERE a.conversation_id=$1 AND m.body='ainda automático'`, queuedConv).Scan(&attempt, &local); err != nil {
		t.Fatal(err)
	}
	if attempt != "cancelled" || local != "cancelled" {
		t.Fatalf("cancelamento %s %s", attempt, local)
	}
	if controller, status, epoch, assignee = readConv(queuedConv); controller != "menu" || status != "open" || assignee != nil || epoch == queuedSession.GrantedEpoch {
		t.Fatalf("fila do interruptor %s %s %d", controller, status, epoch)
	}
	if _, err = svc.Reply(ctx, token, queuedConv, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: queuedSession.GrantedEpoch, Body: "tarde"}); !errors.Is(err, bothook.ErrEpoch) && !errors.Is(err, bothook.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err = svc.Offer(ctx, tenant, openMenu()); !errors.Is(err, bothook.ErrDisabled) {
		t.Fatal(err)
	}
	on, err := svc.SetSwitch(ctx, adminA.p, home.inbox, bothook.SwitchCommand{RequestID: uuid.New(), ExpectedVersion: sw.Version, Enabled: true})
	if err != nil || !on.Enabled || on.Released != 0 {
		t.Fatalf("ligar %+v %v", on, err)
	}

	empty := uuid.New()
	exec(`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Plantão')`, tenant, empty)
	exec(`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3)`, tenant, empty, home.inbox)
	hand := openMenu()
	handSession, err := svc.Offer(ctx, tenant, hand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Reply(ctx, token, hand, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: handSession.GrantedEpoch, Body: "antes do handoff"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Handoff(ctx, token, hand, bothook.HandoffCommand{RequestID: uuid.New(), Epoch: handSession.GrantedEpoch, SectorID: uuid.New(), Reason: "financeiro", Note: "nota"}); !errors.Is(err, bothook.ErrInvalid) {
		t.Fatal(err)
	}
	if controller, _, _, _ = readConv(hand); controller != "bot" {
		t.Fatal("setor inválido moveu a conversa")
	}
	handoffID := uuid.New()
	done, err := svc.Handoff(ctx, token, hand, bothook.HandoffCommand{RequestID: handoffID, Epoch: handSession.GrantedEpoch, SectorID: empty, Reason: "  financeiro  ", Note: "nota interna"})
	if err != nil || done.Controller != "menu" || done.Status != "open" || done.AssigneeID != nil || done.SectorID != empty {
		t.Fatalf("handoff %+v %v", done, err)
	}
	againHandoff, err := svc.Handoff(ctx, token, hand, bothook.HandoffCommand{RequestID: handoffID, Epoch: handSession.GrantedEpoch, SectorID: empty, Reason: "financeiro", Note: "nota interna"})
	if err != nil || againHandoff.HandoffID != done.HandoffID {
		t.Fatalf("retry %+v %v", againHandoff, err)
	}
	var events int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE conversation_id=$1 AND kind='controller_changed'`, hand).Scan(&events); err != nil || events != 1 {
		t.Fatalf("eventos %d %v", events, err)
	}
	if err = admin.QueryRow(ctx, `SELECT a.state FROM outbound_attempts a JOIN messages m ON m.tenant_id=a.tenant_id AND m.id=a.message_id WHERE a.conversation_id=$1 AND m.body='antes do handoff'`, hand).Scan(&attempt); err != nil || attempt != "cancelled" {
		t.Fatalf("fila do handoff %s %v", attempt, err)
	}
	next, err := svc.Offer(ctx, tenant, hand)
	if err != nil || next.GrantedEpoch == handSession.GrantedEpoch {
		t.Fatalf("nova posse %+v %v", next, err)
	}
	if _, err = svc.Reply(ctx, token, hand, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: handSession.GrantedEpoch, Body: "epoch antiga"}); !errors.Is(err, bothook.ErrEpoch) {
		t.Fatal(err)
	}
	if _, err = svc.Reply(ctx, token, hand, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: next.GrantedEpoch, Body: "epoch vigente"}); err != nil {
		t.Fatal(err)
	}

	err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', '', true)`); err != nil {
			return err
		}
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM bot_hooks`).Scan(&visible); err != nil || visible != 0 {
			return fmt.Errorf("RLS sem escopo %d %v", visible, err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, other.String()); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM bot_sessions`).Scan(&visible); err != nil || visible != 0 {
			return fmt.Errorf("RLS outro tenant %d %v", visible, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: store, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(httpapi.Dependencies{Auth: identity, BotHook: svc, TrustedOrigins: []string{"https://panel.example"}})
	call := func(method, path, cookie, origin, csrf, bearer string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: cookie})
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		api.Handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	call(http.MethodPut, "/api/panel/bot-hook/inboxes/"+home.inbox.String(), "", "https://panel.example", adminA.csrf, "", bothook.InstallCommand{RequestID: uuid.New(), DeadlineSeconds: 60}, http.StatusUnauthorized)
	call(http.MethodPut, "/api/panel/bot-hook/inboxes/"+home.inbox.String(), agent.token, "https://panel.example", agent.csrf, "", bothook.InstallCommand{RequestID: uuid.New(), DeadlineSeconds: 60}, http.StatusForbidden)
	body := call(http.MethodGet, "/api/panel/bot-hook/notices?inbox_id="+home.inbox.String(), adminA.token, "", "", "", nil, http.StatusOK)
	var listed bothook.NoticeList
	if err = json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	var httpUncertain bool
	for _, item := range listed.Notices {
		if item.State == "uncertain" && item.Notice == "Resultado do gancho incerto. A conversa voltou para a fila humana." {
			httpUncertain = true
		}
	}
	if !httpUncertain {
		t.Fatalf("painel %+v", listed.Notices)
	}
	live := openMenu()
	liveSession, err := svc.Offer(ctx, tenant, live)
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/api/bot-hook/conversations/"+live.String()+"/reply", "", "", "", token, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: liveSession.GrantedEpoch, Body: "pelo http"}, http.StatusOK)
	call(http.MethodPost, "/api/bot-hook/conversations/"+live.String()+"/reply", "", "", "", token, bothook.ReplyCommand{RequestID: uuid.New(), Epoch: liveSession.GrantedEpoch - 1, Body: "epoch antiga"}, http.StatusConflict)
}
