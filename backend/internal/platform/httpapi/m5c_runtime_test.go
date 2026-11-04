package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func TestM5CRoutesComposed(t *testing.T) {
	api := New(Dependencies{})
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/panel/inboxes/{inbox_id}/triage-menu", "/api/panel/conversations/{conversation_id}/snooze", "/api/panel/lifecycle/inactivity", "/api/panel/presence/occupancy"} {
		if !strings.Contains(string(raw), path) {
			t.Fatalf("missing %s", path)
		}
		concrete := strings.ReplaceAll(strings.ReplaceAll(path, "{inbox_id}", uuid.NewString()), "{conversation_id}", uuid.NewString())
		r := panelCall(api.Handler, http.MethodGet, concrete, "198.51.100.49", nil, "", "", "", "")
		if r.Code != 503 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
	}
}
