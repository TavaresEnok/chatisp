package metacloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"chatisp/internal/channel/whatsapp"
)

const (
	testVersion = "v23.0"
	testPhone   = "106540352242922"
	testToken   = "secret-token-value"
	testBody    = "corpo-secreto-da-mensagem"
	testTrace   = "TRACESECRET99"
)

func testRequest() whatsapp.TextRequest {
	return whatsapp.TextRequest{
		AttemptID:     "attempt-1",
		ConnectionID:  "connection-1",
		ConnectorType: whatsapp.ConnectorMetaCloud,
		RecipientID:   "16505551234",
		CallbackData:  "callback-opaco",
		Text:          testBody,
	}
}

func newTestPair(t *testing.T, mode string) (*Server, *Driver) {
	t.Helper()
	server, err := NewServer(testVersion, testPhone, testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	server.SetMode(mode)
	driver, err := Activate(Config{
		Mode:          ModeSimulation,
		GraphVersion:  testVersion,
		PhoneNumberID: testPhone,
		AccessToken:   testToken,
		BaseURL:       server.URL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, driver
}

func assertClean(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{testToken, testBody, testTrace, "callback-opaco", "Invalid OAuth"} {
		if strings.Contains(value, secret) {
			t.Fatalf("vazou %q em %q", secret, value)
		}
	}
}

func TestSendTextAcceptsWithRemoteIDOnly(t *testing.T) {
	_, driver := newTestPair(t, ServerAccept)
	var face whatsapp.TextDriver = driver
	if !driver.Simulation() || !face.Capabilities().Text || face.Capabilities().OfficialCustomerWindow || face.Capabilities().Pricing {
		t.Fatalf("capacidades: sim=%v %#v", driver.Simulation(), face.Capabilities())
	}
	res, err := face.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateAccepted || res.Remote != whatsapp.EvidenceNone || res.ProviderMessageID == "" || !res.Simulation {
		t.Fatalf("aceitação: %#v", res)
	}
	if whatsapp.LocalState(res.Remote).Valid() {
		t.Fatal("recibo virou estado local")
	}
	assertClean(t, res.Detail+" "+res.RejectionCode+" "+fmt.Sprintf("%#v", res))
}

func TestSendTextInvalidTokenIsSanitized(t *testing.T) {
	_, driver := newTestPair(t, ServerTokenInvalid)
	res, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateFailed || res.RejectionCode != RejectionTokenInvalid || res.ProviderMessageID != "" || res.Remote != whatsapp.EvidenceNone {
		t.Fatalf("token: %#v", res)
	}
	assertClean(t, res.Detail+" "+fmt.Sprintf("%#v", res))
}

func TestSendTextLostResponseAndTimeoutStayUncertain(t *testing.T) {
	server, driver := newTestPair(t, ServerLoseResponse)
	lost, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if lost.Local != whatsapp.StateUncertain || lost.ProviderMessageID != "" || lost.Remote != whatsapp.EvidenceNone {
		t.Fatalf("resposta perdida: %#v", lost)
	}
	got := server.AcceptedRequests()
	if len(got) != 1 || got[0].MessageID == "" || got[0].ResponseWritten || got[0].MessageID == lost.ProviderMessageID {
		t.Fatalf("aceite interno: %#v", got)
	}
	if strings.Contains(lost.Detail, got[0].MessageID) {
		t.Fatal("id interno vazou")
	}

	server.SetMode(ServerTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	timed, err := driver.SendText(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if timed.Local != whatsapp.StateUncertain || timed.ProviderMessageID != "" {
		t.Fatalf("timeout: %#v", timed)
	}
	if len(server.AcceptedRequests()) != 2 || server.AcceptedRequests()[1].ResponseWritten {
		t.Fatalf("timeout interno: %#v", server.AcceptedRequests())
	}
	assertClean(t, lost.Detail+" "+timed.Detail)
}

func TestSendTextStatusBeforeHTTPResponse(t *testing.T) {
	server, driver := newTestPair(t, ServerStatusBefore)
	var seen Item
	var during bool
	server.SetStatusHook(func(raw []byte) {
		during = true
		items, err := Parse("app-trusted", raw, time.Unix(1_800_000_000, 0).UTC())
		if err != nil || len(items) != 1 {
			t.Errorf("status antecipado: %v %#v", err, items)
			return
		}
		seen = items[0]
	})
	res, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !during || seen.Kind != ItemStatus || seen.Remote != whatsapp.EvidenceDelivered || seen.ExternalID == "" {
		t.Fatalf("status: during=%v %#v", during, seen)
	}
	if res.Local != whatsapp.StateAccepted || res.Remote != whatsapp.EvidenceNone || res.ProviderMessageID != seen.ExternalID {
		t.Fatalf("resposta HTTP: %#v", res)
	}
	if seen.CallbackData != "callback-opaco" || seen.AppID != "app-trusted" || seen.PhoneNumberID != testPhone {
		t.Fatalf("correlação: %#v", seen)
	}
	if seen.IngestedAt.Equal(seen.OriginAt) || !seen.OriginKnown {
		t.Fatalf("relógios: origem %v ingestão %v", seen.OriginAt, seen.IngestedAt)
	}
}

func TestSendTextIsolatedHTTPStatusDoesNotRetry(t *testing.T) {
	server, driver := newTestPair(t, ServerTooMany)
	limited, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	server.SetMode(ServerUnavailable)
	failed, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range []whatsapp.Result{limited, failed} {
		if res.Local != whatsapp.StateUncertain || res.RejectionCode == RejectionTokenInvalid || res.ProviderMessageID != "" {
			t.Fatalf("status isolado: %#v", res)
		}
		if whatsapp.HTTPStatusAllowsRetry(res.HTTPStatus) {
			t.Fatalf("repetição autorizada: %#v", res)
		}
	}
	if limited.HTTPStatus != 429 || failed.HTTPStatus != 500 {
		t.Fatalf("códigos: %#v %#v", limited, failed)
	}
}

func TestSendTextDoesNotFollowRedirect(t *testing.T) {
	server, driver := newTestPair(t, ServerRedirect)
	res, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateUncertain || res.ProviderMessageID != "" || server.StolenHits() != 0 {
		t.Fatalf("redirect: hits=%d %#v", server.StolenHits(), res)
	}
	assertClean(t, res.Detail)
}

func TestSendTextCancelAndRefusedBeforeDispatch(t *testing.T) {
	server, driver := newTestPair(t, ServerAccept)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := driver.SendText(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateCancelled || len(server.AcceptedRequests()) != 0 {
		t.Fatalf("cancelamento: %#v aceitos=%d", res, len(server.AcceptedRequests()))
	}
	server.Close()
	refused, err := driver.SendText(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if refused.Local != whatsapp.StateRetrying || refused.ProviderMessageID != "" {
		t.Fatalf("não despachado: %#v", refused)
	}
}

func TestActivateRejectsUnpinnedAndNonLocalEndpoints(t *testing.T) {
	doc := "https://developers.facebook.com/documentation/business-messaging/whatsapp/messages/text-messages"
	official, err := Activate(Config{
		Mode: ModeOfficial, GraphVersion: testVersion, PrimaryDocURL: doc,
		PhoneNumberID: testPhone, AccessToken: testToken,
	})
	if err != nil || official.Simulation() {
		t.Fatal(err, official)
	}
	cases := []Config{
		{Mode: ModeOfficial, PhoneNumberID: testPhone, AccessToken: testToken, PrimaryDocURL: doc},
		{Mode: ModeOfficial, GraphVersion: testVersion, PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeOfficial, GraphVersion: "v0.1", PrimaryDocURL: doc, PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeOfficial, GraphVersion: testVersion, PrimaryDocURL: "http://developers.facebook.com/docs/whatsapp", PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeOfficial, GraphVersion: testVersion, PrimaryDocURL: doc + "?token=" + testToken, PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeOfficial, GraphVersion: testVersion, PrimaryDocURL: doc, BaseURL: "http://graph.facebook.com", PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeOfficial, GraphVersion: testVersion, PrimaryDocURL: doc, BaseURL: "https://127.0.0.1", PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeSimulation, GraphVersion: testVersion, BaseURL: "https://graph.facebook.com", PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeSimulation, GraphVersion: testVersion, BaseURL: "http://169.254.169.254", PhoneNumberID: testPhone, AccessToken: testToken},
		{Mode: ModeSimulation, GraphVersion: "", BaseURL: "http://127.0.0.1:9", PhoneNumberID: testPhone, AccessToken: testToken},
	}
	for _, cfg := range cases {
		got, err := Activate(cfg)
		if err == nil || got != nil {
			t.Fatalf("ativou config inválida: %#v", cfg)
		}
		assertClean(t, err.Error())
	}
	req := testRequest()
	req.Text = strings.Repeat("á", MaxTextRunes+1)
	_, driver := newTestPair(t, ServerAccept)
	if _, err := driver.SendText(context.Background(), req); !errors.Is(err, errTextInvalid) {
		t.Fatalf("texto longo: %v", err)
	}
	if len(driver.endpoint.token) == 0 {
		t.Fatal("token não configurado")
	}
}
