package evolution

import (
	"strings"
	"testing"
)

func TestFlagStaysOffAndSendDoesNotRecord(t *testing.T) {
	if Enabled() {
		t.Fatal("flag ligada")
	}
	fake := NewFake()
	const body = "corpo-secreto-da-mensagem"
	if err := fake.Send("caixa-web"); err != ErrDisabled {
		t.Fatalf("envio: %v", err)
	}
	if err := fake.Connect("caixa-web"); err != ErrDisabled {
		t.Fatalf("conexão: %v", err)
	}
	if fake.Sends() != 0 || fake.Up("caixa-web") {
		t.Fatal("flag desligada ainda enviou ou conectou")
	}
	if strings.Contains(ErrDisabled.Error(), body) {
		t.Fatal("erro com corpo")
	}
}

func TestDisconnectIsolatesOneConnection(t *testing.T) {
	fake := NewFake()
	fake.MarkUp("caixa-web")
	fake.MarkUp("caixa-oficial")
	fake.Disconnect("caixa-web")
	if fake.Up("caixa-web") {
		t.Fatal("a caixa web continuou de pé")
	}
	if !fake.Up("caixa-oficial") {
		t.Fatal("a queda da web derrubou a outra caixa")
	}
}

func TestZeroValueFakeIsolatesConnections(t *testing.T) {
	var fake Fake
	fake.Disconnect("ausente")
	fake.MarkUp("caixa-1")
	fake.MarkUp("caixa-2")
	fake.Disconnect("caixa-1")
	if fake.Up("caixa-1") || !fake.Up("caixa-2") {
		t.Fatal("queda não isolada no fake de valor zero")
	}
}

func TestEchoAndHistoryDoNotAutomate(t *testing.T) {
	const body = "texto-que-nao-volta"
	cases := []Event{
		{Class: ClassEcho, ConnectionID: "caixa-web", ExternalID: "eco-1", Text: body},
		{Class: ClassHistory, ConnectionID: "caixa-web", ExternalID: "hist-1", Text: body},
		{Class: ClassEdit, ConnectionID: "caixa-web", ExternalID: "ed-1", Text: body},
		{Class: ClassRevoke, ConnectionID: "caixa-web", ExternalID: "rv-1", Text: body},
	}
	for _, ev := range cases {
		out, err := Classify(ev)
		if err != nil || out.Automation || strings.Contains(out.Author, body) {
			t.Fatalf("%s: %+v %v", ev.Class, out, err)
		}
	}
	echo, err := Classify(Event{Class: ClassEcho, ConnectionID: "caixa-web", ExternalID: "eco-2"})
	if err != nil || !echo.Duplicate || echo.Automation {
		t.Fatalf("eco: %+v %v", echo, err)
	}
	inbound, err := Classify(Event{Class: ClassInbound, ConnectionID: "caixa-web", ExternalID: "in-1"})
	if err != nil || !inbound.Accept || !inbound.Automation {
		t.Fatalf("entrada: %+v %v", inbound, err)
	}
	external, err := Classify(Event{Class: ClassExternal, ConnectionID: "caixa-web", ExternalID: "dev-1", Text: body})
	if err != nil || external.Author != "dispositivo externo" || external.Automation || !external.ReviewHuman || strings.Contains(external.Author, body) {
		t.Fatalf("aparelho: %+v %v", external, err)
	}
	if _, err := Classify(Event{Class: ClassUnsupported, ConnectionID: "caixa-web"}); err != nil {
		t.Fatal(err)
	}
	ignored, err := Classify(Event{Class: ClassUnsupported, ConnectionID: "caixa-web"})
	if err != nil || ignored.Accept || ignored.Automation {
		t.Fatalf("não suportado: %+v %v", ignored, err)
	}
	if _, err := Classify(Event{Class: "grupo", ConnectionID: "caixa-web"}); err != ErrInvalid {
		t.Fatalf("grupo: %v", err)
	}
}

func TestQRIsNotPersistedAndNotShownWhileDisabled(t *testing.T) {
	fake := NewFake()
	if _, err := IssueQR("agent"); err != ErrForbidden {
		t.Fatalf("atendente: %v", err)
	}
	token, err := IssueQR("admin")
	if err != ErrDisabled || token != "" {
		t.Fatalf("admin: %q %v", token, err)
	}
	if err := fake.StoreQR("qr-secreto"); err != ErrNotPersisted || fake.StoredQR() != "" {
		t.Fatalf("persistiu: %q %v", fake.StoredQR(), err)
	}
}
