package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/channelpolicy"
	"chatisp/internal/erp"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/mail"
	"chatisp/internal/platform/metrics"
	"chatisp/internal/platform/testdb"
)

func TestPanelERPRoutes(t *testing.T) {
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
	authSvc, err := auth.New(auth.Options{Store: store, Mail: &mail.Fake{}, Policy: auth.DefaultPolicy})
	if err != nil {
		t.Fatal(err)
	}
	meter := metrics.New()
	fake := erp.NewFake(erp.AllCapabilities())
	panel := erp.NewPanel(erp.PanelOptions{DB: store, Meter: meter, Adapter: fake, Timeout: 2 * time.Second})
	const origin = "https://panel.example"
	api := httpapi.New(httpapi.Dependencies{
		Auth: authSvc, Store: store, ERP: panel, Metrics: meter, TrustedOrigins: []string{origin},
	})

	tenant, otherTenant := uuid.New(), uuid.New()
	password := "senha-painel-10"
	allowed := seedMember(t, ctx, store, tenant, "agent", password)
	plain := seedMember(t, ctx, store, tenant, "agent", password)
	outsider := seedMember(t, ctx, store, tenant, "agent", password)
	foreign := seedMember(t, ctx, store, otherTenant, "agent", password)
	world := seedChannel(t, ctx, store, tenant, allowed.membership, plain.membership, outsider.membership)
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		on := map[string]bool{}
		for _, name := range erp.AllCapabilities() {
			on[name] = true
		}
		conn := erp.Connection{
			TenantID: tenant, ID: uuid.New(), Driver: erp.DriverFake, Simulated: true,
			Enabled: true, Availability: erp.AvailabilityUp, SectorID: world.sector, Capabilities: on,
		}
		if err := erp.InsertConnection(ctx, tx, conn); err != nil {
			return err
		}
		for _, capability := range []string{erp.ListOpenInvoices, erp.GetInvoiceDocument, erp.GetPixCode} {
			if err := erp.InsertGrant(ctx, tx, tenant, allowed.membership, capability); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	allowedSession := loginERP(t, ctx, authSvc, allowed.email, password)
	plainSession := loginERP(t, ctx, authSvc, plain.email, password)
	outsiderSession := loginERP(t, ctx, authSvc, outsider.email, password)
	foreignSession := loginERP(t, ctx, authSvc, foreign.email, password)
	base := "/api/panel/conversations/" + world.conversation.String() + "/erp"

	callOn := func(handler http.Handler, method, path, body string, session issuedSession, want int) map[string]any {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Origin", origin)
		req.Header.Set(auth.CSRFHeader, session.csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: status %d body %s", method, path, rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			return nil
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json: %v %s", err, rec.Body.String())
		}
		return payload
	}
	call := func(method, path, body string, session issuedSession, want int) map[string]any {
		t.Helper()
		return callOn(api.Handler, method, path, body, session, want)
	}
	freshAPI := func() (*erp.Fake, http.Handler) {
		t.Helper()
		freshFake := erp.NewFake(erp.AllCapabilities())
		freshPanel := erp.NewPanel(erp.PanelOptions{DB: store, Meter: metrics.New(), Adapter: freshFake, Timeout: 2 * time.Second})
		return freshFake, httpapi.New(httpapi.Dependencies{
			Auth: authSvc, Store: store, ERP: freshPanel, Metrics: meter, TrustedOrigins: []string{origin},
		}).Handler
	}

	before := fake.Calls
	found := call(http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"id-casa"}`, allowedSession, http.StatusOK)
	customer, _ := found["customer"].(map[string]any)
	if customer["external_id"] != "fake-customer-casa" || found["simulated"] != true || found["driver"] != erp.DriverFake {
		t.Fatalf("identificar: %+v", found)
	}
	replayID := call(http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"id-casa"}`, allowedSession, http.StatusOK)
	replayCustomer, _ := replayID["customer"].(map[string]any)
	if replayID["replayed"] != true || fake.Calls != before+1 || replayCustomer["external_id"] != "fake-customer-casa" || replayCustomer["display_name"] == "" || replayID["consulted_at"] != found["consulted_at"] {
		t.Fatalf("replay identificar calls=%d payload=%+v", fake.Calls, replayID)
	}
	freshFake, freshHandler := freshAPI()
	freshID := callOn(freshHandler, http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"id-casa"}`, allowedSession, http.StatusOK)
	freshCustomer, _ := freshID["customer"].(map[string]any)
	if freshID["replayed"] != true || freshFake.Calls != 0 || freshCustomer["external_id"] != "fake-customer-casa" || freshID["consulted_at"] != found["consulted_at"] {
		t.Fatalf("replay identificar em outro processo: calls=%d %+v", freshFake.Calls, freshID)
	}
	spacedBefore := fake.Calls
	spacedBody := `{"reference":" ref-casa ","local_key":"id-espacos"}`
	spaced := call(http.MethodPost, base+"/identify", spacedBody, allowedSession, http.StatusOK)
	spacedCustomer, _ := spaced["customer"].(map[string]any)
	if spacedCustomer["external_id"] != "fake-customer-casa" {
		t.Fatalf("identificar com espaços: %+v", spaced)
	}
	spacedAgain := call(http.MethodPost, base+"/identify", spacedBody, allowedSession, http.StatusOK)
	if spacedAgain["replayed"] != true || fake.Calls != spacedBefore+1 || spacedAgain["consulted_at"] != spaced["consulted_at"] {
		t.Fatalf("replay com espaços calls=%d %+v", fake.Calls, spacedAgain)
	}
	freshFake, freshHandler = freshAPI()
	freshSpaced := callOn(freshHandler, http.MethodPost, base+"/identify", spacedBody, allowedSession, http.StatusOK)
	if freshSpaced["replayed"] != true || freshFake.Calls != 0 || freshSpaced["consulted_at"] != spaced["consulted_at"] {
		t.Fatalf("replay com espaços em outro processo: calls=%d %+v", freshFake.Calls, freshSpaced)
	}
	phoneEvents := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:SearchCustomer:conflict:invalid'`, tenant, world.conversation)
	call(http.MethodPost, base+"/identify", `{"reference":"+5511999999999","local_key":"id-telefone"}`, allowedSession, http.StatusUnprocessableEntity)
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:SearchCustomer:conflict:invalid'`, tenant, world.conversation) != phoneEvents+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.SearchCustomer' AND result='conflict' AND correlation_id='id-telefone'`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND position('5511' in coalesce(correlation_id, '')) > 0`, tenant) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='id-telefone'`, tenant) != 0 {
		t.Fatal("identificação inválida sem registro ou com efeito parcial")
	}
	longKey := strings.Repeat("k", 201)
	longIdentifyCalls := fake.Calls
	longIdentifyAudits := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.SearchCustomer' AND result='conflict'`, tenant)
	call(http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"`+longKey+`"}`, allowedSession, http.StatusUnprocessableEntity)
	if fake.Calls != longIdentifyCalls ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:SearchCustomer:conflict:invalid'`, tenant, world.conversation) != phoneEvents+2 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.SearchCustomer' AND result='conflict'`, tenant) != longIdentifyAudits+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND correlation_id=$2`, tenant, longKey) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key=$2`, tenant, longKey) != 0 {
		t.Fatal("local_key longa na identificação consultou o fake ou não registrou a recusa")
	}
	if _, err := erp.NormalizeReference("+5511999999999"); err == nil {
		t.Fatal("telefone aceito")
	}
	call(http.MethodPost, base+"/links", `{"external_customer_id":"+5511999999999","selected":true}`, allowedSession, http.StatusUnprocessableEntity)
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:link:conflict:invalid'`, tenant, world.conversation) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.link' AND result='conflict' AND correlation_id IS NULL`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_links WHERE tenant_id=$1`, tenant) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND position('5511' in coalesce(correlation_id, '')) > 0`, tenant) != 0 {
		t.Fatal("vínculo inválido sem registro ou com efeito parcial")
	}

	call(http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-casa","external_contract_id":"fake-contract-casa","selected":true}`, allowedSession, http.StatusOK)
	second := call(http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-comercio","external_contract_id":"fake-contract-comercio","selected":false}`, allowedSession, http.StatusOK)
	links := call(http.MethodGet, base+"/links", "", allowedSession, http.StatusOK)
	rawLinks, _ := links["links"].([]any)
	if len(rawLinks) != 2 {
		t.Fatalf("vínculos: %+v", links)
	}
	summary := call(http.MethodGet, base+"/summary", "", allowedSession, http.StatusOK)
	summaryCustomer, _ := summary["customer"].(map[string]any)
	if summaryCustomer["external_id"] != "fake-customer-casa" || summaryCustomer["display_name"] == "" || summary["consulted_at"] == nil {
		t.Fatalf("resumo: %+v", summary)
	}
	call(http.MethodGet, base+"/invoices?verification=verificacao-1", "", plainSession, http.StatusForbidden)
	var denied int
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND membership_id=$2 AND action='erp.ListOpenInvoices' AND result='denied'`, tenant, plain.membership).Scan(&denied)
	}); err != nil || denied < 1 {
		t.Fatalf("auditoria da recusa: %d %v", denied, err)
	}
	invoices := call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusOK)
	items, _ := invoices["invoices"].([]any)
	firstItem, _ := items[0].(map[string]any)
	if invoices["outcome"] != erp.OutcomeOK || len(items) != 1 || firstItem["external_id"] != "fake-inv-casa-1" || strings.Contains(jsonText(invoices), "valor") {
		t.Fatalf("faturas: %+v", invoices)
	}
	plainCalls := fake.Calls
	call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", plainSession, http.StatusForbidden)
	if fake.Calls != plainCalls {
		t.Fatalf("replay sem grant chamou o fake: %d -> %d", plainCalls, fake.Calls)
	}
	againInvoices := call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusOK)
	againItems, _ := againInvoices["invoices"].([]any)
	againItem, _ := againItems[0].(map[string]any)
	if againInvoices["replayed"] != true || fake.Calls != plainCalls || againItem["external_id"] != "fake-inv-casa-1" {
		t.Fatalf("replay faturas calls=%d %+v", fake.Calls, againInvoices)
	}
	freshFake, freshHandler = freshAPI()
	freshInvoices := callOn(freshHandler, http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusOK)
	freshItems, _ := freshInvoices["invoices"].([]any)
	freshItem, _ := freshItems[0].(map[string]any)
	if freshInvoices["replayed"] != true || freshFake.Calls != 0 || freshItem["external_id"] != "fake-inv-casa-1" {
		t.Fatalf("replay faturas em outro processo: calls=%d %+v", freshFake.Calls, freshInvoices)
	}
	longInvoiceCalls := fake.Calls
	longInvoiceAudits := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.ListOpenInvoices' AND result='conflict'`, tenant)
	call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key="+longKey, "", allowedSession, http.StatusUnprocessableEntity)
	if fake.Calls != longInvoiceCalls ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:ListOpenInvoices:conflict:invalid'`, tenant, world.conversation) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.ListOpenInvoices' AND result='conflict'`, tenant) != longInvoiceAudits+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND correlation_id=$2`, tenant, longKey) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key=$2`, tenant, longKey) != 0 {
		t.Fatal("local_key longa nas faturas consultou o fake ou não registrou a recusa")
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM erp_capability_grants WHERE tenant_id=$1 AND membership_id=$2 AND capability=$3`, tenant, allowed.membership, erp.ListOpenInvoices)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	revokedCalls := fake.Calls
	call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusForbidden)
	if fake.Calls != revokedCalls {
		t.Fatalf("replay após revogar grant chamou o fake: %d -> %d", revokedCalls, fake.Calls)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return erp.InsertGrant(ctx, tx, tenant, allowed.membership, erp.ListOpenInvoices)
	}); err != nil {
		t.Fatal(err)
	}
	var mismatchMessages int
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation).Scan(&mismatchMessages)
	}); err != nil {
		t.Fatal(err)
	}
	invalidCopy := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind LIKE 'erp_action:GetPixCode:conflict:invalid'`, tenant, world.conversation)
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-sem-req","artifact":"pix"}`, allowedSession, http.StatusUnprocessableEntity)
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-artefato","client_request_id":"req-artefato","artifact":"boleto"}`, allowedSession, http.StatusUnprocessableEntity)
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"+5511999999999","verification":"verificacao-1","local_key":"pix-fone","client_request_id":"req-fone","artifact":"pix"}`, allowedSession, http.StatusUnprocessableEntity)
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetPixCode:conflict:invalid'`, tenant, world.conversation) != invalidCopy+3 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.GetPixCode' AND result='conflict' AND correlation_id IN ('pix-sem-req','pix-artefato','pix-fone')`, tenant) != 3 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation) != mismatchMessages ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key IN ('pix-sem-req','pix-artefato','pix-fone')`, tenant) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND position('5511' in coalesce(correlation_id, '')) > 0`, tenant) != 0 {
		t.Fatal("segunda via inválida sem registro ou com efeito parcial")
	}
	longRequest := strings.Repeat("r", 201)
	longRequestCalls := fake.Calls
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-req-longo","client_request_id":"`+longRequest+`","artifact":"pix"}`, allowedSession, http.StatusUnprocessableEntity)
	if fake.Calls != longRequestCalls ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetPixCode:conflict:invalid'`, tenant, world.conversation) != invalidCopy+4 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.GetPixCode' AND result='conflict' AND correlation_id='pix-req-longo'`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND correlation_id=$2`, tenant, longRequest) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='pix-req-longo'`, tenant) != 0 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation) != mismatchMessages {
		t.Fatal("client_request_id longo na segunda via consultou o fake ou não registrou a recusa")
	}
	mismatchCalls := fake.Calls
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-comercio-1","verification":"verificacao-1","local_key":"pix-alheia","client_request_id":"req-alheia","artifact":"pix"}`, allowedSession, http.StatusNotFound)
	var mismatchNow int
	var invoiceEvents int
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation).Scan(&mismatchNow); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetPixCode:not_found:invoice'`, tenant, world.conversation).Scan(&invoiceEvents)
	}); err != nil || fake.Calls != mismatchCalls || mismatchNow != mismatchMessages || invoiceEvents != 1 {
		t.Fatalf("fatura de outro cliente calls=%d msgs=%d->%d eventos=%d err=%v", fake.Calls, mismatchMessages, mismatchNow, invoiceEvents, err)
	}
	copyBody := `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-1","client_request_id":"req-pix-1","artifact":"pix"}`
	sent := call(http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusOK)
	if sent["pix_code"] != "simulacao:fake-pix-casa-1" || sent["message_id"] == "" || sent["replayed"] == true {
		t.Fatalf("segunda via: %+v", sent)
	}
	callsAfterSend := fake.Calls
	again := call(http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusOK)
	if again["replayed"] != true || again["message_id"] != sent["message_id"] || again["attempt_id"] != sent["attempt_id"] || again["pix_code"] != sent["pix_code"] || fake.Calls != callsAfterSend {
		t.Fatalf("replay segunda via calls=%d %+v", fake.Calls, again)
	}
	freshFake, freshHandler = freshAPI()
	freshCopy := callOn(freshHandler, http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusOK)
	if freshCopy["replayed"] != true || freshFake.Calls != 0 || freshCopy["message_id"] != sent["message_id"] || freshCopy["attempt_id"] != sent["attempt_id"] || freshCopy["pix_code"] != "simulacao:fake-pix-casa-1" {
		t.Fatalf("replay segunda via em outro processo: calls=%d %+v", freshFake.Calls, freshCopy)
	}
	sectorDenials := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND result='denied' AND correlation_id IN ('id-casa','fat-1','pix-1')`, tenant)
	outboundBefore := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation)
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE erp_connections SET sector_id = NULL WHERE tenant_id = $1`, tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	bareCalls := fake.Calls
	noLeak := func(payload map[string]any) {
		t.Helper()
		raw := jsonText(payload)
		if strings.Contains(raw, "fake-customer") || strings.Contains(raw, "fake-inv") || strings.Contains(raw, "simulacao:") || payload["customer"] != nil || payload["invoices"] != nil || payload["pix_code"] != nil || payload["document"] != nil || payload["replayed"] == true {
			t.Fatalf("replay sem setor devolveu dado: %s", raw)
		}
	}
	noLeak(call(http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"id-casa"}`, allowedSession, http.StatusForbidden))
	noLeak(call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusForbidden))
	noLeak(call(http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusForbidden))
	if fake.Calls != bareCalls {
		t.Fatalf("replay sem setor chamou o fake: %d -> %d", bareCalls, fake.Calls)
	}
	freshFake, freshHandler = freshAPI()
	noLeak(callOn(freshHandler, http.MethodPost, base+"/identify", `{"reference":"ref-casa","local_key":"id-casa"}`, allowedSession, http.StatusForbidden))
	noLeak(callOn(freshHandler, http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-1", "", allowedSession, http.StatusForbidden))
	noLeak(callOn(freshHandler, http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusForbidden))
	if freshFake.Calls != 0 || fake.Calls != bareCalls ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND direction='outbound'`, tenant, world.conversation) != outboundBefore ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key IN ('id-casa','fat-1','pix-1')`, tenant) != 3 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind IN ('erp_action:SearchCustomer:denied:sector','erp_action:ListOpenInvoices:denied:sector','erp_action:GetPixCode:denied:sector')`, tenant, world.conversation) != 6 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND result='denied' AND correlation_id IN ('id-casa','fat-1','pix-1')`, tenant) != sectorDenials+6 {
		t.Fatalf("replay sem setor calls=%d fresh=%d auditoria=%d", fake.Calls, freshFake.Calls, sectorDenials)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE erp_connections SET sector_id = $2 WHERE tenant_id = $1`, tenant, world.sector)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-1","client_request_id":"req-inexistente","artifact":"pix"}`, allowedSession, http.StatusConflict)
	collisionID := uuid.New()
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body,local_state,client_request_id,trusted_at) VALUES($1,$2,$3,$4,'outbound','human','public','outra','queued','req-colisao',$5)`, tenant, collisionID, world.conversation, world.connector, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	collided := call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-1","client_request_id":"req-colisao","artifact":"pix"}`, allowedSession, http.StatusConflict)
	if strings.Contains(jsonText(collided), collisionID.String()) || strings.Contains(jsonText(collided), sent["message_id"].(string)) {
		t.Fatalf("conflito devolveu mensagem: %+v", collided)
	}
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-comercio-1","verification":"verificacao-1","local_key":"pix-1","client_request_id":"req-pix-1","artifact":"pix"}`, allowedSession, http.StatusConflict)
	var conflictEvents, conflictAudits int
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetPixCode:conflict:local_key'`, tenant, world.conversation).Scan(&conflictEvents); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.GetPixCode' AND result='conflict'`, tenant).Scan(&conflictAudits)
	}); err != nil || conflictEvents < 1 || conflictAudits < 1 {
		t.Fatalf("conflito eventos=%d auditoria=%d err=%v", conflictEvents, conflictAudits, err)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM erp_capability_grants WHERE tenant_id=$1 AND membership_id=$2 AND capability=$3`, tenant, allowed.membership, erp.GetPixCode)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	revokedPix := fake.Calls
	call(http.MethodPost, base+"/second-copy", copyBody, allowedSession, http.StatusForbidden)
	if fake.Calls != revokedPix {
		t.Fatalf("replay da segunda via após revogar grant chamou o fake: %d -> %d", revokedPix, fake.Calls)
	}
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return erp.InsertGrant(ctx, tx, tenant, allowed.membership, erp.GetPixCode)
	}); err != nil {
		t.Fatal(err)
	}
	var queued int
	var body string
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_attempts WHERE tenant_id=$1 AND conversation_id=$2 AND state='queued'`, tenant, world.conversation).Scan(&queued); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT body FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND client_request_id='req-pix-1'`, tenant, world.conversation).Scan(&body)
	}); err != nil || queued != 1 || !strings.Contains(body, "simulacao:fake-pix-casa-1") {
		t.Fatalf("outbox queued=%d body=%q err=%v", queued, body, err)
	}
	raceBefore := fake.Calls
	sameID := overlapERP(t, api.Handler, origin, allowedSession, repeatCall(6, http.MethodPost, base+"/identify", `{"reference":" ref-casa ","local_key":"id-corrida"}`))
	if countCodes(sameID, http.StatusOK) != 6 || fake.Calls != raceBefore+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='id-corrida'`, tenant) != 1 {
		t.Fatalf("corrida identificar codes=%v calls=%d", codesOf(sameID), fake.Calls)
	}
	var idStamp any
	for _, reply := range sameID {
		payload := decodeERP(t, reply.body)
		customer, _ := payload["customer"].(map[string]any)
		if customer["external_id"] != "fake-customer-casa" {
			t.Fatalf("corrida identificar: %+v", payload)
		}
		if idStamp == nil {
			idStamp = payload["consulted_at"]
		} else if payload["consulted_at"] != idStamp {
			t.Fatalf("corrida identificar instável: %+v", payload)
		}
	}
	mixedID := overlapERP(t, api.Handler, origin, allowedSession, mixCalls(3,
		erpCall{http.MethodPost, base + "/identify", `{"reference":"ref-casa","local_key":"id-disputa"}`},
		erpCall{http.MethodPost, base + "/identify", `{"reference":"ref-comercio","local_key":"id-disputa"}`}))
	if countCodes(mixedID, http.StatusOK) != 3 || countCodes(mixedID, http.StatusConflict) != 3 || fake.Calls != raceBefore+2 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='id-disputa'`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:SearchCustomer:conflict:local_key'`, tenant, world.conversation) < 1 {
		t.Fatalf("disputa identificar codes=%v calls=%d", codesOf(mixedID), fake.Calls)
	}
	copyRace := `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-corrida","client_request_id":"req-corrida","artifact":"pix"}`
	copyBefore := fake.Calls
	sameCopy := overlapERP(t, api.Handler, origin, allowedSession, repeatCall(6, http.MethodPost, base+"/second-copy", copyRace))
	if countCodes(sameCopy, http.StatusOK) != 6 || fake.Calls != copyBefore+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='pix-corrida'`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND client_request_id='req-corrida'`, tenant, world.conversation) != 1 {
		t.Fatalf("corrida segunda via codes=%v calls=%d", codesOf(sameCopy), fake.Calls)
	}
	var copyMessage any
	for _, reply := range sameCopy {
		payload := decodeERP(t, reply.body)
		if payload["pix_code"] != "simulacao:fake-pix-casa-1" || payload["message_id"] == "" {
			t.Fatalf("corrida segunda via: %+v", payload)
		}
		if copyMessage == nil {
			copyMessage = payload["message_id"]
		} else if payload["message_id"] != copyMessage || payload["attempt_id"] == "" {
			t.Fatalf("corrida segunda via par divergente: %+v", payload)
		}
	}
	mixedCopy := overlapERP(t, api.Handler, origin, allowedSession, mixCalls(3,
		erpCall{http.MethodPost, base + "/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-disputa","client_request_id":"req-disputa-a","artifact":"pix"}`},
		erpCall{http.MethodPost, base + "/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-disputa","client_request_id":"req-disputa-b","artifact":"pix"}`}))
	if countCodes(mixedCopy, http.StatusOK) != 3 || countCodes(mixedCopy, http.StatusConflict) != 3 || fake.Calls != copyBefore+2 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND client_request_id IN ('req-disputa-a','req-disputa-b')`, tenant, world.conversation) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='pix-disputa'`, tenant) != 1 {
		t.Fatalf("disputa segunda via codes=%v calls=%d", codesOf(mixedCopy), fake.Calls)
	}
	var winner string
	for _, reply := range mixedCopy {
		if reply.code != http.StatusOK {
			if strings.Contains(string(reply.body), "message_id") {
				t.Fatalf("conflito devolveu mensagem: %s", reply.body)
			}
			continue
		}
		payload := decodeERP(t, reply.body)
		if winner == "" {
			winner = payload["message_id"].(string)
		} else if payload["message_id"] != winner {
			t.Fatalf("disputa segunda via par divergente: %+v", payload)
		}
	}
	protocolInvalid := erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:protocol:conflict:invalid'`, tenant, world.conversation)
	call(http.MethodPost, base+"/protocol", `{"external_protocol":" "}`, allowedSession, http.StatusUnprocessableEntity)
	var externalBefore *string
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT external_protocol FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, world.conversation).Scan(&externalBefore)
	}); err != nil || externalBefore != nil ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:protocol:conflict:invalid'`, tenant, world.conversation) != protocolInvalid+1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.protocol' AND result='conflict' AND correlation_id IS NULL`, tenant) < 1 {
		t.Fatal("protocolo vazio sem registro ou com efeito parcial")
	}
	protocol := call(http.MethodPost, base+"/protocol", `{"external_protocol":"ERP-99"}`, allowedSession, http.StatusOK)
	if protocol["local_protocol"] != "P-LOCAL-1" {
		t.Fatalf("protocolo: %+v", protocol)
	}
	var local, external string
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT protocol, external_protocol FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, world.conversation).Scan(&local, &external)
	}); err != nil || local != "P-LOCAL-1" || external != "ERP-99" {
		t.Fatalf("protocolo gravado local=%s external=%s err=%v", local, external, err)
	}
	longProtocol := strings.Repeat("E", 129)
	call(http.MethodPost, base+"/protocol", `{"external_protocol":"`+longProtocol+`"}`, allowedSession, http.StatusUnprocessableEntity)
	var externalAfter string
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT external_protocol FROM conversations WHERE tenant_id=$1 AND id=$2`, tenant, world.conversation).Scan(&externalAfter)
	}); err != nil || externalAfter != "ERP-99" ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:protocol:conflict:invalid'`, tenant, world.conversation) != protocolInvalid+2 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND position($2 in coalesce(correlation_id, '')) > 0`, tenant, longProtocol[:32]) != 0 {
		t.Fatal("protocolo longo sem registro ou com efeito parcial")
	}
	secondLinks, _ := second["links"].([]any)
	secondLink, _ := secondLinks[0].(map[string]any)
	call(http.MethodDelete, base+"/links/"+secondLink["id"].(string), "", allowedSession, http.StatusOK)
	left := call(http.MethodGet, base+"/links", "", allowedSession, http.StatusOK)
	if rows, _ := left["links"].([]any); len(rows) != 1 {
		t.Fatalf("depois de desvincular: %+v", left)
	}

	otherConversation, otherIdentity := uuid.New(), uuid.New()
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, otherIdentity, tenant, world.contact, world.connector, "wa-"+otherIdentity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','menu','P-LOCAL-2','202611',1)`, otherConversation, tenant, world.inbox, world.otherSector, world.contact, otherIdentity)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	otherBase := "/api/panel/conversations/" + otherConversation.String() + "/erp"
	otherLink := call(http.MethodPost, otherBase+"/links", `{"external_customer_id":"fake-customer-comercio","external_contract_id":"fake-contract-comercio","selected":true}`, outsiderSession, http.StatusOK)
	otherRows, _ := otherLink["links"].([]any)
	otherRow, _ := otherRows[0].(map[string]any)
	call(http.MethodDelete, base+"/links/"+otherRow["id"].(string), "", allowedSession, http.StatusNotFound)
	kept := call(http.MethodGet, otherBase+"/links", "", outsiderSession, http.StatusOK)
	keptRows, _ := kept["links"].([]any)
	if len(keptRows) != 1 {
		t.Fatalf("vínculo de outro setor foi removido: %+v", kept)
	}
	call(http.MethodGet, base+"/summary", "", outsiderSession, http.StatusForbidden)
	call(http.MethodGet, base+"/links", "", foreignSession, http.StatusNotFound)
	var foreignLinks int
	if err = store.WithTenantTx(ctx, otherTenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM erp_links WHERE contact_id=$1`, world.contact).Scan(&foreignLinks)
	}); err != nil || foreignLinks != 0 {
		t.Fatalf("RLS: %d %v", foreignLinks, err)
	}

	assertERPContractPair(t, ctx, store, tenant, api.Handler, origin, allowedSession, world)
	overlapProtocolWithLinkEdits(t, ctx, env.AdminURL, api.Handler, origin, allowedSession, store, tenant, world)
	overlapSecondCopyWithDispatch(t, ctx, env.AdminURL, api.Handler, origin, allowedSession, store, tenant, world)

	panel.SetTimeout(120 * time.Millisecond)
	fake.SetDelay(3 * time.Second)
	started := time.Now()
	call(http.MethodGet, base+"/summary", "", allowedSession, http.StatusServiceUnavailable)
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("timeout do fake não foi respeitado: %s", time.Since(started))
	}
	fake.SetDelay(0)
	fake.SetDown(true)
	call(http.MethodGet, base+"/summary", "", allowedSession, http.StatusServiceUnavailable)
	call(http.MethodGet, base+"/summary", "", allowedSession, http.StatusServiceUnavailable)
	openCalls := fake.Calls
	fake.SetDelay(3 * time.Second)
	started = time.Now()
	call(http.MethodGet, base+"/summary", "", allowedSession, http.StatusServiceUnavailable)
	if fake.Calls != openCalls || time.Since(started) > time.Second {
		t.Fatalf("circuito aberto chamou o fake calls=%d->%d em %s", openCalls, fake.Calls, time.Since(started))
	}
	var timeoutEvents, openEvents, timeoutAudits, openAudits int
	if err = store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetCustomerSummary:unavailable:timeout'`, tenant, world.conversation).Scan(&timeoutEvents); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetCustomerSummary:unavailable:circuit_open'`, tenant, world.conversation).Scan(&openEvents); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.GetCustomerSummary' AND result='unavailable:timeout'`, tenant).Scan(&timeoutAudits); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.GetCustomerSummary' AND result='unavailable:circuit_open'`, tenant).Scan(&openAudits)
	}); err != nil || timeoutEvents < 1 || openEvents < 1 || timeoutAudits < 1 || openAudits < 1 {
		t.Fatalf("linha do tempo timeout=%d circuito=%d auditoria timeout=%d circuito=%d err=%v", timeoutEvents, openEvents, timeoutAudits, openAudits, err)
	}
	scraped, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(scraped, []byte(`chatisp_erp_connector_calls_total`)) || !bytes.Contains(scraped, []byte(`connector="fake"`)) {
		t.Fatalf("métrica ausente:\n%s", scraped)
	}
}

type erpCall struct {
	method string
	path   string
	body   string
}

type erpReply struct {
	code int
	body []byte
}

func erpCount(t *testing.T, ctx context.Context, store *db.Store, tenant uuid.UUID, query string, args ...any) int {
	t.Helper()
	var n int
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func repeatCall(n int, method, path, body string) []erpCall {
	out := make([]erpCall, n)
	for i := range out {
		out[i] = erpCall{method, path, body}
	}
	return out
}

func mixCalls(n int, left, right erpCall) []erpCall {
	out := make([]erpCall, 0, n*2)
	for i := 0; i < n; i++ {
		out = append(out, left, right)
	}
	return out
}

// assertERPContractPair checks a selected customer against the catalog contract.
// An empty contract still resolves that customer. A foreign contract returns no data.
func assertERPContractPair(t *testing.T, ctx context.Context, store *db.Store, tenant uuid.UUID, handler http.Handler, origin string, session issuedSession, world channelWorld) {
	t.Helper()
	identity, conversation := uuid.New(), uuid.New()
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, world.contact, world.connector, "wa-"+identity.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','menu','P-LOCAL-PAR','202611',1)`, conversation, tenant, world.inbox, world.sector, world.contact, identity); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at) VALUES($1,$2,$3,$4,'inbound','customer','public','oi','received',$5)`, tenant, uuid.New(), conversation, world.connector, time.Now().Add(-time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	base := "/api/panel/conversations/" + conversation.String() + "/erp"
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Origin", origin)
		req.Header.Set(auth.CSRFHeader, session.csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: status %d body %s", method, path, rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			return nil
		}
		return decodeERP(t, rec.Body.Bytes())
	}
	optional := call(http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-comercio","selected":true}`, http.StatusOK)
	optionalRows, _ := optional["links"].([]any)
	optionalRow, _ := optionalRows[0].(map[string]any)
	summary := call(http.MethodGet, base+"/summary", "", http.StatusOK)
	customer, _ := summary["customer"].(map[string]any)
	if summary["outcome"] != erp.OutcomeOK || customer["external_id"] != "fake-customer-comercio" || customer["contract_id"] != "fake-contract-comercio" {
		t.Fatalf("contrato opcional: %+v", summary)
	}
	invoices := call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-opcional", "", http.StatusOK)
	items, _ := invoices["invoices"].([]any)
	item, _ := items[0].(map[string]any)
	if invoices["outcome"] != erp.OutcomeOK || len(items) != 1 || item["external_id"] != "fake-inv-comercio-1" {
		t.Fatalf("faturas sem contrato: %+v", invoices)
	}
	call(http.MethodDelete, base+"/links/"+optionalRow["id"].(string), "", http.StatusOK)
	call(http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-casa","external_contract_id":"fake-contract-comercio","selected":true}`, http.StatusOK)
	mismatch := call(http.MethodGet, base+"/summary", "", http.StatusOK)
	if mismatch["outcome"] != erp.OutcomeNotFound || mismatch["customer"] != nil || strings.Contains(jsonText(mismatch), "fake-contract-casa") || strings.Contains(jsonText(mismatch), "fake-inv-casa") {
		t.Fatalf("resumo de par incompatível: %+v", mismatch)
	}
	missed := call(http.MethodGet, base+"/invoices?verification=verificacao-1&local_key=fat-par-errado", "", http.StatusOK)
	if missed["outcome"] != erp.OutcomeNotFound || missed["invoices"] != nil || strings.Contains(jsonText(missed), "fake-inv-casa-1") {
		t.Fatalf("faturas de par incompatível: %+v", missed)
	}
	call(http.MethodPost, base+"/second-copy", `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-par-errado","client_request_id":"req-par-errado","artifact":"pix"}`, http.StatusNotFound)
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:GetCustomerSummary:not_found:not_found'`, tenant, conversation) < 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:ListOpenInvoices:not_found:not_found'`, tenant, conversation) < 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND client_request_id='req-par-errado'`, tenant, conversation) != 0 {
		t.Fatal("par incompatível sem registro ou com mensagem")
	}
}

// overlapProtocolWithLinkEdits pauses the protocol after it locks the link row
// and before it updates the conversation. Selection and removal must wait on the
// conversation advisory, finish, and keep their timeline rows.
func overlapProtocolWithLinkEdits(t *testing.T, ctx context.Context, adminURL string, handler http.Handler, origin string, session issuedSession, store *db.Store, tenant uuid.UUID, world channelWorld) {
	t.Helper()
	lockCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	identity, conversation := uuid.New(), uuid.New()
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, identity, tenant, world.contact, world.connector, "wa-"+identity.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','menu','P-LOCAL-LOCK','202611',1)`, conversation, tenant, world.inbox, world.sector, world.contact, identity)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	base := "/api/panel/conversations/" + conversation.String() + "/erp"
	linked := erpExchange(t, lockCtx, handler, origin, session, http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-casa","external_contract_id":"fake-contract-casa","selected":true}`)
	if linked.code != http.StatusOK {
		t.Fatalf("vínculo inicial: %d %s", linked.code, linked.body)
	}
	rows, _ := decodeERP(t, linked.body)["links"].([]any)
	row, _ := rows[0].(map[string]any)
	linkID, _ := row["id"].(string)
	if linkID == "" {
		t.Fatalf("vínculo inicial: %s", linked.body)
	}
	admin, err := pgx.Connect(lockCtx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err = admin.Exec(lockCtx, `
		CREATE OR REPLACE FUNCTION erp_test_hold_protocol() RETURNS trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
		  IF NEW.external_protocol = 'ERP-HOLD' THEN
		    PERFORM pg_advisory_xact_lock(hashtextextended('erp-test-barrier', 0));
		  END IF;
		  RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(lockCtx, `
		CREATE TRIGGER erp_test_hold_protocol
		BEFORE UPDATE OF external_protocol ON erp_links
		FOR EACH ROW
		EXECUTE FUNCTION erp_test_hold_protocol()`); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP TRIGGER IF EXISTS erp_test_hold_protocol ON erp_links`)
	watch, err := pgx.Connect(lockCtx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close(context.Background())
	holder, err := admin.Begin(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(lockCtx, `SELECT pg_advisory_xact_lock(hashtextextended('erp-test-barrier', 0))`); err != nil {
		t.Fatal(err)
	}
	protocolDone := make(chan erpReply, 1)
	go func() {
		protocolDone <- erpExchange(t, lockCtx, handler, origin, session, http.MethodPost, base+"/protocol", `{"external_protocol":"ERP-HOLD"}`)
	}()
	waitERPQueries(t, lockCtx, watch, func(query string) bool {
		return strings.Contains(query, "UPDATE erp_links") && strings.Contains(query, "external_protocol")
	}, 1)
	linkDone := make(chan erpReply, 1)
	unlinkDone := make(chan erpReply, 1)
	go func() {
		linkDone <- erpExchange(t, lockCtx, handler, origin, session, http.MethodPost, base+"/links", `{"external_customer_id":"fake-customer-comercio","external_contract_id":"fake-contract-comercio","selected":true}`)
	}()
	go func() {
		unlinkDone <- erpExchange(t, lockCtx, handler, origin, session, http.MethodDelete, base+"/links/"+linkID, "")
	}()
	waiters := waitERPQueries(t, lockCtx, watch, func(query string) bool {
		return strings.Contains(query, "pg_advisory_xact_lock(hashtextextended($1, 0))")
	}, 2)
	var tupleLocks int
	if err = watch.QueryRow(lockCtx, `
		SELECT count(*)
		FROM pg_locks l
		JOIN pg_class c ON c.oid = l.relation
		WHERE l.pid = ANY($1) AND c.relname = 'conversations' AND l.granted AND l.locktype = 'tuple'`, waiters).Scan(&tupleLocks); err != nil {
		t.Fatal(err)
	}
	if tupleLocks != 0 {
		t.Fatalf("vínculo segurou a conversa enquanto esperava o protocolo: locks=%d pids=%v", tupleLocks, waiters)
	}
	if err = holder.Commit(lockCtx); err != nil {
		t.Fatal(err)
	}
	protocol := <-protocolDone
	linkedAgain := <-linkDone
	unlinked := <-unlinkDone
	if protocol.code != http.StatusOK || linkedAgain.code != http.StatusOK || unlinked.code != http.StatusOK {
		t.Fatalf("concorrência protocolo=%d %s vínculo=%d %s remoção=%d %s", protocol.code, protocol.body, linkedAgain.code, linkedAgain.body, unlinked.code, unlinked.body)
	}
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:protocol:ok:protocol'`, tenant, conversation) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:link:ok:linked'`, tenant, conversation) != 2 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM conversation_events WHERE tenant_id=$1 AND conversation_id=$2 AND kind='erp_action:unlink:ok:unlinked'`, tenant, conversation) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='erp.protocol' AND result='ok' AND correlation_id='ERP-HOLD'`, tenant) != 1 {
		t.Fatal("protocolo concorrente com o vínculo terminou sem os registros")
	}
}

func erpExchange(t *testing.T, ctx context.Context, handler http.Handler, origin string, session issuedSession, method, path, body string) erpReply {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Origin", origin)
	req.Header.Set(auth.CSRFHeader, session.csrf)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return erpReply{code: rec.Code, body: append([]byte(nil), rec.Body.Bytes()...)}
}

func waitERPQueries(t *testing.T, ctx context.Context, watch *pgx.Conn, match func(string) bool, want int) []int32 {
	t.Helper()
	for {
		rows, err := watch.Query(ctx, `
			SELECT pid, query
			FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		if err != nil {
			t.Fatal(err)
		}
		var pids []int32
		for rows.Next() {
			var pid int32
			var query string
			if err = rows.Scan(&pid, &query); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if match(query) {
				pids = append(pids, pid)
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(pids) >= want {
			return pids
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(15 * time.Millisecond):
		}
	}
}

// overlapSecondCopyWithDispatch holds the dispatch fence up to the contact,
// waits until the second copy blocks on the connector, then takes the
// conversation. Both sides must finish: the fence commits and the copy queues
// one text. A copy that shares the conversation first deadlocks here.
func overlapSecondCopyWithDispatch(t *testing.T, ctx context.Context, adminURL string, handler http.Handler, origin string, session issuedSession, store *db.Store, tenant uuid.UUID, world channelWorld) {
	t.Helper()
	lockCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	admin, err := pgx.Connect(lockCtx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	watch, err := pgx.Connect(lockCtx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close(context.Background())
	holder, err := admin.Begin(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	var holderPID int
	if err = holder.QueryRow(lockCtx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if err = channelpolicy.LockConnector(lockCtx, holder, tenant, world.connector, true); err != nil {
		t.Fatal(err)
	}
	if err = channelpolicy.LockContact(lockCtx, holder, tenant, world.contact, false); err != nil {
		t.Fatal(err)
	}
	type sent struct {
		code int
		body []byte
	}
	done := make(chan sent, 1)
	go func() {
		body := `{"invoice_id":"fake-inv-casa-1","verification":"verificacao-1","local_key":"pix-cerca","client_request_id":"req-cerca","artifact":"pix"}`
		req := httptest.NewRequest(http.MethodPost, "/api/panel/conversations/"+world.conversation.String()+"/erp/second-copy", strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set(auth.CSRFHeader, session.csrf)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		done <- sent{code: rec.Code, body: append([]byte(nil), rec.Body.Bytes()...)}
	}()
	var waiter int
	for waiter == 0 {
		err = watch.QueryRow(lockCtx, `
			SELECT pid FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> $1 AND $1 = ANY(pg_blocking_pids(pid))
			 ORDER BY pid LIMIT 1`, holderPID).Scan(&waiter)
		if err == nil && waiter > 0 {
			break
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		waiter = 0
		select {
		case result := <-done:
			t.Fatalf("segunda via terminou antes de esperar o conector: %d %s", result.code, result.body)
		case <-lockCtx.Done():
			t.Fatal(lockCtx.Err())
		case <-time.After(15 * time.Millisecond):
		}
	}
	var holdsConversation bool
	var waitingQuery string
	if err = watch.QueryRow(lockCtx, `
		SELECT
			EXISTS (
				SELECT 1 FROM pg_locks l
				JOIN pg_class c ON c.oid = l.relation
				WHERE l.pid = $1 AND c.relname = 'conversations' AND l.granted AND l.locktype = 'tuple'
			),
			query
		FROM pg_stat_activity WHERE pid = $1`, waiter).Scan(&holdsConversation, &waitingQuery); err != nil {
		t.Fatal(err)
	}
	if holdsConversation || !strings.Contains(waitingQuery, "FROM channel_connectors") {
		t.Fatalf("segunda via segurou a conversa ou não esperou o conector: conversa=%t query=%s", holdsConversation, waitingQuery)
	}
	if _, err = holder.Exec(lockCtx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	if err = channelpolicy.LockConversation(lockCtx, holder, tenant, world.conversation, true); err != nil {
		t.Fatalf("cerca não conseguiu a conversa: %v", err)
	}
	if err = holder.Commit(lockCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.code != http.StatusOK {
			t.Fatalf("segunda via sob a cerca: %d %s", result.code, result.body)
		}
		payload := decodeERP(t, result.body)
		if payload["message_id"] == "" || payload["pix_code"] != "simulacao:fake-pix-casa-1" {
			t.Fatalf("segunda via sob a cerca: %+v", payload)
		}
	case <-lockCtx.Done():
		t.Fatal(lockCtx.Err())
	}
	if erpCount(t, ctx, store, tenant, `SELECT count(*) FROM erp_actions WHERE tenant_id=$1 AND local_key='pix-cerca'`, tenant) != 1 ||
		erpCount(t, ctx, store, tenant, `SELECT count(*) FROM messages WHERE tenant_id=$1 AND conversation_id=$2 AND client_request_id='req-cerca'`, tenant, world.conversation) != 1 {
		t.Fatal("cerca não deixou a ação e a mensagem da segunda via")
	}
}

func overlapERP(t *testing.T, handler http.Handler, origin string, session issuedSession, specs []erpCall) []erpReply {
	t.Helper()
	out := make([]erpReply, len(specs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range specs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			spec := specs[i]
			req := httptest.NewRequest(spec.method, spec.path, strings.NewReader(spec.body))
			req.Header.Set("Origin", origin)
			req.Header.Set(auth.CSRFHeader, session.csrf)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.token})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			out[i] = erpReply{code: rec.Code, body: append([]byte(nil), rec.Body.Bytes()...)}
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

func countCodes(replies []erpReply, code int) int {
	n := 0
	for _, reply := range replies {
		if reply.code == code {
			n++
		}
	}
	return n
}

func codesOf(replies []erpReply) []int {
	out := make([]int, len(replies))
	for i, reply := range replies {
		out[i] = reply.code
	}
	return out
}

func decodeERP(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json: %v %s", err, raw)
	}
	return payload
}

type memberSeed struct {
	email      string
	membership uuid.UUID
}

type issuedSession struct {
	token string
	csrf  string
}

type channelWorld struct {
	sector       uuid.UUID
	otherSector  uuid.UUID
	conversation uuid.UUID
	contact      uuid.UUID
	connector    uuid.UUID
	inbox        uuid.UUID
	identity     uuid.UUID
}

func seedMember(t *testing.T, ctx context.Context, store *db.Store, tenant uuid.UUID, role, password string) memberSeed {
	t.Helper()
	user, membership := uuid.New(), uuid.New()
	email := user.String() + "@example.com"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithPlatform(ctx, func(ctx context.Context) error {
		if _, err := store.GetTenantByID(ctx, tenant); err != nil {
			if _, err = store.CreateTenant(ctx, db.NewTenant{ID: tenant, Timezone: "America/Sao_Paulo", Status: "active", Limits: []byte(`{}`)}); err != nil {
				return err
			}
		}
		_, err := store.CreateUser(ctx, db.NewUser{ID: user, Email: email, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithTenant(ctx, tenant, func(ctx context.Context) error {
		_, err := store.CreateMembership(ctx, db.NewMembership{ID: membership, UserID: user, Role: role, Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithAuth(ctx, func(ctx context.Context) error {
		return store.SetPasswordHash(ctx, user, hash)
	}); err != nil {
		t.Fatal(err)
	}
	return memberSeed{email: email, membership: membership}
}

func seedChannel(t *testing.T, ctx context.Context, store *db.Store, tenant, allowed, plain, outsider uuid.UUID) channelWorld {
	t.Helper()
	sector, otherSector := uuid.New(), uuid.New()
	connector, inbox := uuid.New(), uuid.New()
	contact, identity, conversation := uuid.New(), uuid.New(), uuid.New()
	app, waba := uuid.New(), uuid.New()
	if err := store.WithTenantTx(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		statements := []struct {
			q string
			a []any
		}{
			{`INSERT INTO channel_connectors(id,tenant_id,connector_type,remote_origin,status,enabled,version) VALUES($1,$2,'meta_cloud',$3,'connected',true,'1')`, []any{connector, tenant, connector.String()}},
			{`INSERT INTO inboxes(id,tenant_id,connector_id,name) VALUES($1,$2,$3,'ERP')`, []any{inbox, tenant, connector}},
			{`INSERT INTO sectors(tenant_id,id,name) VALUES($1,$2,'Casa'),($1,$3,'Outro')`, []any{tenant, sector, otherSector}},
			{`INSERT INTO sector_inboxes(tenant_id,sector_id,inbox_id) VALUES($1,$2,$3),($1,$4,$3)`, []any{tenant, sector, inbox, otherSector}},
			{`INSERT INTO sector_memberships(tenant_id,sector_id,membership_id) VALUES($1,$2,$3),($1,$2,$4),($1,$5,$6)`, []any{tenant, sector, allowed, plain, otherSector, outsider}},
			{`INSERT INTO contacts(id,tenant_id,name) VALUES($1,$2,'Pessoa')`, []any{contact, tenant}},
			{`INSERT INTO contact_channel_identities(id,tenant_id,contact_id,connector_id,external_id) VALUES($1,$2,$3,$4,$5)`, []any{identity, tenant, contact, connector, "wa-" + identity.String()}},
			{`INSERT INTO conversations(id,tenant_id,inbox_id,sector_id,contact_id,contact_identity_id,status,controller,protocol,protocol_period,cycle_number) VALUES($1,$2,$3,$4,$5,$6,'open','menu','P-LOCAL-1','202611',1)`, []any{conversation, tenant, inbox, sector, contact, identity}},
			{`SELECT bind_meta_app($1,$2,$2,'v1',$3,'ingress_registry',NULL,'meta_app_registry',$2,'app_secret',$4,true,NULL)`, []any{app.String(), app, []byte{1}, "ingress_registry\x1fmeta_app_registry\x1f" + app.String() + "\x1fapp_secret"}},
			{`INSERT INTO meta_wabas(tenant_id,id,meta_app_id,waba_id,status) VALUES($1,$2,$3,$4,'active')`, []any{tenant, waba, app, waba.String()}},
			{`INSERT INTO meta_phone_numbers(tenant_id,id,meta_waba_id,phone_number_id,status,connector_id,inbox_id) VALUES($1,$2,$3,$4,'active',$5,$6)`, []any{tenant, uuid.New(), waba, uuid.NewString(), connector, inbox}},
			{`INSERT INTO messages(tenant_id,id,conversation_id,connector_id,direction,origin,visibility,body,local_state,trusted_at) VALUES($1,$2,$3,$4,'inbound','customer','public','oi','received',$5)`, []any{tenant, uuid.New(), conversation, connector, time.Now().Add(-time.Minute)}},
			{`INSERT INTO contact_consents(tenant_id,id,contact_id,inbox_id,purpose,source,evidence,granted_at) VALUES($1,$2,$3,$4,'cobranca','painel','evento:simulacao',$5)`, []any{tenant, uuid.New(), contact, inbox, time.Now().Add(-time.Minute)}},
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement.q, statement.a...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return channelWorld{
		sector: sector, otherSector: otherSector, conversation: conversation, contact: contact,
		connector: connector, inbox: inbox, identity: identity,
	}
}

func loginERP(t *testing.T, ctx context.Context, svc *auth.Service, email, password string) issuedSession {
	t.Helper()
	session, err := svc.Login(ctx, email, password, "", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	return issuedSession{token: session.Token, csrf: session.CSRFToken}
}

func jsonText(payload map[string]any) string {
	raw, _ := json.Marshal(payload)
	return string(raw)
}
