package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// Proves the opt-in m4a fixture wires sessions, the simulation control plane
// and the canned-reply route. The full window, opt-out, attachment and hours
// journeys live in internal/acceptance and in the browser runner.
func TestM4AFixtureWiring(t *testing.T) {
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
	go func() { done <- runDemoMode(ctx, listeners[0], listeners[1], listeners[2], ready, true, true, false, false) }()
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
	if info.M4A == nil || info.M4A.ControlURL == "" || info.M4A.ControlToken == "" || info.M4A.Admin.Token == "" || info.M4A.DriverTemplateID != m4aDriverTemplate || info.M4A.Timezone != "America/Sao_Paulo" {
		t.Fatal("fixture M4A incompleta")
	}
	if info.M3B == nil || len(info.M3B.Actors) != 3 {
		t.Fatal("M4A depende dos atores M3B")
	}
	base := "http://" + listeners[0].Addr().String()
	client := &http.Client{Timeout: 2 * time.Second}
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
	denied, err := client.Post(info.M4A.ControlURL+"/simulation/expire-window", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatalf("controle sem token: %d", denied.StatusCode)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, info.M4A.ControlURL+"/simulation/expire-window", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Simulation-Token", info.M4A.ControlToken)
	expired, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(expired.Body)
	expired.Body.Close()
	if expired.StatusCode != 200 || expired.Header.Get("X-ChatISP-Simulation") == "" {
		t.Fatalf("expiração simulada: %d %s", expired.StatusCode, body)
	}
	var clock struct {
		LeaseReleased bool `json:"lease_released"`
		MetaCalls     int  `json:"meta_calls"`
	}
	if err = json.Unmarshal(body, &clock); err != nil || !clock.LeaseReleased || clock.MetaCalls != 0 {
		t.Fatalf("relógio da fixture: %s", body)
	}
	session, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/panel/canned-replies", nil)
	if err != nil {
		t.Fatal(err)
	}
	session.AddCookie(&http.Cookie{Name: "__Host-session", Value: info.M4A.Admin.Token})
	listed, err := client.Do(session)
	if err != nil {
		t.Fatal(err)
	}
	listed.Body.Close()
	if listed.StatusCode != 200 {
		t.Fatalf("catálogo de respostas: %d", listed.StatusCode)
	}
}
