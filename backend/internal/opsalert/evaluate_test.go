package opsalert

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDeriveAlertsOnlyAboveConfiguredThreshold(t *testing.T) {
	limit := 10 * time.Minute
	connectorID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	otherConnector := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	pendingID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	exactID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	doneID := uuid.MustParse("55555555-5555-5555-5555-555555555555")

	alerts := Derive(Limits{JobAge: limit, WebhookAge: limit}, Facts{
		WebhookQueue: true,
		Jobs: []JobFact{
			{ID: 1, Kind: "chatisp.inbound.apply.v1", Queue: "inbound", State: "available", Age: limit},
			{ID: 2, Kind: "chatisp.inbound.apply.v1", Queue: "inbound", State: "retryable", Age: limit + time.Second},
			{ID: 3, Kind: "chatisp.outbound.dispatch.v1", Queue: "outbound", State: "completed", Age: limit + time.Hour},
			{ID: 4, Kind: "chatisp.outbound.dispatch.v1", Queue: "outbound", State: "discarded", Age: limit + time.Hour},
			{ID: 5, Kind: "chatisp.ingress.expand.v1", Queue: "ingress", State: "scheduled", Age: limit + time.Minute},
		},
		Connectors: []ConnectorFact{
			{ID: connectorID, Type: "meta_cloud", Status: "disconnected"},
			{ID: otherConnector, Type: "meta_cloud", Status: "connected"},
			{ID: uuid.MustParse("66666666-6666-6666-6666-666666666666"), Type: "evolution", Status: "degraded"},
			{ID: uuid.MustParse("77777777-7777-7777-7777-777777777777"), Type: "meta_cloud", Status: "pending"},
		},
		Webhooks: []WebhookFact{
			{ID: exactID, Kind: "message", State: "pending", Age: limit},
			{ID: pendingID, Kind: "status", State: "pending", Age: limit + time.Second},
			{ID: doneID, Kind: "message", State: "processed", Age: limit + time.Hour},
			{ID: uuid.MustParse("88888888-8888-8888-8888-888888888888"), Kind: "other", State: "quarantined", Age: limit + time.Hour},
		},
	})

	if len(alerts) != 4 {
		t.Fatalf("alertas = %d, corpo %#v", len(alerts), alerts)
	}
	got := map[string]Alert{}
	for _, alert := range alerts {
		got[alert.SubjectKey] = alert
	}
	job, ok := got["job:2"]
	if !ok || job.Signal != SignalJobAge || job.AgeSeconds != int64((limit+time.Second)/time.Second) {
		t.Fatalf("job acima do limite ausente: %#v", got)
	}
	if _, ok := got["job:5"]; !ok {
		t.Fatal("job agendado acima do limite deveria alertar")
	}
	if _, ok := got["job:1"]; ok {
		t.Fatal("idade igual ao limite gerou alerta")
	}
	if _, ok := got["job:3"]; ok {
		t.Fatal("job concluído gerou alerta")
	}
	if _, ok := got["job:4"]; ok {
		t.Fatal("job descartado gerou alerta")
	}
	connector, ok := got["connector:"+connectorID.String()]
	if !ok || connector.Signal != SignalConnectorDisconnected || connector.Label != "Conector meta_cloud desconectado" {
		t.Fatalf("conector desconectado: %#v", connector)
	}
	if _, ok := got["connector:"+otherConnector.String()]; ok {
		t.Fatal("conector ligado gerou alerta")
	}
	webhook, ok := got["webhook:"+pendingID.String()]
	if !ok || webhook.Signal != SignalWebhookIncomplete {
		t.Fatalf("webhook pendente: %#v", webhook)
	}
	if _, ok := got["webhook:"+exactID.String()]; ok {
		t.Fatal("webhook na idade exata do limite gerou alerta")
	}
	if _, ok := got["webhook:"+doneID.String()]; ok {
		t.Fatal("webhook concluído gerou alerta")
	}
}

func TestDeriveSkipsUnconfiguredThresholdsAndMissingWebhookQueue(t *testing.T) {
	connectorID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	alerts := Derive(Limits{}, Facts{
		WebhookQueue: false,
		Jobs:         []JobFact{{ID: 9, Kind: "chatisp.inbound.apply.v1", Queue: "inbound", State: "available", Age: time.Hour}},
		Connectors:   []ConnectorFact{{ID: connectorID, Type: "meta_cloud", Status: "disconnected"}},
		Webhooks:     []WebhookFact{{ID: uuid.New(), Kind: "message", State: "pending", Age: time.Hour}},
	})
	if len(alerts) != 1 || alerts[0].Signal != SignalConnectorDisconnected {
		t.Fatalf("sem limite só o conector desconectado alerta: %#v", alerts)
	}
}

func TestMessageBodyDoesNotEnterAlert(t *testing.T) {
	secret := "Olá, preciso da segunda via do boleto"
	document := "390.533.447-05"
	poison := secret + " " + document
	connectorID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	webhookID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	alerts := Derive(Limits{JobAge: time.Minute, WebhookAge: time.Minute}, Facts{
		WebhookQueue: true,
		Jobs: []JobFact{{
			ID: 42, Kind: poison, Queue: poison, State: "available", Age: 2 * time.Minute, Body: poison,
		}},
		Connectors: []ConnectorFact{{
			ID: connectorID, Type: poison, Status: "disconnected", Body: poison,
		}},
		Webhooks: []WebhookFact{{
			ID: webhookID, Kind: poison, State: "pending", Age: 2 * time.Minute, Body: poison,
		}},
	})
	if len(alerts) != 3 {
		t.Fatalf("sinais válidos devem permanecer sem o texto: %#v", alerts)
	}
	raw, err := json.Marshal(alerts)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, hidden := range []string{secret, document, "boleto", "segunda via", "390", "payload", "errors"} {
		if strings.Contains(encoded, hidden) {
			t.Fatalf("alerta contém %q em %s", hidden, encoded)
		}
	}
	for _, alert := range alerts {
		if alert.Label == "" || strings.Contains(alert.Label, secret) {
			t.Fatalf("rótulo inseguro: %#v", alert)
		}
	}
	if !strings.Contains(encoded, "desconhecido") || !strings.Contains(encoded, "desconhecida") {
		t.Fatalf("tipo livre deveria cair no rótulo genérico: %s", encoded)
	}
	if !strings.Contains(encoded, "Webhook pendente sem conclusão") {
		t.Fatalf("webhook sem classe conhecida: %s", encoded)
	}
}
