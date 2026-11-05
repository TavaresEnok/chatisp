package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chatisp/internal/platform/httpapi"
)

func TestMissingConfigAbortsWithoutSecret(t *testing.T) {
	t.Setenv("CHATISP_DATABASE_URL", "postgres://user:super-secret-value@127.0.0.1/chatisp")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"api"}, []string{
		"CHATISP_ENV=development",
		"CHATISP_SHUTDOWN_TIMEOUT=super-secret-value",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("esperava falha")
	}
	message := err.Error()
	if strings.Contains(message, "super-secret-value") || strings.Contains(message, "postgres://") {
		t.Fatalf("mensagem expôs segredo: %s", message)
	}
	if !strings.Contains(message, "CHATISP_SHUTDOWN_TIMEOUT") {
		t.Fatalf("mensagem: %s", message)
	}
	err = run(context.Background(), []string{"migrate"}, []string{"CHATISP_ENV=development"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "CHATISP_DATABASE_URL") {
		t.Fatalf("migrate deve exigir owner: %v", err)
	}
	err = run(context.Background(), []string{"api"}, []string{"CHATISP_ENV=development"}, &stdout, &stderr)
	if err == nil || strings.Contains(err.Error(), "super-secret-value") || !strings.Contains(err.Error(), "obrigatórias") {
		t.Fatalf("dependências ausentes devem falhar sem expor segredo: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr inesperado: %s", stderr.String())
	}
}

func TestOpenAPICommandIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "openapi.json")
	secondPath := filepath.Join(dir, "nested", "openapi.json")
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"openapi", "-output", firstPath}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"openapi", "-output", secondPath}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("arquivos OpenAPI divergiram")
	}
	want, err := httpapi.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, want) {
		t.Fatal("arquivo diferente do gerador")
	}
	if !bytes.Contains(first, []byte("\n")) {
		t.Fatal("JSON sem quebra de linha final")
	}
}

func TestUnknownMode(t *testing.T) {
	err := run(context.Background(), []string{"explode"}, nil, ioDiscard(), ioDiscard())
	if err == nil || !strings.Contains(err.Error(), "modo desconhecido") {
		t.Fatalf("erro: %v", err)
	}
}

func TestWorkerAndAllModeRequireDependencies(t *testing.T) {
	ctx := context.Background()
	baseEnv := []string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=postgres://user:pass@127.0.0.1/chatisp",
	}

	for _, mode := range []string{"worker", "all", "api"} {
		err := run(ctx, []string{mode}, baseEnv, ioDiscard(), ioDiscard())
		if err == nil {
			t.Fatalf("modo %s deveria ter falhado por dependências ausentes", mode)
		}
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func ioDiscard() *nopWriter { return &nopWriter{} }
