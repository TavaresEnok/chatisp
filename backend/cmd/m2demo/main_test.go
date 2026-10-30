package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSimulationEndpoint(t *testing.T) {
	request := func(agent, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v23.0/123/messages", strings.NewReader(body))
		r.Header.Set("User-Agent", agent)
		w := httptest.NewRecorder()
		handler().ServeHTTP(w, r)
		return w
	}
	body := `{"messaging_product":"whatsapp","to":"123","type":"text","text":{"body":"oi"}}`
	if w := request("chatisp-metacloud-simulation", body); w.Code != 200 || !strings.Contains(w.Body.String(), "wamid.simulation.") {
		t.Fatalf("resposta: %d %s", w.Code, w.Body.String())
	}
	if w := request("chatisp-metacloud", body); w.Code != 404 {
		t.Fatalf("driver oficial aceito: %d", w.Code)
	}
	if w := request("chatisp-metacloud-simulation", `{}`); w.Code != 400 {
		t.Fatalf("corpo inválido aceito: %d", w.Code)
	}
}

func TestDemoHealthAndRealLogin(t *testing.T) {
	if _, ok := os.LookupEnv("CHATISP_TEST_DATABASE_URL"); ok {
		t.Fatal("teste exige banco novo")
	}
	listeners := make([]net.Listener, 3)
	for i := range listeners {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners[i] = l
		defer l.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan demoInfo, 1)
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, listeners[0], listeners[1], listeners[2], ready) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("encerramento: %v", err)
		}
	}()
	var info demoInfo
	select {
	case info = <-ready:
	case err := <-done:
		done <- err
		t.Fatalf("fixture: %v", err)
	case <-time.After(90 * time.Second):
		t.Fatal("fixture expirou")
	}
	base := "http://" + listeners[0].Addr().String()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := client.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("serviço não ficou pronto")
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]string{"email": info.Email, "password": info.Password})
	resp, err := client.Post(base+"/api/panel/session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) == 0 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, data)
	}
	api, _ := url.Parse(base)
	vite, _ := url.Parse("http://127.0.0.1:5173")
	proxy := httptest.NewServer(panelProxy(api, vite))
	defer proxy.Close()
	headers := http.Header{"Origin": {"http://127.0.0.1:5174"}}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "__Host-session" {
			headers.Set("Cookie", cookie.Name+"="+cookie.Value)
		}
	}
	ws, upgrade, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(proxy.URL, "http")+"/api/panel/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if upgrade != nil {
			t.Fatalf("upgrade: HTTP %d: %v", upgrade.StatusCode, err)
		}
		t.Fatalf("upgrade: %v", err)
	}
	ws.Close(websocket.StatusNormalClosure, "done")
}

func TestReceiveIsExplicitLocalAndSigned(t *testing.T) {
	const secret = "simulation-secret"
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		if r.URL.Path != "/api/webhooks/meta" || r.URL.Query().Get("app_id") != "sim-app" || r.Header.Get("X-Hub-Signature-256") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || !strings.Contains(string(body), "wamid.simulation.inbound.") {
			t.Errorf("webhook inválido: %s %s", r.URL, body)
		}
	}))
	defer server.Close()
	args := []string{"-api", server.URL, "-app", "sim-app", "-secret", secret, "-waba", "sim-waba", "-phone", "123"}
	if err := receive(args); err != nil || !seen {
		t.Fatalf("envio: %v, recebido=%v", err, seen)
	}
	args[1] = "http://example.com"
	if err := receive(args); err == nil {
		t.Fatal("aceitou API não local")
	}
}
