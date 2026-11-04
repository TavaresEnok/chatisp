package metrics

import (
	"strings"
	"testing"
)

func TestOpsSnapshotClosesLabelsAndHidesFreeText(t *testing.T) {
	meter := metricsNew(t)
	secret := "111.111.111-11 https://user:pass@evil.example/segredo"
	meter.SetOps(OpsSnapshot{
		Uncertain:            2,
		UncertainOldestAge:   9,
		SendReservationsHeld: 1,
		StorageExpectedBytes: 40,
		WebhookPendingAge:    3,
		JobReservations: []CountSample{
			{Name: secret, Value: 4},
			{Name: "inbound", Value: 1},
		},
		WebhookItems: []CountSample{
			{Name: secret, Value: 5},
			{Name: "pending", Value: 2},
		},
		Connectors: []PairSample{
			{Left: secret, Right: secret, Value: 7},
			{Left: "meta_cloud", Right: "disconnected", Value: 1},
		},
	})
	body := gatherOps(t, meter)
	if strings.Contains(body, secret) || strings.Contains(body, "111.111") || strings.Contains(body, "evil.example") {
		t.Fatalf("texto livre entrou na série:\n%s", body)
	}
	if !strings.Contains(body, `queue="other"`) || !strings.Contains(body, `queue="inbound"`) {
		t.Fatalf("fila fechada ausente:\n%s", body)
	}
	if !strings.Contains(body, `type="other"`) || !strings.Contains(body, `type="meta_cloud"`) {
		t.Fatalf("tipo de conector ausente:\n%s", body)
	}
	if !strings.Contains(body, `status="disconnected"`) {
		t.Fatalf("estado do conector ausente:\n%s", body)
	}
	uncertain, ok := opsLine(body, "chatisp_outbound_uncertain")
	if !ok || uncertain != 2 {
		t.Fatalf("incertos = %v ok=%v", uncertain, ok)
	}
	storage, ok := opsLine(body, "chatisp_storage_expected_bytes")
	if !ok || storage != 40 {
		t.Fatalf("storage = %v ok=%v", storage, ok)
	}
}

func TestOpsFailureKeepsPriorSnapshot(t *testing.T) {
	meter := metricsNew(t)
	meter.SetOps(OpsSnapshot{Uncertain: 1, StorageExpectedBytes: 8})
	meter.SetOpsCollectError()
	body := gatherOps(t, meter)
	okValue, ok := opsLine(body, "chatisp_ops_collect_ok")
	errors, errorsOK := opsLine(body, "chatisp_ops_collect_errors_total")
	uncertain, uncertainOK := opsLine(body, "chatisp_outbound_uncertain")
	if !ok || okValue != 0 || !errorsOK || errors != 1 || !uncertainOK || uncertain != 1 {
		t.Fatalf("falha apagou a leitura anterior: ok=%v erros=%v incertos=%v\n%s", okValue, errors, uncertain, body)
	}
}

func TestOpsAbsentBeforeFirstRead(t *testing.T) {
	meter := metricsNew(t)
	meter.EnableOps()
	body := gatherOps(t, meter)
	if strings.Contains(body, "chatisp_ops_collect_ok") || strings.Contains(body, "chatisp_storage_expected_bytes") {
		t.Fatalf("leitura vazia virou zero:\n%s", body)
	}
}

func metricsNew(t *testing.T) *Metrics {
	t.Helper()
	return New()
}

func gatherOps(t *testing.T, meter *Metrics) string {
	t.Helper()
	body, err := meter.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func opsLine(text, name string, parts ...string) (float64, bool) {
	return lineValue(text, name, parts...)
}
