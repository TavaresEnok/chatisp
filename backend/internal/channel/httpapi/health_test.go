package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"chatisp/internal/channel/configuration"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/crypto"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type stubQuality struct {
	mu     sync.Mutex
	result configuration.QualityResult
	err    error
	seen   []string
}

func (s *stubQuality) ReadQuality(_ context.Context, m configuration.Material) (configuration.QualityResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, m.PhoneNumberID)
	return s.result, s.err
}

func TestNumberHealthAndQualityRead(t *testing.T) {
	ring, err := crypto.Parse("v1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "v1")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.New(auth.Options{Store: testStore, Keys: ring})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := configuration.New(testStore, testPools.Ingress, ring)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	cfg := huma.DefaultConfig("test", "1")
	cfg.CreateHooks = nil
	Register(humachi.New(router, cfg), Dependencies{Auth: identity, Channels: channels, TrustedOrigins: []string{"https://panel.example"}})
	a := seed(t, ring, "admin", true)
	other := seed(t, ring, "admin", true)
	agent := seed(t, ring, "agent", true)
	const origin = "https://panel.example"
	request := func(method, path string, who actor, withOrigin bool, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"inbox_name":"Saúde","display_number":"+5511977770001","app_id":"app-health","app_secret":"secret-health-local","waba_id":"waba-health","phone_number_id":"5511977770001","access_token":"token-health-local"}`))
		req.Header.Set("Content-Type", "application/json")
		if who.token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-session", Value: who.token})
		}
		if withOrigin {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-CSRF-Token", who.csrf)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d, esperado %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "token-health-local") || strings.Contains(rec.Body.String(), "secret-health-local") {
			t.Fatalf("segredo na resposta: %s", rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	var created box
	if err := json.Unmarshal(request("POST", "/api/panel/channels/meta", a, true, 200), &created); err != nil {
		t.Fatal(err)
	}
	type healthBody struct {
		Numbers []numberHealth `json:"numbers"`
	}
	read := func(who actor) healthBody {
		t.Helper()
		var out healthBody
		if err := json.Unmarshal(request("GET", "/api/panel/channels/meta/health", who, false, 200), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	h := read(a)
	if len(h.Numbers) != 1 || h.Numbers[0].Limit != nil || h.Numbers[0].Quality != nil || h.Numbers[0].WindowDays != 7 || h.Numbers[0].FailedSends != 0 || h.Numbers[0].QuarantinedItems != 0 {
		t.Fatalf("saúde inicial: %+v", h)
	}
	if got := read(other); len(got.Numbers) != 0 {
		t.Fatalf("outro tenant viu o número: %+v", got)
	}
	request("GET", "/api/panel/channels/meta/health", agent, false, 403)
	request("GET", "/api/panel/channels/meta/health", actor{}, false, 401)

	qpath := "/api/panel/channels/meta/" + created.ConnectorID.String() + "/quality"
	// No reader configured: the process does not read Graph.
	request("POST", qpath, a, true, 503)

	stub := &stubQuality{result: configuration.QualityResult{Rating: "GREEN", Simulation: true}}
	channels.SetQualityReader(stub)
	request("POST", qpath, a, false, 403)
	request("POST", qpath, agent, true, 403)
	request("POST", qpath, other, true, 404)
	request("POST", "/api/panel/channels/meta/nao-uuid/quality", a, true, 422)
	var q qualityReport
	if err := json.Unmarshal(request("POST", qpath, a, true, 200), &q); err != nil || q.Rating != "GREEN" || !q.Simulation || q.ReadAt == "" {
		t.Fatalf("leitura: %+v %v", q, err)
	}
	if len(stub.seen) != 1 || stub.seen[0] != "5511977770001" {
		t.Fatalf("leitor recebeu: %v", stub.seen)
	}

	// Limit reported by the webhook, an inbound item and a quarantined one.
	conn := adminConn(t)
	defer conn.Close(context.Background())
	ctx := context.Background()
	var phoneRow, inbox uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT p.id, p.inbox_id FROM meta_phone_numbers p WHERE p.tenant_id=$1 AND p.phone_number_id='5511977770001'`, a.tenant).Scan(&phoneRow, &inbox); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO meta_number_health (tenant_id,id,phone_id,kind,dedup_key,event,current_limit,old_limit,max_daily) VALUES ($1,$2,$3,'limit','number_limit:x','THROUGHPUT_UPGRADE','TIER_2K','TIER_250',2000)`, a.tenant, uuid.New(), phoneRow); err != nil {
		t.Fatal(err)
	}
	delivery := uuid.New()
	sum := sha256.Sum256(delivery[:])
	if _, err := conn.Exec(ctx, `INSERT INTO ingress_deliveries (id,source,app_id,body_sha256,body,state) VALUES ($1,'meta_cloud','app-health',$2,'{}','routed')`, delivery, sum[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO webhook_items (tenant_id,id,delivery_id,inbox_id,connector_id,phone_number_id,external_key,kind,state) VALUES ($1,$2,$3,$4,$5,'5511977770001','k-msg','message','processed'),($1,$6,$3,$4,$5,'5511977770001','k-q','other','quarantined')`, a.tenant, uuid.New(), delivery, inbox, created.ConnectorID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	h = read(a)
	n := h.Numbers[0]
	if n.Limit == nil || n.Limit.CurrentLimit != "TIER_2K" || n.Limit.OldLimit != "TIER_250" || n.Limit.Event != "THROUGHPUT_UPGRADE" || n.Limit.MaxDaily == nil || *n.Limit.MaxDaily != 2000 {
		t.Fatalf("limite: %+v", n.Limit)
	}
	if n.Quality == nil || n.Quality.Rating != "GREEN" || !n.Quality.Simulation {
		t.Fatalf("qualidade: %+v", n.Quality)
	}
	if n.QuarantinedItems != 1 || n.LastInboundAt == "" || n.OpenCredentialAlert {
		t.Fatalf("contadores: %+v", n)
	}

	// Refused read records nothing and does not degrade.
	stub.result, stub.err = configuration.QualityResult{}, configuration.ErrQualityRefused
	request("POST", qpath, a, true, 409)
	// A 190 degrades the channel like a send would, and opens the alert.
	stub.err = configuration.ErrQualityToken
	request("POST", qpath, a, true, 409)
	h = read(a)
	if h.Numbers[0].Status != "degraded" || !h.Numbers[0].OpenCredentialAlert || h.Numbers[0].Quality.Rating != "GREEN" {
		t.Fatalf("degradação: %+v", h.Numbers[0])
	}
	// Degraded: no token is opened for a new read.
	stub.err = nil
	stub.result = configuration.QualityResult{Rating: "RED"}
	request("POST", qpath, a, true, 409)
	if len(stub.seen) != 3 {
		t.Fatalf("leitura com canal degradado: %v", stub.seen)
	}
}
