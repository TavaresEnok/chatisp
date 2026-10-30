package httpapi

import (
	"bytes"
	"encoding/json"
	"testing"

	"chatisp/internal/attendance/store"
	"chatisp/internal/realtime"
)

func TestRealtimeContractUsesWireTypes(t *testing.T) {
	first, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("OpenAPI não determinístico")
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ClientMessage", "ServerMessage", "RealtimeStoreHead", "RealtimeStoreMessage", "RealtimeStoreEvent"} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Fatalf("schema ausente: %s", name)
		}
	}
	wire, err := json.Marshal(realtime.ServerMessage{Type: "events", Events: []store.Event{{Seq: 42}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"Seq":42`)) || bytes.Contains(wire, []byte(`"sequence_number"`)) {
		t.Fatalf("shape inesperado: %s", wire)
	}
}
