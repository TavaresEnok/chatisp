package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/service"
	"chatisp/internal/channel/whatsapp"
	"chatisp/internal/channel/whatsapp/metacloud"
	"chatisp/internal/channelpolicy"
	"chatisp/internal/sectors"
)

func TestDispatchFenceRevalidatesPolicyBeforeDriver(t *testing.T) {
	cases := []struct {
		name   string
		phone  string
		when   string
		mutate string
		code   string
	}{
		{name: "janela após reserva", phone: "5511977712101", when: "start", mutate: "window", code: "window_closed"},
		{name: "janela durante preparação", phone: "5511977712102", when: "after", mutate: "window", code: "window_closed"},
		{name: "opt-out durante preparação", phone: "5511977712103", when: "after", mutate: "consent", code: "consent_required"},
		{name: "template revogado durante preparação", phone: "5511977712104", when: "after", mutate: "template", code: "template_not_authorized"},
		{name: "idioma do catálogo mudou durante preparação", phone: "5511977712105", when: "after", mutate: "language", code: "template_not_authorized"},
		{name: "idioma gravado inválido durante preparação", phone: "5511977712106", when: "after", mutate: "bad-language", code: "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, _, contactID, attemptID := seedPolicyAttempt(t, tc.phone, tc.mutate)
			driver := &recordingDriver{local: whatsapp.StateAccepted, providerID: "wamid.nao-chamar"}
			factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
			if err != nil {
				t.Fatal(err)
			}
			change := func() {
				if err := mutatePolicy(ctx, st, contactID, tc.mutate); err != nil {
					t.Fatalf("mudança de política: %v", err)
				}
			}
			resolver := &delayedMaterialResolver{underlying: testConfigServer}
			if tc.when == "start" {
				resolver.onResolve = change
			} else {
				resolver.afterResolve = change
			}
			err = NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attemptID)
			if err != nil {
				t.Fatalf("despacho: %v", err)
			}
			if driver.CallCount() != 0 {
				t.Fatalf("driver chamado após política negada: %d", driver.CallCount())
			}
			state, code := readAttempt(t, ctx, st.tenantID, attemptID)
			if state != "failed" || code != tc.code {
				t.Fatalf("tentativa %s/%s, esperado failed/%s", state, code, tc.code)
			}
			var decisions int
			if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `
					SELECT count(*) FROM dispatch_policy_decisions
					 WHERE tenant_id=$1 AND attempt_id=$2 AND phase='dispatch'
					   AND allowed=false AND reason=$3 AND lease_version >= 1`,
					st.tenantID, attemptID, tc.code).Scan(&decisions)
			}); err != nil || decisions != 1 {
				t.Fatalf("decisão durável: %d %v", decisions, err)
			}
		})
	}
}

func TestDispatchTemplateReachesIdentifiedDriver(t *testing.T) {
	ctx := context.Background()
	st, _, _, attemptID := seedPolicyAttempt(t, "5511977712201", "template")
	fake := whatsapp.NewFake()
	probe := &templateProbe{inner: fake}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: probe})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attemptID); err != nil {
		t.Fatal(err)
	}
	if probe.textCalls != 0 || probe.templateCalls != 1 {
		t.Fatalf("roteamento texto=%d template=%d", probe.textCalls, probe.templateCalls)
	}
	if probe.last.TemplateID != "tpl-cerca" || probe.last.Language != "pt_BR" {
		t.Fatalf("request: %#v", probe.last)
	}
	caps := fake.Captures()
	if len(caps) != 1 || caps[0].TemplateID != "tpl-cerca" || caps[0].Language != "pt_BR" || !caps[0].Result.Simulation {
		t.Fatalf("fake: %#v", caps)
	}
	state, _ := readAttempt(t, ctx, st.tenantID, attemptID)
	if state != "accepted" {
		t.Fatalf("estado %s", state)
	}
}

func TestDispatchTemplateReachesOfficialRefusal(t *testing.T) {
	ctx := context.Background()
	st, _, _, attemptID := seedPolicyAttempt(t, "5511977712202", "template")
	official, err := metacloud.Activate(metacloud.Config{
		Mode:          metacloud.ModeOfficial,
		GraphVersion:  "v23.0",
		PrimaryDocURL: "https://developers.facebook.com/docs/whatsapp/cloud-api/messages",
		PhoneNumberID: "106540352242922",
		AccessToken:   "secret-token-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := &templateProbe{inner: official}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: probe})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewOutboundHandler(testStore, testAttendanceSvc, testConfigServer, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attemptID); err != nil {
		t.Fatal(err)
	}
	if probe.textCalls != 0 || probe.templateCalls != 1 {
		t.Fatalf("roteamento texto=%d template=%d", probe.textCalls, probe.templateCalls)
	}
	if probe.last.Language != "pt_BR" || probe.last.TemplateID != "tpl-cerca" {
		t.Fatalf("request: %#v", probe.last)
	}
	state, code := readAttempt(t, ctx, st.tenantID, attemptID)
	if state != "failed" || code != "template_not_authorized" {
		t.Fatalf("recusa oficial: %s/%s", state, code)
	}
}

type templateProbe struct {
	textCalls     int
	templateCalls int
	last          whatsapp.TemplateRequest
	inner         whatsapp.TemplateSender
}

func (p *templateProbe) Capabilities() whatsapp.Capabilities {
	return whatsapp.Capabilities{Text: true}
}

func (p *templateProbe) SendText(_ context.Context, _ whatsapp.TextRequest) (whatsapp.Result, error) {
	p.textCalls++
	return whatsapp.Result{Local: whatsapp.StateFailed, RejectionCode: "texto_indevido", Detail: "template não pode ir como texto"}, nil
}

func (p *templateProbe) SendTemplate(ctx context.Context, req whatsapp.TemplateRequest) (whatsapp.Result, error) {
	p.templateCalls++
	p.last = req
	return p.inner.SendTemplate(ctx, req)
}

func seedPolicyAttempt(t *testing.T, phone, kind string) (seededTenant, service.Principal, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	st := seedTenantWithMeta(t, "app-policy-"+phone, "waba-policy-"+phone, phone, "tok-policy-"+phone, "secret-policy-"+phone)
	principal := service.Principal{UserID: st.userID, MembershipID: st.memberID, TenantID: st.tenantID, Role: "admin"}
	contactID := uuid.New()
	identityID := uuid.New()
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, tenant_id, name) VALUES ($1, $2, 'Cliente Política')`, contactID, st.tenantID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (id, tenant_id, contact_id, connector_id, external_id) VALUES ($1,$2,$3,$4,$5)`,
			identityID, st.tenantID, contactID, st.connectorID, phone)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	received, err := testAttendanceSvc.ReceiveText(ctx, principal, service.ReceiveInput{
		InboxID: st.inboxID, ContactID: contactID, IdentityID: identityID, ConnectorID: st.connectorID,
		ExternalMessageID: "wamid.policy." + phone, Body: "oi", Period: time.Now().UTC().Format("200601"),
		TrustedAt: time.Now().Add(-10 * time.Minute), OpenedAt: time.Now().Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := testAttendanceSvc.Assume(ctx, principal, st.inboxID, received.Conversation.ID, received.Conversation.Version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT version FROM conversations WHERE tenant_id=$1 AND id=$2`, st.tenantID, conv.ID).Scan(&version)
	}); err != nil {
		t.Fatal(err)
	}
	var attemptID uuid.UUID
	if kind == "window" {
		sent, err := testAttendanceSvc.Send(ctx, principal, service.SendInput{
			InboxID: st.inboxID, ConversationID: conv.ID, ExpectedVersion: version,
			ClientRequestID: "req-policy-" + phone, Body: "resposta", At: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		attemptID = sent.AttemptID
		return st, principal, contactID, attemptID
	}
	if err := grantTemplate(ctx, st, principal, contactID); err != nil {
		t.Fatal(err)
	}
	sent, err := testAttendanceSvc.SendTemplate(ctx, principal, service.TemplateSendInput{
		InboxID: st.inboxID, ConversationID: conv.ID, ExpectedVersion: version,
		ClientRequestID: "req-tpl-" + phone, TemplateID: "tpl-cerca", Language: "pt_BR", Purpose: "cobranca", At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, principal, contactID, sent.AttemptID
}

func grantTemplate(ctx context.Context, st seededTenant, principal service.Principal, contactID uuid.UUID) error {
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO wa_templates (tenant_id, waba_id, external_id, name, language, category, state, components, sync_source, synced_at)
			VALUES ($1, $2, 'tpl-cerca', 'cerca', 'pt_BR', 'utility', 'APPROVED', '[]'::jsonb, 'driver', clock_timestamp())`,
			st.tenantID, st.wabaRowID)
		return err
	}); err != nil {
		return err
	}
	policy := channelpolicy.Service{DB: testStore}
	_, err := policy.RecordConsent(ctx, sectors.Principal{
		TenantID: st.tenantID, UserID: principal.UserID, MembershipID: principal.MembershipID, Role: principal.Role,
	}, channelpolicy.ConsentInput{
		Scope:    channelpolicy.Scope{InboxID: st.inboxID, ContactID: contactID, Purpose: "cobranca"},
		Source:   "painel",
		Evidence: "aceite do contato",
	})
	return err
}

func mutatePolicy(ctx context.Context, st seededTenant, contactID uuid.UUID, kind string) error {
	switch kind {
	case "window":
		return testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE messages SET trusted_at = clock_timestamp() - interval '25 hours' WHERE tenant_id=$1 AND direction='inbound'`, st.tenantID)
			return err
		})
	case "template":
		return testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE wa_templates SET state='PAUSED' WHERE tenant_id=$1 AND external_id='tpl-cerca'`, st.tenantID)
			return err
		})
	case "language":
		return testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE wa_templates SET language='en_US' WHERE tenant_id=$1 AND external_id='tpl-cerca'`, st.tenantID)
			return err
		})
	case "bad-language":
		return testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE messages SET content_context = jsonb_set(content_context, '{language}', '"bad lang"'::jsonb) WHERE tenant_id=$1 AND content_kind='template'`, st.tenantID)
			return err
		})
	default:
		policy := channelpolicy.Service{DB: testStore}
		_, err := policy.RecordConsent(ctx, sectors.Principal{
			TenantID: st.tenantID, UserID: st.userID, MembershipID: st.memberID, Role: "admin",
		}, channelpolicy.ConsentInput{
			Scope:    channelpolicy.Scope{InboxID: st.inboxID, ContactID: contactID, Purpose: "cobranca"},
			Source:   "painel",
			Evidence: "revogação do contato",
			Revoke:   true,
		})
		return err
	}
}

// TestDispatchFenceOverlapsAcceptanceWithoutDeadlock holds the conversation
// until the final fence has the connector, then starts a real text acceptance.
// The acceptance must wait on that fence, not on the conversation. Both finish
// after the barrier, the queued attempt is accepted, and nothing is left uncertain.
func TestDispatchFenceOverlapsAcceptanceWithoutDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	phone := "5511977712301"
	st, principal, _, attemptID := seedPolicyAttempt(t, phone, "window")
	var conversationID uuid.UUID
	var version int64
	if err := testStore.WithTenantTx(ctx, st.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT c.id, c.version FROM outbound_attempts o JOIN conversations c ON c.tenant_id=o.tenant_id AND c.id=o.conversation_id WHERE o.tenant_id=$1 AND o.id=$2`, st.tenantID, attemptID).Scan(&conversationID, &version)
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, testEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	ready, releasePrep := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releasePrep) }) }
	defer release()
	resolver := &delayedMaterialResolver{underlying: testConfigServer, afterResolve: func() {
		close(ready)
		select {
		case <-releasePrep:
		case <-ctx.Done():
		}
	}}
	driver := &gateDriver{entered: make(chan struct{}), release: make(chan struct{})}
	factory, err := NewDriverFactory(DriverFactoryConfig{Simulation: true, FakeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- NewOutboundHandler(testStore, testAttendanceSvc, resolver, testConfigServer, factory).DispatchOutbound(ctx, st.tenantID, attemptID)
	}()
	select {
	case <-ready:
	case err := <-dispatchDone:
		t.Fatalf("despacho terminou na preparação: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	var holderPID int
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	var locked uuid.UUID
	if err := holder.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	release()
	fencePID := waitBlockedByPID(t, ctx, admin, holderPID, dispatchDone)
	acceptDone := make(chan error, 1)
	go func() {
		_, err := testAttendanceSvc.Send(ctx, principal, service.SendInput{
			InboxID: st.inboxID, ConversationID: conversationID, ExpectedVersion: version,
			ClientRequestID: "req-overlap-" + phone, Body: "segunda resposta", At: time.Now(),
		})
		acceptDone <- err
	}()
	waitBlockedByPID(t, ctx, admin, fencePID, acceptDone)
	select {
	case err := <-dispatchDone:
		t.Fatalf("cerca terminou durante a barreira: %v", err)
	case err := <-acceptDone:
		t.Fatalf("aceite terminou durante a barreira: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-driver.entered:
	case err := <-dispatchDone:
		t.Fatalf("despacho não chegou ao driver: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("aceite: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(driver.release)
	select {
	case err := <-dispatchDone:
		if err != nil {
			t.Fatalf("despacho: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if driver.calls() != 1 {
		t.Fatalf("chamadas do driver: %d", driver.calls())
	}
	state, code := readAttempt(t, ctx, st.tenantID, attemptID)
	if state != "accepted" || code != "" {
		t.Fatalf("tentativa %s/%s, incerto espúrio se o estado não for accepted", state, code)
	}
}

func waitBlockedByPID[T any](t *testing.T, ctx context.Context, db *pgx.Conn, blocker int, done <-chan T) int {
	t.Helper()
	for {
		var pid int
		err := db.QueryRow(ctx, `
			SELECT pid FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> $1 AND $1 = ANY(pg_blocking_pids(pid))
			 ORDER BY pid LIMIT 1`, blocker).Scan(&pid)
		if err == nil && pid > 0 {
			return pid
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case result := <-done:
			t.Fatalf("operação terminou antes de esperar o backend %d: %+v", blocker, result)
		case <-ctx.Done():
			t.Fatalf("sem espera pelo backend %d: %v", blocker, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type gateDriver struct {
	mu      sync.Mutex
	n       int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *gateDriver) Capabilities() whatsapp.Capabilities {
	return whatsapp.Capabilities{Text: true}
}

func (d *gateDriver) SendText(ctx context.Context, req whatsapp.TextRequest) (whatsapp.Result, error) {
	d.mu.Lock()
	d.n++
	d.mu.Unlock()
	d.once.Do(func() { close(d.entered) })
	select {
	case <-d.release:
	case <-ctx.Done():
		return whatsapp.Result{}, ctx.Err()
	}
	return whatsapp.Result{
		AttemptID: req.AttemptID, Local: whatsapp.StateAccepted, ProviderMessageID: "wamid.overlap", Simulation: true,
	}, nil
}

func (d *gateDriver) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

func readAttempt(t *testing.T, ctx context.Context, tenantID, attemptID uuid.UUID) (string, string) {
	t.Helper()
	var state, code string
	err := testStore.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state, COALESCE(error_code, '') FROM outbound_attempts WHERE tenant_id=$1 AND id=$2`, tenantID, attemptID).Scan(&state, &code)
	})
	if err != nil {
		t.Fatal(err)
	}
	return state, code
}
