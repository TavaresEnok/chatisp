package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionRequiresMasterKeyWithoutEcho(t *testing.T) {
	_, err := Load([]string{
		"CHATISP_ENV=production",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
	})
	if err == nil || !strings.Contains(err.Error(), "CHATISP_DATA_KEYS") {
		t.Fatalf("produção sem chave: %v", err)
	}

	secret := "material-super-secreto-da-chave"
	_, err = Load([]string{
		"CHATISP_ENV=production",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
		"CHATISP_DATA_KEYS=v1:" + secret,
		"CHATISP_DATA_KEY_VERSION=v1",
	})
	if err == nil {
		t.Fatal("esperava chave inválida")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("erro expôs segredo: %s", err)
	}
	if !strings.Contains(err.Error(), "CHATISP_DATA_KEYS") {
		t.Fatalf("mensagem: %s", err)
	}
}

func TestLoadAcceptsKeyFile(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString(bytes32(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "keys")
	if err := os.WriteFile(path, []byte("v1:"+raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{
		"CHATISP_ENV=production",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
		"CHATISP_DATA_KEYS_FILE=" + path,
		"CHATISP_DATA_KEY_VERSION=v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeyRing == nil || cfg.KeyRing.Current() != "v1" {
		t.Fatalf("anel: %#v", cfg.KeyRing)
	}
}

func TestDocumentPepperIsNotConfiguration(t *testing.T) {
	secret := "pepper-em-claro-nao-pode-aparecer"
	_, err := Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
		"CHATISP_CONTACT_DOCUMENT_PEPPER=" + secret,
	})
	if err == nil || !strings.Contains(err.Error(), "CHATISP_CONTACT_DOCUMENT_PEPPER") || strings.Contains(err.Error(), secret) {
		t.Fatalf("pepper: %v", err)
	}
	_, err = Load([]string{
		"CHATISP_ENV=development",
		"CHATISP_DATABASE_URL=postgres://chatisp:local@127.0.0.1:5432/chatisp?sslmode=disable",
		"CHATISP_DOCUMENT_PEPPER=" + secret,
	})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "CHATISP_CONTACT_DOCUMENT_PEPPER") {
		t.Fatalf("alias: %v", err)
	}
}

func bytes32(t *testing.T) []byte {
	t.Helper()
	return []byte("0123456789abcdef0123456789abcdef")
}
