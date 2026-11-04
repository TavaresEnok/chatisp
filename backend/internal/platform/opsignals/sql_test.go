package opsignals

import (
	"strings"
	"testing"
)

func TestScanSQLOmitsMessageMaterial(t *testing.T) {
	joined := strings.ToLower(scalarSQL + "\n" + jobSQL + "\n" + webhookSQL + "\n" + connectorSQL)
	for _, forbidden := range []string{
		"payload", "error_message", "error_code", "driver_ref", "phone_number",
		"credential", "remote_origin", "ciphertext", "body", "cpf", "args",
		"display_number", "object_key",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("consulta lê %s", forbidden)
		}
	}
	if !strings.Contains(scalarSQL, "state = 'uncertain'") {
		t.Fatal("envio incerto sem filtro de estado")
	}
	if !strings.Contains(scalarSQL, "state = 'validated'") {
		t.Fatal("storage sem filtro de anexo validado")
	}
	if !strings.Contains(webhookSQL, "GROUP BY state") {
		t.Fatal("webhook sem agregação por estado")
	}
}
