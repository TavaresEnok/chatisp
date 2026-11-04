package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	attendanceService "chatisp/internal/attendance/service"
	"chatisp/internal/platform/db"
)

func TestPublicConversationTextAssignAndState(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	adminUser, agentUser, otherUser := uuid.New(), uuid.New(), uuid.New()
	admin, agent, other := uuid.New(), uuid.New(), uuid.New()
	inbox, connector, sector := uuid.New(), uuid.New(), uuid.New()
	waba, app := uuid.New(), uuid.New()
	conversation, contact, identity := uuid.New(), uuid.New(), uuid.New()
	quiet, quietContact, quietIdentity := uuid.New(), uuid.New(), uuid.New()
	if err := testStore.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := testStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: json.RawMessage(`{}`)}); err != nil {
			return err
		}
		for _, user := range []uuid.UUID{adminUser, agentUser, otherUser} {
			if _, err := testStore.CreateUser(ctx, db.NewUser{ID: user, Email: user.String() + "@example.com", Status: "active"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.WithTenant(ctx, tenant, func(ctx context.Context) error {
		if _, err := testStore.CreateMembership(ctx, db.NewMembership{ID: admin, UserID: adminUser, Role: "admin", Status: "active"}); err != nil {
			return err
		}
		if _, err := testStore.CreateMembership(ctx, db.NewMembership{ID: agent, UserID: agentUser, Role: "agent", Status: "active"}); err != nil {
			return err
		}
		_, err := testStore.CreateMembership(ctx, db.NewMembership{ID: other, UserID: otherUser, Role: "agent", Status: "active"})
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
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'Caixa local')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Suporte')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes VALUES($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, agent}},
			{`INSERT INTO sector_memberships VALUES($1,$2,$3)`, []any{tenant, sector, other}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Cliente')`, []any{contact, tenant}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Outro')`, []any{quietContact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, identity.String()}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{quietIdentity, tenant, quietContact, connector, quietIdentity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,version) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,$8,'202611',1,1)`, []any{conversation, tenant, inbox, sector, contact, identity, agent, "T-" + conversation.String()[:8]}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,assignee_id,protocol,protocol_period,cycle_number,version) VALUES($1,$2,$3,$4,$5,$6,'open','human',$7,$8,'202611',1,1)`, []any{quiet, tenant, inbox, sector, quietContact, quietIdentity, agent, "Q-" + quiet.String()[:8]}},
			{`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,now())`, []any{tenant, uuid.New(), conversation}},
			{`INSERT INTO conversation_cycles(tenant_id,id,conversation_id,cycle_number,opened_at) VALUES($1,$2,$3,1,now())`, []any{tenant, uuid.New(), quiet}},
			{`INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at,historical) VALUES($1,$2,$3,$4,'inbound','customer','public','oi','received',clock_timestamp(),false)`, []any{tenant, uuid.New(), conversation, connector}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{tenant, uuid.New(), waba, uuid.NewString(), connector, inbox}},
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
	secret, _, err := IssueScoped(ctx, testStore, tenant, []string{"conversations.write", "conversations.read"}, 40)
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := IssueScoped(ctx, testStore, tenant, []string{"conversations.read"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	api := Mount(testStore, attendanceService.New(testStore, stubQueuer{}))

	denied := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/messages", reader, []byte(`{"body":"ola","client_request_id":"txt-1","expected_version":1}`))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("leitura escreveu: %d %s", denied.Code, denied.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 0 {
		t.Fatal("escopo de leitura gravou tentativa")
	}

	closedWindow := postPublic(t, api, "/v1/conversations/"+quiet.String()+"/messages", secret, []byte(`{"body":"ola","client_request_id":"sem-janela","expected_version":1}`))
	if closedWindow.Code != http.StatusUnprocessableEntity || !bytes.Contains(closedWindow.Body.Bytes(), []byte("window_closed")) {
		t.Fatalf("janela: %d %s", closedWindow.Code, closedWindow.Body.String())
	}
	if countAttempts(t, tenant, quiet) != 0 {
		t.Fatal("janela fechada gravou tentativa")
	}

	body := []byte(`{"body":"resposta local","client_request_id":"txt-1","expected_version":1}`)
	first := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/messages", secret, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("envio %d %s", first.Code, first.Body.String())
	}
	var sent templateResult
	if err := json.Unmarshal(first.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Replayed || sent.MessageID == uuid.Nil || sent.AttemptID == uuid.Nil || sent.JobID != 1 {
		t.Fatalf("resultado %+v", sent)
	}
	if countAttempts(t, tenant, conversation) != 1 {
		t.Fatal("tentativa não ficou única")
	}
	again := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/messages", secret, body)
	if again.Code != http.StatusOK {
		t.Fatalf("replay %d %s", again.Code, again.Body.String())
	}
	var replay templateResult
	if err := json.Unmarshal(again.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.MessageID != sent.MessageID || replay.AttemptID != sent.AttemptID || replay.JobID != 1 {
		t.Fatalf("replay %+v", replay)
	}
	changed := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/messages", secret, []byte(`{"body":"outro texto","client_request_id":"txt-1","expected_version":1}`))
	if changed.Code != http.StatusConflict {
		t.Fatalf("conflito %d %s", changed.Code, changed.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 1 {
		t.Fatal("conflito criou outra tentativa")
	}

	version := publicVersion(t, api, conversation, secret)
	assigned := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/assignment", secret, []byte(fmt.Sprintf(`{"assignee_id":"%s","client_request_id":"atr-1","expected_version":%d}`, other, version)))
	if assigned.Code != http.StatusOK {
		t.Fatalf("atribuição %d %s", assigned.Code, assigned.Body.String())
	}
	var after conversationView
	if err := json.Unmarshal(assigned.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.AssigneeID == nil || *after.AssigneeID != other || after.Version <= version {
		t.Fatalf("conversa atribuída %+v", after)
	}
	same := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/assignment", secret, []byte(fmt.Sprintf(`{"assignee_id":"%s","client_request_id":"atr-1","expected_version":%d}`, other, version)))
	if same.Code != http.StatusOK {
		t.Fatalf("replay da atribuição %d %s", same.Code, same.Body.String())
	}
	var replayedAssign conversationView
	if err := json.Unmarshal(same.Body.Bytes(), &replayedAssign); err != nil {
		t.Fatal(err)
	}
	if replayedAssign.Version != after.Version || replayedAssign.AssigneeID == nil || *replayedAssign.AssigneeID != other {
		t.Fatalf("atribuição repetida %+v", replayedAssign)
	}
	kept := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/messages", secret, body)
	if kept.Code != http.StatusOK {
		t.Fatalf("replay depois da transferência %d %s", kept.Code, kept.Body.String())
	}
	if countAttempts(t, tenant, conversation) != 1 {
		t.Fatal("transferência reenviou o texto")
	}

	closed := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/state", secret, []byte(fmt.Sprintf(`{"status":"closed","reason":"fim","result":"resolved","summary":"encerrado no teste","client_request_id":"est-1","expected_version":%d}`, after.Version)))
	if closed.Code != http.StatusOK {
		t.Fatalf("encerramento %d %s", closed.Code, closed.Body.String())
	}
	var ended conversationView
	if err := json.Unmarshal(closed.Body.Bytes(), &ended); err != nil {
		t.Fatal(err)
	}
	if ended.Status != "closed" || ended.Version <= after.Version {
		t.Fatalf("encerrada %+v", ended)
	}
	reclosed := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/state", secret, []byte(fmt.Sprintf(`{"status":"closed","reason":"fim","result":"resolved","summary":"encerrado no teste","client_request_id":"est-1","expected_version":%d}`, after.Version)))
	if reclosed.Code != http.StatusOK {
		t.Fatalf("replay do encerramento %d %s", reclosed.Code, reclosed.Body.String())
	}
	opened := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/state", secret, []byte(fmt.Sprintf(`{"status":"open","client_request_id":"est-2","expected_version":%d}`, ended.Version)))
	if opened.Code != http.StatusOK {
		t.Fatalf("reabertura %d %s", opened.Code, opened.Body.String())
	}
	var live conversationView
	if err := json.Unmarshal(opened.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if live.Status == "closed" || live.Version <= ended.Version {
		t.Fatalf("reaberta %+v", live)
	}
	stale := postPublic(t, api, "/v1/conversations/"+conversation.String()+"/state", secret, []byte(`{"status":"closed","reason":"fim","result":"resolved","summary":"encerrado no teste","client_request_id":"est-3","expected_version":1}`))
	if stale.Code != http.StatusConflict {
		t.Fatalf("versão velha %d %s", stale.Code, stale.Body.String())
	}
}

func publicVersion(t *testing.T, api http.Handler, conversation uuid.UUID, secret string) int64 {
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

func postPublic(t *testing.T, api http.Handler, path, secret string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	return rec
}
