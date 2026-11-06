package channelpolicy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type catalogRoundTrip func(*http.Request) (*http.Response, error)

func (f catalogRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func catalogForServer(t *testing.T, handler http.HandlerFunc) *MetaCatalog {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	local, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	client := &http.Client{Transport: catalogRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "graph.facebook.com" {
			t.Fatalf("unexpected target %s", r.URL)
		}
		cloned := r.Clone(r.Context())
		u := *r.URL
		u.Host = local.Host
		cloned.URL = &u
		return transport.RoundTrip(cloned)
	})}
	d, err := NewMetaCatalog(MetaCatalogConfig{GraphVersion: "v99.0", PrimaryDocURL: "https://developers.facebook.com/docs/whatsapp/business-management-api/message-templates", AccessToken: "contract-token-private", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const catalogItem = `{"id":"12","name":"hello","language":"pt_BR","category":"UTILITY","status":"APPROVED","components":[]}`

func TestMetaCatalogCompletePaginatedContract(t *testing.T) {
	calls := 0
	d := catalogForServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v99.0/123/message_templates" || r.Header.Get("Authorization") != "Bearer contract-token-private" || r.URL.Query().Get("access_token") != "" {
			t.Errorf("request contract failed: %s", r.URL)
		}
		if r.URL.Query().Get("fields") != "id,name,language,category,status,components" {
			t.Error("missing fields")
		}
		if calls == 1 {
			fmt.Fprintf(w, `{"data":[%s],"paging":{"next":"https://graph.facebook.com/v99.0/123/message_templates?after=opaque%%2Bcursor&access_token=do-not-follow","cursors":{"after":"opaque+cursor"}}}`, catalogItem)
			return
		}
		if r.URL.Query().Get("after") != "opaque+cursor" {
			t.Error("cursor not preserved")
		}
		fmt.Fprint(w, `{"data":[]}`)
	})
	got, err := d.Templates(context.Background(), "123")
	if err != nil || len(got) != 1 || got[0].State != "APPROVED" || calls != 2 || d.IsFake() {
		t.Fatalf("snapshot=%v err=%v calls=%d", got, err, calls)
	}
	if strings.Contains(fmt.Sprintf("%v %#v", d, d), "contract-token-private") {
		t.Fatal("secret in diagnostic")
	}
}
func TestMetaCatalogFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"absent data", `{}`, 200}, {"null data", `{"data":null}`, 200},
		{"invalid components", `{"data":[{"id":"1","name":"x","language":"pt","category":"UTILITY","status":"APPROVED","components":null}]}`, 200},
		{"external paging", `{"data":[],"paging":{"next":"https://evil.example/?after=x","cursors":{"after":"x"}}}`, 200},
		{"missing cursor", `{"data":[],"paging":{"next":"https://graph.facebook.com/v99.0/123/message_templates"}}`, 200},
		{"truncated", `{"data":[`, 200}, {"token rejected", `{"error":{"code":190,"message":"contract-token-private"}}`, 401},
		{"rate limit", `{"error":{"code":4}}`, 429}, {"redirect", `{"data":[]}`, 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := catalogForServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://evil.example/")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			got, err := d.Templates(context.Background(), "123")
			if err == nil || got != nil || strings.Contains(err.Error(), "contract-token-private") {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}
func TestMetaCatalogNoPartialSnapshotOrCursorLoop(t *testing.T) {
	calls := 0
	d := catalogForServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprintf(w, `{"data":[%s],"paging":{"next":"https://graph.facebook.com/v99.0/123/message_templates?after=x","cursors":{"after":"x"}}}`, catalogItem)
			return
		}
		fmt.Fprint(w, `{"data":[],"paging":{"next":"https://graph.facebook.com/v99.0/123/message_templates?after=x","cursors":{"after":"x"}}}`)
	})
	if got, err := d.Templates(context.Background(), "123"); err == nil || got != nil || calls != 2 {
		t.Fatalf("got=%v err=%v calls=%d", got, err, calls)
	}
}
func TestMetaCatalogExplicitConfigurationAndCredentialFailure(t *testing.T) {
	for _, cfg := range []MetaCatalogConfig{{}, {GraphVersion: "v99.0", AccessToken: "x"}, {PrimaryDocURL: "https://developers.facebook.com/docs/test", AccessToken: "x"}} {
		if _, err := NewMetaCatalog(cfg); err == nil {
			t.Fatal("implicit Graph configuration accepted")
		}
	}
	d := catalogForServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"error":{"code":190}}`) })
	invalid := false
	d.credentialInvalid = func(context.Context) error { invalid = true; return nil }
	if _, err := d.Templates(context.Background(), "123"); err != ErrCatalogTokenInvalid || !invalid {
		t.Fatalf("error=%v invalid=%v", err, invalid)
	}
	if _, err := d.Templates(context.Background(), "../456"); err == nil {
		t.Fatal("path injection accepted")
	}
}
