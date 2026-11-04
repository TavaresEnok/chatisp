package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/attendance/pipeline"
	"chatisp/internal/attendance/service"
	"chatisp/internal/contactdocs"
	"chatisp/internal/distribution"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/logging"
	"chatisp/internal/platform/mail"
	"chatisp/internal/presence"
)

const composedCPF = "52998224725"

func TestM3CM5BRoutesRegistered(t *testing.T) {
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, fragment := range []string{
		"/api/panel/contacts/{contact_id}/document",
		"/api/panel/presence/heartbeat",
		"/api/panel/sectors/{sector_id}/distribution",
		"/api/panel/sectors/{sector_id}/presence",
	} {
		if !strings.Contains(document, fragment) {
			t.Fatalf("contrato ausente: %s", fragment)
		}
	}
	const origin = "https://painel.chatisp.test"
	api := New(Dependencies{TrustedOrigins: []string{origin}})
	for _, path := range []string{
		"/api/panel/contacts/" + uuid.NewString() + "/document",
		"/api/panel/presence",
		"/api/panel/sectors/" + uuid.NewString() + "/distribution?inbox_id=" + uuid.NewString(),
	} {
		response := httptestGet(api, path)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func httptestGet(api *API, path string) *httpResponse {
	rec := panelCall(api.Handler, http.MethodGet, path, "198.51.100.40", nil, "", "", "https://painel.chatisp.test", "")
	return &httpResponse{Code: rec.Code, Body: rec.Body}
}

type httpResponse struct {
	Code int
	Body *bytes.Buffer
}

func TestM3CM5BComposition(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := seedMembership(t, tenant, "admin", "senha-correta-10")
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{
		Auth:           panelService(t, &mail.Fake{}),
		Logger:         logging.New("info", &logs),
		TrustedOrigins: []string{origin},
		Documents:      contactdocs.New(panelStore, ring),
		Presence:       presence.New(panelStore),
		Store:          panelStore,
	})
	adminCookie, adminCSRF, _ := panelLogin(t, nil, admin.email, "senha-correta-10")
	agentCookie, agentCSRF, _ := panelLogin(t, nil, agent.email, "senha-correta-10")

	contact := uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,'Documento')`, tenant, contact)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	masked := panelCall(api.Handler, http.MethodGet, "/api/panel/contacts/"+contact.String()+"/document", "198.51.100.41", nil, adminCookie, "", origin, "")
	if masked.Code != http.StatusOK || strings.Contains(masked.Body.String(), composedCPF) || !strings.Contains(masked.Body.String(), `"present":false`) {
		t.Fatalf("leitura inicial: %d %s", masked.Code, masked.Body.String())
	}
	stored := panelCall(api.Handler, http.MethodPut, "/api/panel/contacts/"+contact.String()+"/document", "198.51.100.41", map[string]any{
		"request_id": uuid.New(), "expected_version": 0, "document": composedCPF,
	}, adminCookie, adminCSRF, origin, "")
	if stored.Code != http.StatusOK || strings.Contains(stored.Body.String(), composedCPF) || !strings.Contains(stored.Body.String(), `"mask":"***.***.***-**"`) {
		t.Fatalf("gravação mascarada: %d %s", stored.Code, stored.Body.String())
	}
	again := panelCall(api.Handler, http.MethodGet, "/api/panel/contacts/"+contact.String()+"/document", "198.51.100.41", nil, adminCookie, "", origin, "")
	if again.Code != http.StatusOK || strings.Contains(again.Body.String(), composedCPF) || !strings.Contains(again.Body.String(), `"present":true`) {
		t.Fatalf("releitura: %d %s", again.Code, again.Body.String())
	}
	if strings.Contains(logs.String(), composedCPF) || strings.Contains(logs.String(), "dHJvc") {
		t.Fatalf("log expôs documento ou chave: %s", logs.String())
	}

	session := panelCall(api.Handler, http.MethodGet, "/api/panel/session", "198.51.100.42", nil, adminCookie, "", origin, "")
	if session.Code != http.StatusOK {
		t.Fatalf("sessão: %d %s", session.Code, session.Body.String())
	}
	adminPresence := panelCall(api.Handler, http.MethodGet, "/api/panel/presence", "198.51.100.42", nil, adminCookie, "", origin, "")
	if adminPresence.Code != http.StatusOK || !strings.Contains(adminPresence.Body.String(), `"connected":true`) {
		t.Fatalf("presença da sessão: %d %s", adminPresence.Code, adminPresence.Body.String())
	}
	agentBeat := panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.43", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if agentBeat.Code != http.StatusOK || !strings.Contains(agentBeat.Body.String(), `"state":"available"`) {
		t.Fatalf("heartbeat: %d %s", agentBeat.Code, agentBeat.Body.String())
	}

	sector, inbox, connector := uuid.New(), uuid.New(), uuid.New()
	convOff, convLater := uuid.New(), uuid.New()
	contactA, contactB := uuid.New(), uuid.New()
	identityA, identityB := uuid.New(), uuid.New()
	err = panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3)`, []any{tenant, connector, connector.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,'Caixa')`, []any{tenant, inbox, connector}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'Fila')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes (tenant_id,sector_id,inbox_id) VALUES ($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships (tenant_id,sector_id,membership_id) VALUES ($1,$2,$3)`, []any{tenant, sector, agent.membership}},
			{`INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,'A'),($1,$3,'B')`, []any{tenant, contactA, contactB}},
			{`INSERT INTO contact_channel_identities (tenant_id,id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,'a'),($1,$5,$6,$4,'b')`, []any{tenant, identityA, contactA, connector, identityB, contactB}},
		}
		for _, item := range statements {
			if _, err := tx.Exec(ctx, item.q, item.args...); err != nil {
				return err
			}
		}
		return insertConversation(ctx, tx, tenant, convOff, inbox, sector, contactA, identityA, "P-off")
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := panelCall(api.Handler, http.MethodGet, "/api/panel/sectors/"+sector.String()+"/distribution?inbox_id="+inbox.String(), "198.51.100.44", nil, adminCookie, "", origin, "")
	if queue.Code != http.StatusOK || !strings.Contains(queue.Body.String(), `"enabled":false`) || !strings.Contains(queue.Body.String(), convOff.String()) {
		t.Fatalf("fila desligada: %d %s", queue.Code, queue.Body.String())
	}
	now := time.Now().UTC()
	off, err := AssignArrived(ctx, panelStore, tenant, sector, inbox, now)
	if err != nil || off.Reason != distribution.ReasonAutoDisabled {
		t.Fatalf("flag ausente: %+v %v", off, err)
	}
	if assignee(t, ctx, tenant, convOff) != uuid.Nil {
		t.Fatal("flag ausente atribuiu conversa")
	}
	enabled := panelCall(api.Handler, http.MethodPut, "/api/panel/sectors/"+sector.String()+"/distribution", "198.51.100.44", map[string]any{
		"expected_version": 0, "enabled": true,
	}, adminCookie, adminCSRF, origin, "")
	if enabled.Code != http.StatusOK || !strings.Contains(enabled.Body.String(), `"enabled":true`) || !strings.Contains(enabled.Body.String(), `"version":1`) {
		t.Fatalf("ligar flag: %d %s", enabled.Code, enabled.Body.String())
	}
	on, err := AssignArrived(ctx, panelStore, tenant, sector, inbox, time.Now().UTC())
	if err != nil || on.Status != distribution.StatusAssigned || on.ConversationID != convOff || on.MembershipID != agent.membership {
		t.Fatalf("flag ligada: %+v %v", on, err)
	}
	if assignee(t, ctx, tenant, convOff) != agent.membership {
		t.Fatal("conversa não ficou com o atendente")
	}

	for _, tc := range []struct {
		cookie, path string
		want         int
		match        string
	}{
		{agentCookie, "/api/panel/presence/occupancy", 200, `"occupied":1`},
		{adminCookie, "/api/panel/presence/occupancy?sector_id=" + sector.String(), 200, agent.membership.String()},
		{agentCookie, "/api/panel/presence/occupancy?sector_id=" + sector.String(), 403, ""},
		{"", "/api/panel/presence/occupancy", 401, ""},
	} {
		r := panelCall(api.Handler, http.MethodGet, tc.path, "198.51.100.45", nil, tc.cookie, "", origin, "")
		if r.Code != tc.want || (tc.match != "" && !strings.Contains(r.Body.String(), tc.match)) {
			t.Fatalf("ocupação %s: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	reopenPath := "/api/panel/inboxes/" + inbox.String() + "/reopen-window"
	for _, tc := range []struct {
		method, cookie, csrf string
		body                 any
		want                 int
		match                string
	}{
		{http.MethodGet, adminCookie, "", nil, 200, `"hours":0`},
		{http.MethodGet, agentCookie, "", nil, 403, ""},
		{http.MethodPut, agentCookie, "", map[string]any{"hours": 24}, 403, ""},
		{http.MethodPut, adminCookie, adminCSRF, map[string]any{"hours": 721}, 422, ""},
		{http.MethodPut, adminCookie, adminCSRF, map[string]any{"hours": 24}, 200, `"hours":24`},
		{http.MethodGet, adminCookie, "", nil, 200, `"hours":24`},
		{http.MethodGet, "", "", nil, 401, ""},
	} {
		r := panelCall(api.Handler, tc.method, reopenPath, "198.51.100.46", tc.body, tc.cookie, tc.csrf, origin, "")
		if r.Code != tc.want || (tc.match != "" && !strings.Contains(r.Body.String(), tc.match)) {
			t.Fatalf("janela de reabertura %s: %d %s", tc.method, r.Code, r.Body.String())
		}
	}
	missing := panelCall(api.Handler, http.MethodGet, "/api/panel/inboxes/"+uuid.NewString()+"/reopen-window", "198.51.100.46", nil, adminCookie, "", origin, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("caixa inexistente: %d %s", missing.Code, missing.Body.String())
	}
	disabled := panelCall(api.Handler, http.MethodPut, "/api/panel/sectors/"+sector.String()+"/distribution", "198.51.100.44", map[string]any{
		"expected_version": 1, "enabled": false,
	}, adminCookie, adminCSRF, origin, "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"enabled":false`) {
		t.Fatalf("desligar flag: %d %s", disabled.Code, disabled.Body.String())
	}
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return insertConversation(ctx, tx, tenant, convLater, inbox, sector, contactB, identityB, "P-later")
	}); err != nil {
		t.Fatal(err)
	}
	later, err := AssignArrived(ctx, panelStore, tenant, sector, inbox, time.Now().UTC())
	if err != nil || later.Reason != distribution.ReasonAutoDisabled {
		t.Fatalf("flag desligada: %+v %v", later, err)
	}
	if assignee(t, ctx, tenant, convLater) != uuid.Nil || assignee(t, ctx, tenant, convOff) != agent.membership {
		t.Fatal("desligar a flag alterou a fila ou o responsável anterior")
	}
	if err := PickInbound(ctx, panelStore, tenant, uuid.New(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(again.Body.Bytes(), &decoded); err != nil || decoded["document"] != nil {
		t.Fatalf("corpo mascarado: %s", again.Body.String())
	}
}

func insertConversation(ctx context.Context, tx pgx.Tx, tenant, id, inbox, sector, contact, identity uuid.UUID, protocol string) error {
	_, err := tx.Exec(ctx, `INSERT INTO conversations (tenant_id,id,inbox_id,sector_id,contact_id,contact_identity_id,status,protocol,protocol_period,cycle_number) VALUES ($1,$2,$3,$4,$5,$6,'open',$7,'202611',1)`, tenant, id, inbox, sector, contact, identity, protocol)
	return err
}

type scriptedInbound struct {
	calls int
	fail  int
}

func (s *scriptedInbound) ApplyInbound(context.Context, uuid.UUID, uuid.UUID) error {
	s.calls++
	if s.calls <= s.fail {
		return errors.New("inbound ainda não concluiu")
	}
	return nil
}

func TestArrivalOnceThroughWrapper(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := seedMembership(t, tenant, "admin", "senha-correta-10")
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{
		Auth:           panelService(t, &mail.Fake{}),
		TrustedOrigins: []string{origin},
		Presence:       presence.New(panelStore),
		Store:          panelStore,
	})
	adminCookie, adminCSRF, _ := panelLogin(t, nil, admin.email, "senha-correta-10")
	agentCookie, agentCSRF, _ := panelLogin(t, nil, agent.email, "senha-correta-10")
	sector, inbox, connector := uuid.New(), uuid.New(), uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3)`, []any{tenant, connector, connector.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,'Caixa chegada')`, []any{tenant, inbox, connector}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,'Chegada')`, []any{tenant, sector}},
			{`INSERT INTO sector_inboxes (tenant_id,sector_id,inbox_id) VALUES ($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO sector_memberships (tenant_id,sector_id,membership_id) VALUES ($1,$2,$3)`, []any{tenant, sector, agent.membership}},
		}
		for _, item := range statements {
			if _, err := tx.Exec(ctx, item.q, item.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	beat := panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.50", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", beat.Code, beat.Body.String())
	}

	convA := seedQueuedConversation(t, ctx, tenant, inbox, sector, connector, "A-off")
	itemA := seedProcessedItem(t, ctx, tenant, inbox, connector, convA, "message", "processed", "ext-a")
	inbound := &scriptedInbound{}
	now := time.Now().UTC()
	if err := Arrive(ctx, inbound, panelStore, tenant, itemA, now); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convA) != uuid.Nil || arrivalClaims(t, ctx, tenant, itemA) != 1 || inbound.calls != 1 {
		t.Fatal("flag desligada atribuiu ou não registrou a tentativa")
	}
	enabled := panelCall(api.Handler, http.MethodPut, "/api/panel/sectors/"+sector.String()+"/distribution", "198.51.100.50", map[string]any{
		"expected_version": 0, "enabled": true,
	}, adminCookie, adminCSRF, origin, "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("ligar flag: %d %s", enabled.Code, enabled.Body.String())
	}
	if err := Arrive(ctx, inbound, panelStore, tenant, itemA, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convA) != uuid.Nil || arrivalClaims(t, ctx, tenant, itemA) != 1 || inbound.calls != 2 {
		t.Fatal("reprise com a flag ligada redistribuiu o item já tentado")
	}

	convHead := seedQueuedConversation(t, ctx, tenant, inbox, sector, connector, "A-head")
	shiftQueue(t, ctx, tenant, convHead, -time.Minute)
	if err := Arrive(ctx, inbound, panelStore, tenant, itemA, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convHead) != uuid.Nil || assignee(t, ctx, tenant, convA) != uuid.Nil {
		t.Fatal("reprise tomou a cabeça da fila")
	}

	convP := seedQueuedConversation(t, ctx, tenant, inbox, sector, connector, "A-pending")
	itemP := seedProcessedItem(t, ctx, tenant, inbox, connector, convP, "message", "pending", "ext-p")
	if err := Arrive(ctx, inbound, panelStore, tenant, itemP, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if arrivalClaims(t, ctx, tenant, itemP) != 0 || assignee(t, ctx, tenant, convP) != uuid.Nil || assignee(t, ctx, tenant, convHead) != uuid.Nil {
		t.Fatal("item pendente gravou tentativa ou atribuiu")
	}
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE webhook_items SET state='processed' WHERE tenant_id=$1 AND id=$2`, tenant, itemP)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	beat = panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.50", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", beat.Code, beat.Body.String())
	}
	if err := Arrive(ctx, inbound, panelStore, tenant, itemP, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convHead) != agent.membership || assignee(t, ctx, tenant, convP) != uuid.Nil || arrivalClaims(t, ctx, tenant, itemP) != 1 {
		t.Fatal("recuperação após o inbound não atribuiu a cabeça uma única vez")
	}
	if err := Arrive(ctx, inbound, panelStore, tenant, itemP, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convP) != uuid.Nil || assignee(t, ctx, tenant, convA) != uuid.Nil {
		t.Fatal("reprise depois da atribuição tomou outra conversa")
	}

	returnConversation(t, ctx, tenant, convHead)
	if err := Arrive(ctx, inbound, panelStore, tenant, itemP, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convHead) != uuid.Nil || assignee(t, ctx, tenant, convP) != uuid.Nil {
		t.Fatal("reprise após devolução distribuiu de novo")
	}

	itemStatus := seedProcessedItem(t, ctx, tenant, inbox, connector, convP, "status", "processed", "ext-status")
	if err := Arrive(ctx, inbound, panelStore, tenant, itemStatus, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if arrivalClaims(t, ctx, tenant, itemStatus) != 0 || assignee(t, ctx, tenant, convHead) != uuid.Nil {
		t.Fatal("item de status atribuiu")
	}

	failed := &scriptedInbound{fail: 1}
	convFail := seedQueuedConversation(t, ctx, tenant, inbox, sector, connector, "A-fail")
	itemFail := seedProcessedItem(t, ctx, tenant, inbox, connector, convFail, "message", "processed", "ext-fail")
	if err := Arrive(ctx, failed, panelStore, tenant, itemFail, time.Now().UTC()); err == nil || arrivalClaims(t, ctx, tenant, itemFail) != 0 {
		t.Fatalf("falha de inbound chegou a distribuir: %v", err)
	}
	beat = panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.51", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", beat.Code, beat.Body.String())
	}
	if err := Arrive(ctx, failed, panelStore, tenant, itemFail, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if arrivalClaims(t, ctx, tenant, itemFail) != 1 || assignee(t, ctx, tenant, convA) != agent.membership {
		t.Fatal("retomada depois da falha de inbound não distribuiu a cabeça")
	}
	if err := Arrive(ctx, failed, panelStore, tenant, itemFail, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if assignee(t, ctx, tenant, convFail) != uuid.Nil || assignee(t, ctx, tenant, convHead) != uuid.Nil || assignee(t, ctx, tenant, convA) != agent.membership {
		t.Fatal("segunda reprise distribuiu conversa que continuava sem responsável")
	}
	if err := PickInbound(ctx, panelStore, tenant, uuid.New(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// TestRealInboundArrivalThroughComposition runs the existing inbound handler
// through Arrive. The item starts pending, with no message and no conversation.
// PickInbound then sees the rows that handler wrote. The flag off leaves the
// conversation unassigned. The flag on assigns it.
func TestRealInboundArrivalThroughComposition(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	if err := panelStore.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := panelStore.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "UTC", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := seedMembership(t, tenant, "admin", "senha-correta-10")
	agent := seedMembership(t, tenant, "agent", "senha-correta-10")
	origin := "https://painel.chatisp.test"
	api := New(Dependencies{
		Auth:           panelService(t, &mail.Fake{}),
		TrustedOrigins: []string{origin},
		Presence:       presence.New(panelStore),
		Store:          panelStore,
	})
	adminCookie, adminCSRF, _ := panelLogin(t, nil, admin.email, "senha-correta-10")
	agentCookie, agentCSRF, _ := panelLogin(t, nil, agent.email, "senha-correta-10")
	beat := panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.60", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", beat.Code, beat.Body.String())
	}
	handler := pipeline.NewInboundHandler(panelStore, service.New(panelStore, nil))

	sectorOff, inboxOff, connectorOff := seedArrivalBox(t, ctx, tenant, agent.membership, "Caixa desligada")
	itemOff := seedPendingArrival(t, ctx, tenant, inboxOff, connectorOff, "551191110001", "wamid.real.off", "oi desligado")
	if messagesForExternal(t, ctx, tenant, connectorOff, "wamid.real.off") != 0 || itemState(t, ctx, tenant, itemOff) != "pending" {
		t.Fatal("a chegada real já tinha mensagem ou não estava pendente")
	}
	if err := Arrive(ctx, handler, panelStore, tenant, itemOff, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	off := arrivalRow(t, ctx, tenant, connectorOff, "wamid.real.off")
	if itemState(t, ctx, tenant, itemOff) != "processed" || off.body != "oi desligado" || off.sector != sectorOff || off.inbox != inboxOff || off.status != "open" {
		t.Fatalf("inbound real não produziu a linha que PickInbound consulta: %+v state=%s", off, itemState(t, ctx, tenant, itemOff))
	}
	if pickTargets(t, ctx, tenant, itemOff) != 1 || arrivalClaims(t, ctx, tenant, itemOff) != 1 || assignee(t, ctx, tenant, off.conversation) != uuid.Nil {
		t.Fatal("flag desligada atribuiu a chegada real")
	}

	sectorOn, inboxOn, connectorOn := seedArrivalBox(t, ctx, tenant, agent.membership, "Caixa ligada")
	enabled := panelCall(api.Handler, http.MethodPut, "/api/panel/sectors/"+sectorOn.String()+"/distribution", "198.51.100.61", map[string]any{
		"expected_version": 0, "enabled": true,
	}, adminCookie, adminCSRF, origin, "")
	if enabled.Code != http.StatusOK || !strings.Contains(enabled.Body.String(), `"enabled":true`) {
		t.Fatalf("ligar flag: %d %s", enabled.Code, enabled.Body.String())
	}
	beat = panelCall(api.Handler, http.MethodPost, "/api/panel/presence/heartbeat", "198.51.100.61", map[string]any{
		"tab_id": uuid.New(),
	}, agentCookie, agentCSRF, origin, "")
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", beat.Code, beat.Body.String())
	}
	itemOn := seedPendingArrival(t, ctx, tenant, inboxOn, connectorOn, "551191110002", "wamid.real.on", "oi ligado")
	if messagesForExternal(t, ctx, tenant, connectorOn, "wamid.real.on") != 0 || itemState(t, ctx, tenant, itemOn) != "pending" {
		t.Fatal("a segunda chegada real já tinha mensagem ou não estava pendente")
	}
	if err := Arrive(ctx, handler, panelStore, tenant, itemOn, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	on := arrivalRow(t, ctx, tenant, connectorOn, "wamid.real.on")
	if itemState(t, ctx, tenant, itemOn) != "processed" || on.body != "oi ligado" || on.sector != sectorOn || on.inbox != inboxOn || on.status != "open" {
		t.Fatalf("inbound real com flag ligada não produziu a conversa: %+v state=%s", on, itemState(t, ctx, tenant, itemOn))
	}
	if arrivalClaims(t, ctx, tenant, itemOn) != 1 || assignee(t, ctx, tenant, on.conversation) != agent.membership {
		t.Fatal("flag ligada não atribuiu a chegada real")
	}
	if assignee(t, ctx, tenant, off.conversation) != uuid.Nil || pickTargets(t, ctx, tenant, itemOff) != 1 {
		t.Fatal("a chegada com flag desligada foi atribuída junto com a outra")
	}
}

func seedArrivalBox(t *testing.T, ctx context.Context, tenant, membership uuid.UUID, name string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	sector, inbox, connector := uuid.New(), uuid.New(), uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channel_connectors (tenant_id,id,connector_type,remote_origin) VALUES ($1,$2,'meta_cloud',$3)`, []any{tenant, connector, connector.String()}},
			{`INSERT INTO inboxes (tenant_id,id,connector_id,name) VALUES ($1,$2,$3,$4)`, []any{tenant, inbox, connector, name}},
			{`INSERT INTO sectors (tenant_id,id,name) VALUES ($1,$2,$3)`, []any{tenant, sector, name}},
			{`INSERT INTO sector_inboxes (tenant_id,sector_id,inbox_id) VALUES ($1,$2,$3)`, []any{tenant, sector, inbox}},
			{`INSERT INTO inbox_sector_defaults (tenant_id,inbox_id,sector_id) VALUES ($1,$2,$3)`, []any{tenant, inbox, sector}},
			{`INSERT INTO sector_memberships (tenant_id,sector_id,membership_id) VALUES ($1,$2,$3)`, []any{tenant, sector, membership}},
		}
		for _, item := range statements {
			if _, err := tx.Exec(ctx, item.q, item.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return sector, inbox, connector
}

func seedPendingArrival(t *testing.T, ctx context.Context, tenant, inbox, connector uuid.UUID, from, external, text string) uuid.UUID {
	t.Helper()
	payload, err := json.Marshal(pipeline.MessageItemPayload{
		AppID:         "arrival-real",
		WABAID:        "waba-real",
		PhoneNumberID: "5511999990100",
		From:          from,
		Text:          text,
		ExternalID:    external,
		DedupKey:      "message:" + external,
		OriginAt:      time.Now().UTC(),
		ProfileName:   "Cliente real",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, delivery := uuid.New(), uuid.New()
	var hash [32]byte
	copy(hash[:16], delivery[:])
	copy(hash[16:], delivery[:])
	conn, err := pgx.Connect(ctx, panelEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `INSERT INTO ingress_deliveries (id,source,app_id,body_sha256,body,state) VALUES ($1,'meta_cloud','arrival-real',$2,$3,'routed')`, delivery, hash[:], []byte{'x'}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO webhook_items (tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload) VALUES ($1,$2,$3,$4,$5,'5511999990100',$6,'message','pending',$7::jsonb)`, tenant, item, delivery, inbox, connector, "message:"+external, payload); err != nil {
		t.Fatal(err)
	}
	return item
}

type arrivedConversation struct {
	conversation uuid.UUID
	sector       uuid.UUID
	inbox        uuid.UUID
	status       string
	body         string
}

func arrivalRow(t *testing.T, ctx context.Context, tenant, connector uuid.UUID, external string) arrivedConversation {
	t.Helper()
	var row arrivedConversation
	err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT c.id, c.sector_id, c.inbox_id, c.status, m.body
			FROM messages m
			JOIN conversations c ON c.tenant_id = m.tenant_id AND c.id = m.conversation_id
			WHERE m.tenant_id = $1 AND m.connector_id = $2 AND m.external_message_id = $3
		`, tenant, connector, external).Scan(&row.conversation, &row.sector, &row.inbox, &row.status, &row.body)
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func messagesForExternal(t *testing.T, ctx context.Context, tenant, connector uuid.UUID, external string) int {
	t.Helper()
	var n int
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND connector_id=$2 AND external_message_id=$3`, tenant, connector, external).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func itemState(t *testing.T, ctx context.Context, tenant, item uuid.UUID) string {
	t.Helper()
	var state string
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state FROM webhook_items WHERE tenant_id=$1 AND id=$2`, tenant, item).Scan(&state)
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func pickTargets(t *testing.T, ctx context.Context, tenant, item uuid.UUID) int {
	t.Helper()
	var n int
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM (
				SELECT DISTINCT c.sector_id, c.inbox_id
				FROM webhook_items wi
				JOIN messages m
				  ON m.tenant_id = wi.tenant_id
				 AND m.connector_id = wi.connector_id
				 AND m.external_message_id = wi.payload->>'external_id'
				JOIN conversations c
				  ON c.tenant_id = m.tenant_id
				 AND c.id = m.conversation_id
				WHERE wi.tenant_id = $1
				  AND wi.id = $2
				  AND wi.kind = 'message'
				  AND c.assignee_id IS NULL
				  AND c.inbox_id IS NOT NULL
				  AND c.sector_id IS NOT NULL
				  AND c.status IN ('open', 'waiting_customer')
			) targets
		`, tenant, item).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedQueuedConversation(t *testing.T, ctx context.Context, tenant, inbox, sector, connector uuid.UUID, protocol string) uuid.UUID {
	t.Helper()
	conv, contact, identity := uuid.New(), uuid.New(), uuid.New()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (tenant_id,id,name) VALUES ($1,$2,$3)`, tenant, contact, protocol); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities (tenant_id,id,contact_id,connector_id,external_id) VALUES ($1,$2,$3,$4,$5)`, tenant, identity, contact, connector, protocol); err != nil {
			return err
		}
		return insertConversation(ctx, tx, tenant, conv, inbox, sector, contact, identity, protocol)
	}); err != nil {
		t.Fatal(err)
	}
	return conv
}

func seedProcessedItem(t *testing.T, ctx context.Context, tenant, inbox, connector, conv uuid.UUID, kind, state, external string) uuid.UUID {
	t.Helper()
	if kind == "message" {
		if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO messages (tenant_id,id,conversation_id,connector_id,external_message_id,direction,origin,visibility,body) VALUES ($1,$2,$3,$4,$5,'inbound','customer','public','oi')`, tenant, uuid.New(), conv, connector, external)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	item, delivery := uuid.New(), uuid.New()
	var hash [32]byte
	copy(hash[:16], delivery[:])
	copy(hash[16:], delivery[:])
	payload, err := json.Marshal(map[string]string{"external_id": external})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, panelEnv.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `INSERT INTO ingress_deliveries (id,source,app_id,body_sha256,body,state) VALUES ($1,'meta_cloud','arrival',$2,$3,'routed')`, delivery, hash[:], []byte{'x'}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO webhook_items (tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state,payload) VALUES ($1,$2,$3,$4,$5,'5511999999999',$6,$7,$8,$9::jsonb)`, tenant, item, delivery, inbox, connector, external, kind, state, payload); err != nil {
		t.Fatal(err)
	}
	return item
}

func shiftQueue(t *testing.T, ctx context.Context, tenant, conv uuid.UUID, delta time.Duration) {
	t.Helper()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE assignment_queue_intervals SET started_at = started_at + $3 WHERE tenant_id=$1 AND conversation_id=$2 AND ended_at IS NULL`, tenant, conv, delta)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func returnConversation(t *testing.T, ctx context.Context, tenant, conv uuid.UUID) {
	t.Helper()
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE conversations SET assignee_id=NULL, controller='menu', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, conv)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func arrivalClaims(t *testing.T, ctx context.Context, tenant, item uuid.UUID) int {
	t.Helper()
	var n int
	if err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM webhook_items WHERE tenant_id=$1 AND id=$2 AND payload ? 'distribution_arrival_claim'`, tenant, item).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func assignee(t *testing.T, ctx context.Context, tenant, conversation uuid.UUID) uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	err := panelStore.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT assignee_id FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, conversation).Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == nil {
		return uuid.Nil
	}
	return *id
}
