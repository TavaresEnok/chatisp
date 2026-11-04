package realtime

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/metrics"
)

const socketSecret = "cookie-secreto-painel"

func TestWebSocketGaugeTracksAcceptedSocketsOnly(t *testing.T) {
	meter := metrics.New()
	userID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	handler := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      scriptAuth{},
		PollInterval:       time.Hour,
		RevalidateInterval: time.Hour,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:            meter,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	refuseSocket(t, server.URL)
	if got := websocketGauge(t, meter); got != 0 {
		t.Fatalf("recusa contou conexão: %v", got)
	}

	first := dialSocket(t, server.URL)
	defer first.Close(websocket.StatusNormalClosure, "fim")
	waitWebsocket(t, meter, 1)
	second := dialSocket(t, server.URL)
	waitWebsocket(t, meter, 2)
	second.Close(websocket.StatusNormalClosure, "fim")
	waitWebsocket(t, meter, 1)
	first.Close(websocket.StatusNormalClosure, "fim")
	waitWebsocket(t, meter, 0)

	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, socketSecret) || strings.Contains(text, userID.String()) || strings.Contains(text, testTrustedOrigin) {
		t.Fatalf("gauge expôs identificador:\n%s", text)
	}
	_ = userID
}

func TestWebSocketGaugeFallsWhenTheSocketFailsOrTheContextEnds(t *testing.T) {
	meter := metrics.New()
	auth := &gateAuth{}
	handler := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      auth,
		PollInterval:       20 * time.Millisecond,
		RevalidateInterval: 20 * time.Millisecond,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:            meter,
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	conn := dialSocket(t, server.URL)
	defer conn.Close(websocket.StatusNormalClosure, "fim")
	waitWebsocket(t, meter, 1)
	auth.fail.Store(true)
	waitWebsocket(t, meter, 0)

	parent, stop := context.WithCancel(context.Background())
	cancelHandler := NewHandler(Options{
		TrustedOrigins:     []string{testTrustedOrigin},
		Authenticator:      scriptAuth{},
		PollInterval:       time.Hour,
		RevalidateInterval: time.Hour,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:            meter,
	})
	cancelServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelHandler.ServeHTTP(w, r.WithContext(parent))
	}))
	cancelServer.Start()
	defer cancelServer.Close()
	live := dialSocket(t, cancelServer.URL)
	defer live.Close(websocket.StatusNormalClosure, "fim")
	waitWebsocket(t, meter, 1)
	stop()
	waitWebsocket(t, meter, 0)
}

type scriptAuth struct{}

func (scriptAuth) AuthenticateSession(context.Context, string) (Principal, error) {
	return Principal{Role: "agent", UserID: uuid.MustParse("55555555-5555-4555-8555-555555555555")}, nil
}

func (scriptAuth) RevalidatePrincipal(context.Context, Principal, string) error { return nil }

type gateAuth struct {
	fail atomic.Bool
}

func (a *gateAuth) AuthenticateSession(context.Context, string) (Principal, error) {
	return Principal{Role: "agent"}, nil
}

func (a *gateAuth) RevalidatePrincipal(context.Context, Principal, string) error {
	if a.fail.Load() {
		return ErrForbidden
	}
	return nil
}

func refuseSocket(t *testing.T, rawURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := strings.Replace(rawURL, "http://", "ws://", 1)
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{auth.SessionCookieName + "=" + socketSecret}},
	})
	if err == nil {
		t.Fatal("origem ausente foi aceita")
	}
	if resp != nil {
		resp.Body.Close()
	}
	request, err := http.NewRequest(http.MethodPost, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", testTrustedOrigin)
	request.Header.Set("Cookie", auth.SessionCookieName+"="+socketSecret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("POST virou websocket")
	}
}

func dialSocket(t *testing.T, rawURL string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := strings.Replace(rawURL, "http://", "ws://", 1)
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{testTrustedOrigin},
			"Cookie": []string{auth.SessionCookieName + "=" + socketSecret},
		},
	})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			_, _, err := conn.Read(context.Background())
			if err != nil {
				return
			}
		}
	}()
	return conn
}

func waitWebsocket(t *testing.T, meter *metrics.Metrics, want float64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last float64
	var ok bool
	for time.Now().Before(deadline) {
		last, ok = websocketValue(t, meter)
		if ok && last == want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("conexões websocket = %v ok=%v, esperado %v", last, ok, want)
}

func websocketGauge(t *testing.T, meter *metrics.Metrics) float64 {
	t.Helper()
	value, _ := websocketValue(t, meter)
	return value
}

func websocketValue(t *testing.T, meter *metrics.Metrics) (float64, bool) {
	t.Helper()
	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "chatisp_websocket_connections ") && line != "chatisp_websocket_connections" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatal(err)
		}
		return value, true
	}
	return 0, false
}
