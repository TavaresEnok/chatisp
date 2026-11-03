package whatsapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleRequest(attempt string) TextRequest {
	return TextRequest{
		AttemptID:     AttemptID(attempt),
		ConnectionID:  "connection-1",
		ConnectorType: ConnectorMetaCloud,
		RecipientID:   "recipient-1",
		CallbackData:  "callback-1",
		Text:          "olá, preciso de suporte",
	}
}

func TestFakeIdentifiesSimulationAndCapabilities(t *testing.T) {
	fake := NewFake()
	if !fake.Simulation() || fake.Name() != FakeDriverName {
		t.Fatalf("identidade: %s %v", fake.Name(), fake.Simulation())
	}
	caps := fake.Capabilities()
	if !caps.Text || caps.Media || caps.Templates || caps.Interactive || caps.DeliveryReceipts || caps.ReadReceipts || caps.Pricing || caps.OfficialCustomerWindow {
		t.Fatalf("capacidades: %#v", caps)
	}
	var driver TextDriver = fake
	res, err := driver.SendText(context.Background(), sampleRequest("attempt-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Simulation || res.Local != StateAccepted || res.Remote != EvidenceNone || res.ProviderMessageID != "sim:attempt-1:1" {
		t.Fatalf("resultado: %#v", res)
	}
}

func TestFakeTemplateStaysLocal(t *testing.T) {
	fake := NewFake()
	res, err := fake.SendTemplate(context.Background(), TemplateRequest{
		AttemptID: "attempt-tpl", ConnectionID: "connection-1", ConnectorType: ConnectorMetaCloud,
		RecipientID: "recipient-1", CallbackData: "callback-1", TemplateID: "tpl-1", Language: "pt_BR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Simulation || res.Local != StateAccepted || res.ProviderMessageID != "sim:attempt-tpl:1" {
		t.Fatalf("template simulado: %#v", res)
	}
	got := fake.Captures()
	if len(got) != 1 || got[0].TemplateID != "tpl-1" || got[0].Language != "pt_BR" || !got[0].Result.Simulation {
		t.Fatalf("captura: %#v", got)
	}
	if fake.Capabilities().Templates {
		t.Fatal("simulação passou a anunciar template oficial")
	}
}

func TestFakeLoseResponseAfterAcceptance(t *testing.T) {
	fake := NewFake()
	fake.SetMode(ModeLoseResponse)
	res, err := fake.SendText(context.Background(), sampleRequest("attempt-lost"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != StateUncertain || res.ProviderMessageID != "" || res.Remote != EvidenceNone || !res.Simulation {
		t.Fatalf("resposta perdida promovida: %#v", res)
	}
	captures := fake.Captures()
	if len(captures) != 1 || !captures[0].Dispatched || !captures[0].ResponseLost {
		t.Fatalf("captura: %#v", captures)
	}
	if captures[0].InternalAcceptID == "" || captures[0].InternalAcceptID == res.ProviderMessageID {
		t.Fatalf("aceite interno: %#v", captures[0])
	}
	if res.Detail == captures[0].InternalAcceptID {
		t.Fatal("id interno vazou no resultado")
	}
}

func TestFakeDefinitiveRejection(t *testing.T) {
	fake := NewFake()
	fake.SetMode(ModeReject)
	res, err := fake.SendText(context.Background(), sampleRequest("attempt-reject"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != StateFailed || res.RejectionCode != "simulated_rejection" || res.ProviderMessageID != "" || res.Remote != EvidenceNone {
		t.Fatalf("rejeição: %#v", res)
	}
}

func TestFakeIsolatedStatusStaysUncertain(t *testing.T) {
	fake := NewFake()
	for _, mode := range []Mode{ModeHTTP429, ModeHTTP500} {
		fake.SetMode(mode)
		res, err := fake.SendText(context.Background(), sampleRequest(string(mode)))
		if err != nil {
			t.Fatal(err)
		}
		if res.Local != StateUncertain || res.Local == StateRetrying || res.ProviderMessageID != "" {
			t.Fatalf("%s: %#v", mode, res)
		}
	}
	fake.SetMode(ModeNotDispatched)
	res, err := fake.SendText(context.Background(), sampleRequest("local-retry"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != StateRetrying {
		t.Fatalf("não despacho: %#v", res)
	}
	last := fake.Captures()
	if last[len(last)-1].Dispatched {
		t.Fatal("modo sem despacho registrou despacho")
	}
}

func TestFakeCancelWhileBlockedDoesNotDispatch(t *testing.T) {
	fake := NewFake()
	fake.Block()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := fake.SendText(ctx, sampleRequest("attempt-cancel"))
		errCh <- err
		done <- res
	}()
	waitUntil(t, func() bool { return fake.Waiting() == 1 })
	cancel()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.Local != StateCancelled || !res.Simulation || res.ProviderMessageID != "" {
		t.Fatalf("cancelamento: %#v", res)
	}
	if len(fake.Captures()) != 0 {
		t.Fatalf("capturou envio cancelado: %#v", fake.Captures())
	}
}

func TestFakeUnblockReleasesAccept(t *testing.T) {
	fake := NewFake()
	fake.Block()
	done := make(chan Result, 1)
	go func() {
		res, err := fake.SendText(context.Background(), sampleRequest("attempt-release"))
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	waitUntil(t, func() bool { return fake.Waiting() == 1 })
	fake.Unblock()
	res := <-done
	if res.Local != StateAccepted || res.ProviderMessageID != "sim:attempt-release:1" {
		t.Fatalf("liberação: %#v", res)
	}
}

func TestFakeConcurrentCaptures(t *testing.T) {
	fake := NewFake()
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		attempt := "attempt-" + string(rune('a'+(i%26))) + "-" + itoa(i)
		go func(attempt string) {
			defer wg.Done()
			res, err := fake.SendText(context.Background(), sampleRequest(attempt))
			if err != nil {
				errCh <- err
				return
			}
			if !res.Simulation || res.Local != StateAccepted || res.Remote != EvidenceNone {
				errCh <- errors.New("resultado concorrente inesperado: " + string(res.Local))
			}
		}(attempt)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	seen := map[string]int{}
	for _, capture := range fake.Captures() {
		seen[string(capture.Request.AttemptID)]++
		if !capture.Dispatched || capture.ResponseLost || capture.InternalAcceptID == "" {
			t.Fatalf("captura: %#v", capture)
		}
	}
	if len(seen) != n {
		t.Fatalf("capturas %d", len(seen))
	}
}

func TestFakeCallbackIsNotIdempotent(t *testing.T) {
	fake := NewFake()
	first := sampleRequest("attempt-a")
	second := sampleRequest("attempt-b")
	second.CallbackData = first.CallbackData
	same := sampleRequest("attempt-a")
	for _, req := range []TextRequest{first, second, same} {
		if _, err := fake.SendText(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	captures := fake.Captures()
	if len(captures) != 3 {
		t.Fatalf("deduplicou correlação: %d", len(captures))
	}
	if captures[0].Result.ProviderMessageID == captures[1].Result.ProviderMessageID || captures[0].Result.ProviderMessageID == captures[2].Result.ProviderMessageID {
		t.Fatalf("ids: %#v %#v %#v", captures[0].Result, captures[1].Result, captures[2].Result)
	}
}

func TestFakeRejectsInvalidRequestWithoutCapture(t *testing.T) {
	fake := NewFake()
	req := sampleRequest("attempt-1")
	req.Text = "corpo-secreto-da-mensagem"
	req.ConnectionID = ""
	_, err := fake.SendText(context.Background(), req)
	if err == nil || strings.Contains(err.Error(), "corpo-secreto") {
		t.Fatalf("erro: %v", err)
	}
	req.ConnectionID = "connection-1"
	req.ConnectorType = ConnectorEvolutionWeb
	_, err = fake.SendText(context.Background(), req)
	if err == nil || err.Error() != "conector indisponível" || strings.Contains(err.Error(), "corpo-secreto") {
		t.Fatalf("evolution: %v", err)
	}
	if len(fake.Captures()) != 0 {
		t.Fatal("pedido inválido foi capturado")
	}
	if _, err := (*Fake)(nil).SendText(context.Background(), req); err == nil {
		t.Fatal("driver nulo")
	}
}

func TestFakeUnknownModeIsUncertain(t *testing.T) {
	fake := NewFake()
	fake.SetMode(Mode("live-meta"))
	res, err := fake.SendText(context.Background(), sampleRequest("attempt-unknown"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != StateUncertain || res.ProviderMessageID != "" || !res.Simulation {
		t.Fatalf("modo desconhecido: %#v", res)
	}
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tempo esgotado")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
