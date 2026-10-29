package whatsapp

import (
	"strings"
	"testing"
)

func TestLocalStateExcludesRemoteEvidence(t *testing.T) {
	for _, state := range []LocalState{
		StateQueued, StateSending, StateAccepted, StateFailed,
		StateRetrying, StateUncertain, StateCancelled,
	} {
		if !state.Valid() {
			t.Fatalf("estado local %q", state)
		}
	}
	if LocalState(EvidenceDelivered).Valid() || LocalState(EvidenceRead).Valid() {
		t.Fatal("entrega e leitura não são estado local")
	}
	if !EvidenceDelivered.Valid() || !EvidenceRead.Valid() || !EvidenceNone.Valid() {
		t.Fatal("evidência remota")
	}
	if RemoteEvidence("accepted").Valid() {
		t.Fatal("aceitação não é evidência remota")
	}
}

func TestClassifyAcceptanceIsNotDelivery(t *testing.T) {
	got := Classify(Observation{AcceptedMessageID: "wamid-teste"})
	if got.Local != StateAccepted || got.Remote != EvidenceNone || got.ProviderMessageID != "wamid-teste" {
		t.Fatalf("aceitação: %#v", got)
	}
	withReceipt := Classify(Observation{
		AcceptedMessageID: "wamid-teste",
		Remote:            EvidenceDelivered,
	})
	if withReceipt.Local != StateAccepted || withReceipt.Remote != EvidenceDelivered {
		t.Fatalf("entrega separada: %#v", withReceipt)
	}
	read := Classify(Observation{AcceptedMessageID: "wamid-teste", Remote: EvidenceRead})
	if read.Local != StateAccepted || read.Remote != EvidenceRead {
		t.Fatalf("leitura separada: %#v", read)
	}
}

func TestClassifyAbsentResponseIsUncertain(t *testing.T) {
	cases := []Observation{
		{},
		{HTTPStatus: 200},
		{HTTPStatus: 400},
		{Remote: EvidenceDelivered},
		{ClaimedSafeRetry: true},
		{AcceptedMessageID: "wamid", RejectionCode: "rejected"},
		{AcceptedMessageID: "wamid", NotDispatched: true},
		{NotDispatched: true, RejectionCode: "rejected"},
		{CancelBeforeDispatch: true},
	}
	for _, obs := range cases {
		got := Classify(obs)
		if got.Local != StateUncertain || got.ProviderMessageID != "" || got.Local == StateAccepted {
			t.Fatalf("observação %#v -> %#v", obs, got)
		}
	}
}

func TestClassifyDefinitiveRejection(t *testing.T) {
	got := Classify(Observation{
		RejectionCode: "policy_rejected",
		HTTPStatus:    400,
		Detail:        "rejeição definitiva do conector",
	})
	if got.Local != StateFailed || got.RejectionCode != "policy_rejected" || got.ProviderMessageID != "" {
		t.Fatalf("rejeição: %#v", got)
	}
	if got.Remote != EvidenceNone {
		t.Fatalf("rejeição não é entrega: %#v", got)
	}
}

func TestClassifyCancelBeforeDispatch(t *testing.T) {
	got := Classify(Observation{NotDispatched: true, CancelBeforeDispatch: true})
	if got.Local != StateCancelled || got.ProviderMessageID != "" {
		t.Fatalf("cancelamento: %#v", got)
	}
	inflight := Classify(Observation{CancelBeforeDispatch: true, HTTPStatus: 0})
	if inflight.Local != StateUncertain {
		t.Fatalf("cancelamento tardio: %#v", inflight)
	}
}

func TestClassifyIsolatedHTTPStatusDoesNotRetry(t *testing.T) {
	for _, code := range []int{0, 200, 400, 408, 429, 500, 503, 599} {
		if HTTPStatusAllowsRetry(code) {
			t.Fatalf("status %d autorizou repetição", code)
		}
		for _, claim := range []bool{false, true} {
			got := Classify(Observation{HTTPStatus: code, ClaimedSafeRetry: claim})
			if got.Local != StateUncertain {
				t.Fatalf("status %d claim %v -> %#v", code, claim, got)
			}
		}
	}
	retry := Classify(Observation{NotDispatched: true, HTTPStatus: 429, ClaimedSafeRetry: true})
	if retry.Local != StateRetrying {
		t.Fatalf("não despacho deveria permitir repetição local: %#v", retry)
	}
}

func TestClassifyDoesNotReturnPreDispatchStates(t *testing.T) {
	got := Classify(Observation{NotDispatched: true})
	if got.Local == StateQueued || got.Local == StateSending || !got.Local.Valid() {
		t.Fatalf("classificação: %#v", got)
	}
}

func TestClassifyDropsUnsafeDetail(t *testing.T) {
	body := "corpo-secreto-da-mensagem"
	got := Classify(Observation{Detail: body + "\nlinha"})
	if got.Local != StateUncertain || strings.Contains(got.Detail, body) {
		t.Fatalf("detalhe: %#v", got)
	}
}

func TestValidateHidesBodyAndRejectsBadIDs(t *testing.T) {
	const body = "corpo-secreto-da-mensagem"
	const callback = "callback-secreto"
	req := TextRequest{
		AttemptID:     "attempt-1",
		ConnectionID:  "",
		ConnectorType: ConnectorMetaCloud,
		RecipientID:   "recipient-1",
		CallbackData:  callback,
		Text:          body,
	}
	err := Validate(req)
	if err == nil || strings.Contains(err.Error(), body) || strings.Contains(err.Error(), callback) {
		t.Fatalf("erro: %v", err)
	}
	req.ConnectionID = "connection-1"
	req.ConnectorType = ConnectorEvolutionWeb
	req.Text = "olá"
	if err := Validate(req); err != nil {
		t.Fatalf("evolution estrutural: %v", err)
	}
	req.ConnectorType = ConnectorMetaCloud
	if err := Validate(req); err != nil {
		t.Fatal(err)
	}
	req.Text = " "
	if err := Validate(req); err == nil {
		t.Fatal("texto vazio")
	}
}
