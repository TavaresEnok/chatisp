package conversation

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSendStatesThatDoNotStartCustomerWait(t *testing.T) {
	acceptedAt := at(5)
	human := func(state LocalSendState) OutboundMessage {
		return OutboundMessage{
			ID:         uuid.MustParse("77777777-7777-4777-8777-777777777777"),
			Origin:     OriginHuman,
			Visibility: VisibilityPublic,
			LocalState: state,
		}
	}
	cases := []struct {
		name string
		msg  OutboundMessage
	}{
		{"queued", human(SendQueued)},
		{"uncertain", human(SendUncertain)},
		{"failed", human(SendFailed)},
		{"sending", human(SendSending)},
		{"cancelled", human(SendCancelled)},
		{"retrying", human(SendRetrying)},
		{"note", OutboundMessage{Origin: OriginNote, Visibility: VisibilityInternal, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: acceptedAt}}},
		{"bot", OutboundMessage{Origin: OriginBot, Visibility: VisibilityPublic, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: acceptedAt}}},
		{"menu", OutboundMessage{Origin: OriginMenu, Visibility: VisibilityPublic, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: acceptedAt}}},
		{"reaction", OutboundMessage{Origin: OriginReaction, Visibility: VisibilityPublic, LocalState: SendAccepted, AcceptanceDocumented: true, Remote: RemoteFacts{AcceptedAt: acceptedAt}}},
		{"delivered only", OutboundMessage{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendSending, Remote: RemoteFacts{DeliveredAt: acceptedAt}}},
		{"read only", OutboundMessage{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendAccepted, Remote: RemoteFacts{ReadAt: acceptedAt}}},
		{"accepted without evidence", OutboundMessage{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendAccepted, Remote: RemoteFacts{AcceptedAt: acceptedAt}}},
	}
	for _, tc := range cases {
		if StartsCustomerWait(tc.msg, time.Time{}) {
			t.Fatalf("%s iniciou espera", tc.name)
		}
	}

	ok := human(SendAccepted)
	ok.AcceptanceDocumented = true
	ok.Remote.AcceptedAt = acceptedAt
	if !StartsCustomerWait(ok, time.Time{}) {
		t.Fatal("resposta humana aceita não iniciou espera")
	}
	if !StartsCustomerWait(ok, acceptedAt) {
		t.Fatal("mensagem do cliente no mesmo instante foi tratada como posterior")
	}
	if StartsCustomerWait(ok, acceptedAt.Add(time.Second)) {
		t.Fatal("mensagem posterior do cliente ainda iniciou espera")
	}
	if !StartsCustomerWait(ok, acceptedAt.Add(-time.Second)) {
		t.Fatal("mensagem anterior do cliente bloqueou a espera")
	}
}

func TestRemoteEvidenceDoesNotConfuseAcceptanceWithDelivery(t *testing.T) {
	sending := OutboundMessage{Origin: OriginHuman, Visibility: VisibilityPublic, LocalState: SendSending}
	delivered, err := ApplyRemoteEvidence(sending, RemoteEvidence{
		Kind: EvidenceDelivered, At: at(3), IngestedAt: at(9), Documented: true, ExternalID: "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivered.LocalState != SendSending || delivered.AcceptanceDocumented || delivered.Remote.Accepted() {
		t.Fatalf("entrega virou aceite: %+v", delivered)
	}
	if !delivered.Remote.Delivered() || delivered.Remote.Read() {
		t.Fatal("entrega não ficou só na evidência remota")
	}
	if StartsCustomerWait(delivered, time.Time{}) {
		t.Fatal("entrega iniciou espera")
	}

	accepted, err := ApplyRemoteEvidence(sending, RemoteEvidence{
		Kind: EvidenceAccepted, At: at(2), Documented: true, ExternalID: "a1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.LocalState != SendAccepted || !accepted.AcceptanceDocumented || !accepted.Remote.Accepted() {
		t.Fatalf("aceite: %+v", accepted)
	}
	if accepted.Remote.Delivered() || accepted.Remote.Read() {
		t.Fatal("aceite preencheu entrega ou leitura")
	}
	if !StartsCustomerWait(accepted, time.Time{}) {
		t.Fatal("aceite documentado não habilita espera")
	}
	if _, err := ApplyRemoteEvidence(sending, RemoteEvidence{Kind: EvidenceAccepted, At: at(2), ExternalID: "a1"}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatal("aceite sem evidência documentada foi aplicado")
	}

	read, err := ApplyRemoteEvidence(accepted, RemoteEvidence{Kind: EvidenceRead, At: at(4), ExternalID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if read.LocalState != SendAccepted || !read.Remote.AcceptedAt.Equal(at(2)) || !read.Remote.ReadAt.Equal(at(4)) || read.Remote.Delivered() {
		t.Fatalf("leitura misturou estados: %+v", read.Remote)
	}
}

func TestLateEvidenceDoesNotRewindClocksOrMutateInput(t *testing.T) {
	backing := make([]RemoteEvidence, 1, 2)
	backing[0] = RemoteEvidence{Kind: EvidenceRead, At: at(1), ExternalID: "keep"}
	backing = append(backing, RemoteEvidence{ExternalID: "sentinel"})
	m := OutboundMessage{
		LocalState: SendSending,
		Evidence:   backing[:1],
		Remote:     RemoteFacts{ReadAt: at(4), HighWaterAt: at(4), AcceptedAt: at(3)},
	}
	m.LocalState = SendAccepted
	m.AcceptanceDocumented = true

	if _, err := ApplyRemoteEvidence(m, RemoteEvidence{}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatal("evidência vazia")
	}
	if backing[1].ExternalID != "sentinel" || len(m.Evidence) != 1 || m.Evidence[0].ExternalID != "keep" {
		t.Fatal("erro de evidência escreveu na capacidade da entrada")
	}

	lateDelivered, err := ApplyRemoteEvidence(m, RemoteEvidence{
		Kind: EvidenceDelivered, At: at(1), IngestedAt: at(20), ExternalID: "late-d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lateDelivered.Remote.HighWaterAt.Equal(at(4)) || !lateDelivered.Remote.ReadAt.Equal(at(4)) || !lateDelivered.Remote.AcceptedAt.Equal(at(3)) {
		t.Fatalf("relógio retrocedeu: %+v", lateDelivered.Remote)
	}
	if !lateDelivered.Remote.DeliveredAt.Equal(at(1)) || lateDelivered.LocalState != SendAccepted {
		t.Fatal("entrega atrasada alterou o aceite local")
	}
	if !m.Remote.DeliveredAt.IsZero() || len(m.Evidence) != 1 {
		t.Fatal("entrada recebeu a entrega atrasada")
	}

	lateAccepted, err := ApplyRemoteEvidence(lateDelivered, RemoteEvidence{
		Kind: EvidenceAccepted, At: at(2), Documented: true, ExternalID: "late-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lateAccepted.Remote.AcceptedAt.Equal(at(3)) || !lateAccepted.Remote.HighWaterAt.Equal(at(4)) || lateAccepted.LocalState != SendAccepted {
		t.Fatalf("aceite atrasado: %+v", lateAccepted.Remote)
	}

	failed, err := ApplyRemoteEvidence(lateAccepted, RemoteEvidence{Kind: EvidenceFailed, At: at(1), ExternalID: "late-f"})
	if err != nil {
		t.Fatal(err)
	}
	if failed.LocalState != SendAccepted || failed.Remote.AcceptedAt.IsZero() {
		t.Fatal("falha atrasada desfez o aceite")
	}
}

func TestUncertainIsNotRedeliveredAndInFlightIsNotCancelled(t *testing.T) {
	sending := OutboundMessage{LocalState: SendSending, Origin: OriginBot, ControllerEpoch: 2}
	uncertain, err := RecoverCrashedAttempt(sending, false)
	if err != nil || uncertain.LocalState != SendUncertain {
		t.Fatal(err)
	}
	if _, err := TransitionSend(uncertain, SendSending); !errors.Is(err, ErrInvalidSendTransition) {
		t.Fatal("uncertain voltou a sending")
	}
	proven, err := RecoverCrashedAttempt(sending, true)
	if err != nil || proven.LocalState != SendQueued {
		t.Fatal(err)
	}

	reconciled, err := ApplyRemoteEvidence(uncertain, RemoteEvidence{
		Kind: EvidenceAccepted, At: at(6), Documented: true, ExternalID: "late-receipt",
	})
	if err != nil || reconciled.LocalState != SendAccepted || reconciled.Remote.Delivered() {
		t.Fatalf("recibo atrasado: %+v", reconciled)
	}
	stillUncertain, err := ApplyRemoteEvidence(uncertain, RemoteEvidence{Kind: EvidenceDelivered, At: at(6), ExternalID: "d"})
	if err != nil || stillUncertain.LocalState != SendUncertain || !stillUncertain.Remote.Delivered() || stillUncertain.Remote.Accepted() {
		t.Fatal("entrega reconciliou uncertain como aceite")
	}

	queued := OutboundMessage{Origin: OriginBot, LocalState: SendQueued, ControllerEpoch: 2}
	cancelled, ok := CancelUnsentAutomatic(queued, 2)
	if !ok || cancelled.LocalState != SendCancelled || queued.LocalState != SendQueued {
		t.Fatal("fila automática não cancelou só a cópia")
	}
	if _, ok := CancelUnsentAutomatic(sending, 2); ok || sending.LocalState != SendSending {
		t.Fatal("envio em voo foi cancelado")
	}
	human := OutboundMessage{Origin: OriginHuman, LocalState: SendQueued, ControllerEpoch: 2}
	if _, ok := CancelUnsentAutomatic(human, 2); ok {
		t.Fatal("mensagem humana entrou no cancelamento automático")
	}
	if _, ok := CancelUnsentAutomatic(queued, 1); ok {
		t.Fatal("epoch diferente cancelou a fila")
	}
}

func TestDuplicateEvidenceHasNoSecondEffect(t *testing.T) {
	m := OutboundMessage{LocalState: SendSending}
	ev := RemoteEvidence{Kind: EvidenceAccepted, At: at(2), Documented: true, ExternalID: "a1"}
	once, err := ApplyRemoteEvidence(m, ev)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := ApplyRemoteEvidence(once, ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(twice.Evidence) != 1 || twice.LocalState != SendAccepted {
		t.Fatalf("duplicata: %d %s", len(twice.Evidence), twice.LocalState)
	}
}
