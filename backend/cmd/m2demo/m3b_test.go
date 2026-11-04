package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Checks the opt-in fixture's real auth/session boundary independently of the
// browser launcher. The ordinary M2/M3A fixture test remains unchanged.
func TestM3BFixtureSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	listeners := make([]net.Listener, 3)
	for i := range listeners {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners[i] = l
		defer l.Close()
	}
	ready, done := make(chan demoInfo, 1), make(chan error, 1)
	go func() { done <- runDemoMode(ctx, listeners[0], listeners[1], listeners[2], ready, true, false, false, false) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	var info demoInfo
	select {
	case info = <-ready:
	case err := <-done:
		done <- err
		t.Fatalf("fixture: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if info.M3B == nil || len(info.M3B.Actors) != 3 || info.M3B.Source == info.M3B.Destination {
		t.Fatal("missing M3B actors/sectors")
	}
	base := "http://" + listeners[0].Addr().String()
	client := &http.Client{Timeout: time.Second}
	for {
		resp, err := client.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	for _, a := range info.M3B.Actors {
		req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/panel/session", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: a.Token})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Role string `json:"role"`
			User string `json:"user_id"`
			MFA  bool   `json:"mfa_enrollment_required"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || body.Role != a.Role || body.User != a.User.String() || body.MFA {
			t.Fatalf("session role=%s status=%d body=%+v err=%v", a.Role, resp.StatusCode, body, err)
		}
	}
}

func TestM3BProxyNegotiatesHTTP2(t *testing.T) {
	cert, err := m3bCertificate()
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	t.Cleanup(backend.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &http.Server{Handler: panelProxy(target, target), ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	go func() { _ = proxy.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = proxy.Close() })
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get("https://" + ln.Addr().String() + "/src/main.tsx")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 {
		t.Fatalf("status=%d proto=%s", resp.StatusCode, resp.Proto)
	}
}
