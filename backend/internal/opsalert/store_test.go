package opsalert

import (
	"strings"
	"testing"
)

func TestScanQueriesOmitMessageMaterial(t *testing.T) {
	joined := strings.ToLower(jobScanSQL + "\n" + connectorScanSQL + "\n" + webhookScanSQL)
	for _, forbidden := range []string{
		"payload", "errors", "ciphertext", "credential", "remote_origin",
		"phone_number", "display_number", "cpf", "body",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("consulta lê %s", forbidden)
		}
	}
	if !strings.Contains(jobScanSQL, "args->>'tenant_id' = $1") {
		t.Fatal("job sem filtro de provedor")
	}
	if strings.Contains(jobScanSQL, "SELECT args") || strings.Contains(jobScanSQL, ", args") {
		t.Fatal("args do job não podem sair da consulta")
	}
	if !strings.Contains(webhookScanSQL, "state = 'pending'") {
		t.Fatal("webhook precisa da fila ainda sem conclusão")
	}
	if !strings.Contains(connectorScanSQL, "status = 'disconnected'") {
		t.Fatal("conector precisa do status desconectado")
	}
}
