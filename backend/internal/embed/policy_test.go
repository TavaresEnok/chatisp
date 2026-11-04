package embed

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"chatisp/internal/platform/urlguard"
	"github.com/google/uuid"
)

func policy(t *testing.T) *urlguard.Policy {
	t.Helper()
	p, err := urlguard.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOrigemErradaRecusada(t *testing.T) {
	_, _, err := Check(policy(t), "https://erp.example", "https://outro.example/painel")
	if !errors.Is(err, ErrOrigin) {
		t.Fatalf("origem divergente: %v", err)
	}
	_, _, err = Check(policy(t), "https://erp.example", "https://erp.example.evil/painel")
	if !errors.Is(err, ErrOrigin) {
		t.Fatalf("sufixo de host: %v", err)
	}
	_, _, err = Check(policy(t), "https://erp.example/outro", "https://erp.example/painel")
	if !errors.Is(err, ErrOrigin) {
		t.Fatalf("origem com caminho: %v", err)
	}
	origin, panel, err := Check(policy(t), "https://erp.example", "https://erp.example/painel")
	if err != nil || origin != "https://erp.example" || panel != "https://erp.example/painel" {
		t.Fatalf("par válido: %s %s %v", origin, panel, err)
	}
	view := Decide(policy(t), "https://erp.example", "https://outro.example/painel", uuid.New(), "ATD-1", uuid.New(), uuid.New())
	if view.Embed || view.URL != "" || view.Context != nil {
		t.Fatalf("página divergente foi embutida: %+v", view)
	}
}

func TestURLInternaRecusada(t *testing.T) {
	origin := "https://erp.example"
	internal := []string{
		"http://127.0.0.1/admin",
		"http://127.0.0.1:80/admin",
		"http://169.254.169.254/latest/meta-data",
		"http://10.1.2.3/painel",
		"http://192.168.0.8/painel",
		"http://172.16.0.4/painel",
		"http://localhost/painel",
		"http://[::1]/painel",
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://erp.example/painel?cpf=00000000000",
		"https://user:secret@erp.example/painel",
		"file:///etc/passwd",
	}
	for _, raw := range internal {
		_, _, err := Check(policy(t), origin, raw)
		if !errors.Is(err, ErrURL) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	if _, _, err := Check(policy(t), "http://127.0.0.1", "http://127.0.0.1/painel"); !errors.Is(err, ErrOrigin) && !errors.Is(err, ErrURL) {
		t.Fatalf("origem interna: %v", err)
	}
}

func TestPayloadSemCorpo(t *testing.T) {
	conversation, sector, inbox := uuid.New(), uuid.New(), uuid.New()
	decoy := "CORPO-SECRETO-CPF-999-TOKEN"
	ctx, err := ContextFrom(conversation, sector, inbox, "ATD-2026-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(decoy)) || bytes.Contains(bytes.ToLower(raw), []byte("cpf")) || bytes.Contains(raw, []byte("body")) || bytes.Contains(raw, []byte("secret")) || bytes.Contains(raw, []byte("token")) {
		t.Fatalf("payload vazou dados: %s", raw)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"type": true, "v": true, "conversation_id": true, "protocol": true, "sector_id": true, "inbox_id": true}
	if len(keys) != len(allowed) {
		t.Fatalf("chaves: %s", raw)
	}
	for key := range keys {
		if !allowed[key] {
			t.Fatalf("campo extra %s", key)
		}
	}
	var back Context
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back != ctx || back.Protocol != "ATD-2026-1" {
		t.Fatalf("contexto: %+v", back)
	}
	view := Decide(policy(t), "https://erp.example", "https://erp.example/painel", conversation, "ATD-2026-1", sector, inbox)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Embed || bytes.Contains(encoded, []byte(decoy)) || bytes.Contains(bytes.ToLower(encoded), []byte("cpf")) || bytes.Contains(encoded, []byte(`"body"`)) {
		t.Fatalf("visão: %s", encoded)
	}
}
