package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/testdb"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("CHATISP_DATABASE_URL")
	_ = os.Unsetenv(testdb.EnvDatabaseURL)
	os.Exit(m.Run())
}

func TestRunRefusesNonDisposableDestination(t *testing.T) {
	dump := t.TempDir()
	socket := "/home/enok/srv_projetos/workspace/chat-isp/.tmp/testdb/pg"
	admin := "postgresql://postgres@/postgres?sslmode=disable&host=" + url.QueryEscape(socket)
	var stdout, stderr bytes.Buffer
	err := run([]string{"-url", admin, "-dump", dump}, &stdout, &stderr)
	if err == nil {
		t.Fatal("destino no checkout real foi aceito")
	}
	if !strings.Contains(err.Error(), "checkout real") {
		t.Fatalf("recusa inesperada: %s", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("recusa imprimiu relatório: %s", stdout.String())
	}
	if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "postgresql://") {
		t.Fatalf("erro expôs URL: %s", err)
	}

	t.Setenv("CHATISP_DATABASE_URL", "postgresql://postgres@/postgres?host=/tmp/chatisp-app&sslmode=disable")
	stdout.Reset()
	err = run([]string{"-url", "postgresql://postgres@/postgres?sslmode=disable&host=" + url.QueryEscape(t.TempDir()), "-dump", dump}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "CHATISP_DATABASE_URL") {
		t.Fatalf("banco da aplicação foi aceito: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("recusa imprimiu relatório: %s", stdout.String())
	}
}

func TestRunPrintsRestoreReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("postgresql de teste: %s", err)
	}
	t.Cleanup(env.Close)

	dump := t.TempDir()
	var stdout, stderr bytes.Buffer
	err = run([]string{"-url", env.AdminURL, "-dump", dump}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ensaio: %s", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	var report drillReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("json: %s\n%s", err, stdout.String())
	}
	if !strings.HasPrefix(report.Banco, "chatisp_restore_drill_") || len(report.Banco) != len("chatisp_restore_drill_")+32 {
		t.Fatalf("banco: %s", report.Banco)
	}
	if !report.Anexos || !report.Chaves || !report.ExclusoesReaplicadas {
		t.Fatalf("relatório incompleto: %+v", report)
	}
	if report.DuracaoMS <= 0 {
		t.Fatalf("duração: %d", report.DuracaoMS)
	}
	if report.Medida != "rto" {
		t.Fatalf("medida: %s", report.Medida)
	}
}
