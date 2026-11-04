package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/platform/db"
)

type stubQueuer struct{}

func (stubQueuer) EnqueueOutbound(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (int64, error) {
	return 1, nil
}

func TestPublicTemplateUsesConsentAndIdempotency(t *testing.T) {
	ctx := context.Background()
	tenant, user, member := uuid.New(), uuid.New(), uuid.New()
	inbox, connector, sector := uuid.New(), uuid.New(), uuid.New()
	conversation, contact, identity := uuid.New(), uuid.New(), uuid.New()
	waba, app := uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: member, UserID: user, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		steps := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled,version) VALUES($1,$2,'meta_cloud',$3,'connected',true,'1')`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'WhatsApp')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, member}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,version) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,$8,'202611',1,1)`, []any{conversation, tenant, inbox, sector, contact, identity, member, "T-" + conversation.String()[:8]}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{tenant, uuid.New(), waba, uuid.NewString(), connector, inbox}},
			{`INSERT INTO wa_templates(tenant_id,waba_id,external_id,name,language,category,state,components,sync_source,synced_at) VALUES($1,$2,'tpl-api','tpl','pt_BR','UTILITY','APPROVED','[]','driver',now())`, []any{tenant, waba}},
		}
		for _, step := range steps {
			if _, err := tx.Exec(ctx, step.q, step.a...); err != nil {
				return fmt.Errorf("%s: %w", step.q, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secret, _, err := IssueScoped(ctx, testStore, tenant, []string{"templates.send"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	api := Mount(testStore, attendanceService.New(testStore, stubQueuer{}))
	body := []byte(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"tpl-1","expected_version":1}`)
	denied := postTemplate(t, api, conversation, secret, body)
	if denied.Code != http.StatusUnprocessableEntity || !bytes.Contains(denied.Body.Bytes(), []byte("consent_required")) {
		t.Fatalf("sem consentimento: %d %s", denied.Code, denied.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 0 {
		t.Fatal("recusa de consentimento gravou tentativa")
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contact_consents(tenant_id,id,contact_id,inbox_id,purpose,source,evidence,granted_at) VALUES($1,$2,$3,$4,'atendimento','teste','aceite',now())`, tenant, uuid.New(), contact, inbox)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first := postTemplate(t, api, conversation, secret, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("envio %d %s", first.Code, first.Body.String())
	}
	var sent templateResult
	if err := json.Unmarshal(first.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Replayed || sent.AttemptID == uuid.Nil || sent.JobID != 1 {
		t.Fatalf("resultado %+v", sent)
	}
	if countAttempts(t, tenant, conversation) != 1 {
		t.Fatal("tentativa não ficou única")
	}
	again := postTemplate(t, api, conversation, secret, body)
	if again.Code != http.StatusOK {
		t.Fatalf("replay %d %s", again.Code, again.Body.String())
	}
	var replay templateResult
	if err := json.Unmarshal(again.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.AttemptID != sent.AttemptID || replay.MessageID != sent.MessageID {
		t.Fatalf("replay %+v primeiro %+v", replay, sent)
	}
	if countAttempts(t, tenant, conversation) != 1 {
		t.Fatal("replay criou outra tentativa")
	}
}

func postTemplate(t *testing.T, api http.Handler, conversation uuid.UUID, secret string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conversation.String()+"/template", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	return rec
}

func TestPublicTemplateUsesReadVersionAndStableKey(t *testing.T) {
	ctx := context.Background()
	tenant, userA, userB := uuid.New(), uuid.New(), uuid.New()
	memberA, memberB := uuid.New(), uuid.New()
	inbox, connector, sector := uuid.New(), uuid.New(), uuid.New()
	conversation, contact, identity := uuid.New(), uuid.New(), uuid.New()
	waba, app := uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		if _, err := testStore.CreateUser(ctx, db.NewUser{ID: userA, Email: userA.String() + "@example.com", Status: "active"}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: userB, Email: userB.String() + "@example.com", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, tenant, func(ctx context.Context) error {
		if _, err := testStore.CreateMembership(ctx, db.NewMembership{ID: memberA, UserID: userA, Role: "agent", Status: "active"}); err != nil {
			return err
		}
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: memberB, UserID: userB, Role: "agent", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		steps := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled,version) VALUES($1,$2,'meta_cloud',$3,'connected',true,'1')`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'WhatsApp')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, memberA}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, memberB}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,version) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,$8,'202611',1,4)`, []any{conversation, tenant, inbox, sector, contact, identity, memberA, "V-" + conversation.String()[:8]}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{tenant, uuid.New(), waba, uuid.NewString(), connector, inbox}},
			{`INSERT INTO wa_templates(tenant_id,waba_id,external_id,name,language,category,state,components,sync_source,synced_at) VALUES($1,$2,'tpl-api','tpl','pt_BR','UTILITY','APPROVED','[]','driver',now())`, []any{tenant, waba}},
			{`INSERT INTO contact_consents(tenant_id,id,contact_id,inbox_id,purpose,source,evidence,granted_at) VALUES($1,$2,$3,$4,'atendimento','teste','aceite',now())`, []any{tenant, uuid.New(), contact, inbox}},
		}
		for _, step := range steps {
			if _, err := tx.Exec(ctx, step.q, step.a...); err != nil {
				return fmt.Errorf("%s: %w", step.q, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secret, _, err := IssueScoped(ctx, testStore, tenant, []string{"templates.send", "conversations.read"}, 40)
	if err != nil {
		t.Fatal(err)
	}
	api := Mount(testStore, attendanceService.New(testStore, stubQueuer{}))
	version := readConversationVersion(t, api, conversation, secret)
	if version < 2 {
		t.Fatalf("versão lida %d", version)
	}
	body := []byte(fmt.Sprintf(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"estavel","expected_version":%d}`, version))
	first := postTemplate(t, api, conversation, secret, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("envio %d %s", first.Code, first.Body.String())
	}
	var sent templateResult
	if err := json.Unmarshal(first.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	stale := postTemplate(t, api, conversation, secret, []byte(fmt.Sprintf(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"atrasada","expected_version":%d}`, version-1)))
	if stale.Code != http.StatusConflict {
		t.Fatalf("versão antiga %d %s", stale.Code, stale.Body.String())
	}
	reread := readConversationVersion(t, api, conversation, secret)
	second := postTemplate(t, api, conversation, secret, []byte(fmt.Sprintf(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"releitura","expected_version":%d}`, reread)))
	if second.Code != http.StatusCreated {
		t.Fatalf("releitura %d %s", second.Code, second.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 2 {
		t.Fatal("releitura não usou a versão lida")
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=$3, version=version+1 WHERE tenant_id=$1 AND id=$2`, tenant, conversation, memberB)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	moved := postTemplate(t, api, conversation, secret, body)
	if moved.Code != http.StatusOK {
		t.Fatalf("replay após transferência %d %s", moved.Code, moved.Body.String())
	}
	var replay templateResult
	if err := json.Unmarshal(moved.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.AttemptID != sent.AttemptID || replay.MessageID != sent.MessageID || replay.JobID != sent.JobID {
		t.Fatalf("replay %+v primeiro %+v", replay, sent)
	}
	if err := testStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=NULL, controller='menu', controller_epoch=controller_epoch+1 WHERE tenant_id=$1 AND id=$2`, tenant, conversation)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unassigned := postTemplate(t, api, conversation, secret, body)
	if unassigned.Code != http.StatusOK {
		t.Fatalf("replay sem atendente %d %s", unassigned.Code, unassigned.Body.String())
	}
	other := postTemplate(t, api, conversation, secret, []byte(`{"template_id":"outro","language":"pt_BR","purpose":"atendimento","client_request_id":"estavel","expected_version":`+fmt.Sprint(reread)+`}`))
	if other.Code != http.StatusConflict {
		t.Fatalf("payload diferente %d %s", other.Code, other.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 2 {
		t.Fatal("replay ou conflito criou tentativa")
	}
}

func readConversationVersion(t *testing.T, api http.Handler, conversation uuid.UUID, secret string) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/"+conversation.String(), nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("leitura %d %s", rec.Code, rec.Body.String())
	}
	var view conversationView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view.Version
}

type countingQueuer struct{ n int64 }

func (q *countingQueuer) EnqueueOutbound(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (int64, error) {
	q.n++
	return q.n, nil
}

type templateWorld struct {
	tenant       uuid.UUID
	user         uuid.UUID
	member       uuid.UUID
	inbox        uuid.UUID
	conversation uuid.UUID
}

func seedTemplateWorld(t *testing.T) templateWorld {
	t.Helper()
	ctx := context.Background()
	w := templateWorld{tenant: uuid.New(), user: uuid.New(), member: uuid.New(), inbox: uuid.New(), conversation: uuid.New()}
	connector, sector, contact, identity := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	waba, app := uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: w.tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		_, err := testStore.CreateUser(ctx, db.NewUser{ID: w.user, Email: w.user.String() + "@example.com", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, w.tenant, func(ctx context.Context) error {
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: w.member, UserID: w.user, Role: "admin", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		steps := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled,version) VALUES($1,$2,'meta_cloud',$3,'connected',true,'1')`, []any{connector, w.tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'WhatsApp')`, []any{w.inbox, w.tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, []any{w.tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{w.tenant, sector, w.inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{w.tenant, sector, w.member}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, []any{contact, w.tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, w.tenant, contact, connector, identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,version) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,$8,'202611',1,1)`, []any{w.conversation, w.tenant, w.inbox, sector, contact, identity, w.member, "K-" + w.conversation.String()[:8]}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{w.tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{w.tenant, uuid.New(), waba, uuid.NewString(), connector, w.inbox}},
			{`INSERT INTO wa_templates(tenant_id,waba_id,external_id,name,language,category,state,components,sync_source,synced_at) VALUES($1,$2,'tpl-api','tpl','pt_BR','UTILITY','APPROVED','[]','driver',now())`, []any{w.tenant, waba}},
			{`INSERT INTO contact_consents(tenant_id,id,contact_id,inbox_id,purpose,source,evidence,granted_at) VALUES($1,$2,$3,$4,'atendimento','teste','aceite',now())`, []any{w.tenant, uuid.New(), contact, w.inbox}},
		}
		for _, step := range steps {
			if _, err := tx.Exec(ctx, step.q, step.a...); err != nil {
				return fmt.Errorf("%s: %w", step.q, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPublicTemplateIsolatesTokenAndPanelKeys(t *testing.T) {
	w := seedTemplateWorld(t)
	ctx := context.Background()
	secretA, tokenA, err := IssueScoped(ctx, testStore, w.tenant, []string{"templates.send"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	secretB, _, err := IssueScoped(ctx, testStore, w.tenant, []string{"templates.send"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	q := &countingQueuer{}
	api := Mount(testStore, attendanceService.New(testStore, q))
	body := []byte(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"shared","expected_version":1}`)
	first := postTemplate(t, api, w.conversation, secretA, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("token A %d %s", first.Code, first.Body.String())
	}
	var sentA templateResult
	if err := json.Unmarshal(first.Body.Bytes(), &sentA); err != nil {
		t.Fatal(err)
	}
	second := postTemplate(t, api, w.conversation, secretB, body)
	if second.Code != http.StatusCreated {
		t.Fatalf("token B %d %s", second.Code, second.Body.String())
	}
	var sentB templateResult
	if err := json.Unmarshal(second.Body.Bytes(), &sentB); err != nil {
		t.Fatal(err)
	}
	if sentA.MessageID == sentB.MessageID || sentA.AttemptID == sentB.AttemptID || sentA.Replayed || sentB.Replayed {
		t.Fatalf("tokens compartilharam o envio %+v %+v", sentA, sentB)
	}
	panel, err := attendanceService.New(testStore, q).SendTemplate(ctx, attendanceService.Principal{
		UserID: w.user, MembershipID: w.member, TenantID: w.tenant, Role: "admin",
	}, attendanceService.TemplateSendInput{
		InboxID: w.inbox, ConversationID: w.conversation, ExpectedVersion: 1,
		ClientRequestID: "shared", TemplateID: "tpl-api", Language: "pt_BR", Purpose: "atendimento", At: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if panel.Replayed || panel.MessageID == sentA.MessageID || panel.MessageID == sentB.MessageID {
		t.Fatalf("painel reaproveitou a API %+v", panel)
	}
	replay := postTemplate(t, api, w.conversation, secretA, body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay do token %d %s", replay.Code, replay.Body.String())
	}
	var again templateResult
	if err := json.Unmarshal(replay.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.MessageID != sentA.MessageID || again.AttemptID != sentA.AttemptID || again.JobID != sentA.JobID {
		t.Fatalf("replay %+v original %+v", again, sentA)
	}
	longID := strings.Repeat("k", 200)
	longBody := []byte(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"` + longID + `","expected_version":1}`)
	longSend := postTemplate(t, api, w.conversation, secretA, longBody)
	if longSend.Code != http.StatusCreated {
		t.Fatalf("chave longa %d %s", longSend.Code, longSend.Body.String())
	}
	if len(attendanceRequestID(tokenA, longID)) > 200 {
		t.Fatal("chave interna passou de 200")
	}
	if countAttempts(t, w.tenant, w.conversation) != 4 || q.n != 4 {
		t.Fatalf("tentativas %d jobs %d", countAttempts(t, w.tenant, w.conversation), q.n)
	}
}

func TestPublicTemplateRecoversWithoutResending(t *testing.T) {
	w := seedTemplateWorld(t)
	ctx := context.Background()
	secret, tokenID, err := IssueScoped(ctx, testStore, w.tenant, []string{"templates.send"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	const external = "gap-1"
	sum := templatePayloadHash(w.conversation, "tpl-api", "pt_BR", "atendimento")
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO api_template_requests (tenant_id, token_id, client_request_id, conversation_id, payload_hash, membership_id)
			VALUES ($1,$2,$3,$4,$5,$6)`, w.tenant, tokenID, external, w.conversation, sum, w.member)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	q := &countingQueuer{}
	direct := attendanceService.New(testStore, bindingQueuer{inner: q})
	sent, err := direct.SendTemplate(ctx, attendanceService.Principal{
		UserID: w.user, MembershipID: w.member, TenantID: w.tenant, Role: "admin",
	}, attendanceService.TemplateSendInput{
		InboxID: w.inbox, ConversationID: w.conversation, ExpectedVersion: 1,
		ClientRequestID: attendanceRequestID(tokenID, external), TemplateID: "tpl-api", Language: "pt_BR", Purpose: "atendimento", At: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Replayed || sent.JobID != 1 || q.n != 1 {
		t.Fatalf("envio direto %+v jobs %d", sent, q.n)
	}
	if err := testStore.WithTenantTx(ctx, w.tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE memberships SET status='disabled' WHERE tenant_id=$1 AND id=$2`, w.tenant, w.member); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=NULL, controller='menu' WHERE tenant_id=$1 AND id=$2`, w.tenant, w.conversation)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	api := Mount(testStore, attendanceService.New(testStore, q))
	body := []byte(`{"template_id":"tpl-api","language":"pt_BR","purpose":"atendimento","client_request_id":"gap-1","expected_version":1}`)
	again := postTemplate(t, api, w.conversation, secret, body)
	if again.Code != http.StatusOK {
		t.Fatalf("recuperação %d %s", again.Code, again.Body.String())
	}
	var recovered templateResult
	if err := json.Unmarshal(again.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if !recovered.Replayed || recovered.MessageID != sent.MessageID || recovered.AttemptID != sent.AttemptID || recovered.JobID != sent.JobID {
		t.Fatalf("recuperado %+v original %+v", recovered, sent)
	}
	if q.n != 1 || countAttempts(t, w.tenant, w.conversation) != 1 {
		t.Fatalf("reenvio jobs %d tentativas %d", q.n, countAttempts(t, w.tenant, w.conversation))
	}
}

func countAttempts(t *testing.T, tenant, conversation uuid.UUID) int {
	t.Helper()
	var n int
	err := testStore.WithTenantTx(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2`, tenant, conversation).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
