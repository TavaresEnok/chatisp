package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeployAndCIYAMLParseWithoutSecrets(t *testing.T) {
	root := repoRoot(t)
	files := []string{
		filepath.Join(root, "deploy", "compose.yaml"),
		filepath.Join(root, ".github", "workflows", "ci.yml"),
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var node yaml.Node
		if err := yaml.Unmarshal(raw, &node); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := rejectSecretScalars(&node); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		text := string(raw)
		for _, needle := range []string{"AKIA", "BEGIN PRIVATE", "ghp_", "xoxb-", "sk_live_"} {
			if strings.Contains(text, needle) {
				t.Fatalf("%s contém marcador de segredo %s", filepath.Base(path), needle)
			}
		}
	}

	example, err := os.ReadFile(filepath.Join(root, "deploy", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(example)
	if strings.Contains(text, "AKIA") || strings.Contains(text, "BEGIN PRIVATE") {
		t.Fatal("deploy/.env.example contém marcador de segredo")
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		if !secretKey(key) {
			continue
		}
		if value == "" || !strings.Contains(strings.ToLower(value), "troque") {
			t.Fatalf("placeholder ausente em %s", key)
		}
	}
}

func rejectSecretScalars(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode || node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if err := rejectSecretScalars(child); err != nil {
				return err
			}
		}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if value.Kind == yaml.ScalarNode && secretKey(key.Value) {
			if !strings.Contains(value.Value, "${") {
				return errLiteral(key.Value)
			}
		}
		if err := rejectSecretScalars(value); err != nil {
			return err
		}
	}
	return nil
}

func secretKey(key string) bool {
	name := strings.ToLower(key)
	return strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "token")
}

type literalSecretError string

func (e literalSecretError) Error() string {
	return "valor literal em campo sensível: " + string(e)
}

func errLiteral(key string) error {
	return literalSecretError(key)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "deploy")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "backend", "go.mod")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("raiz do repositório não encontrada")
		}
		dir = parent
	}
}

// Every variable the Compose file demands with ":?" must be present in deploy/.env.example,
// so a fresh copy of the example is enough for `docker compose config`.
func TestEnvExampleDefinesEveryRequiredComposeVariable(t *testing.T) {
	root := repoRoot(t)
	compose, err := os.ReadFile(filepath.Join(root, "deploy", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile(filepath.Join(root, "deploy", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, line := range strings.Split(string(example), "\n") {
		if key, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok && !strings.HasPrefix(key, "#") {
			defined[key] = true
		}
	}
	rest := string(compose)
	for {
		i := strings.Index(rest, "${")
		if i < 0 {
			break
		}
		rest = rest[i+2:]
		end := strings.Index(rest, "}")
		if end < 0 {
			break
		}
		expr := rest[:end]
		if name, _, ok := strings.Cut(expr, ":?"); ok && !defined[name] {
			t.Errorf("deploy/.env.example não define %s, exigida pelo compose.yaml", name)
		}
	}
}

// A bare ":443" site with "tls internal" has no name to issue for: Caddy answers the handshake
// with "tlsv1 alert internal error". Found by scripts/compose-validate.sh on 05/11/2026.
func TestCaddyfileNamesTheInternalCertificate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	var site string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "{") && trimmed != "{" && !strings.HasPrefix(trimmed, "handle") {
			site = strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
			break
		}
	}
	if site == "" || strings.HasPrefix(site, ":") {
		t.Fatalf("site do Caddy sem nome de host: %q", site)
	}
	for _, want := range []string{"localhost", "127.0.0.1"} {
		if !strings.Contains(site, want) {
			t.Fatalf("site do Caddy sem %s: %q", want, site)
		}
	}
	if !strings.Contains(string(raw), "default_sni {$CHATISP_SITE_HOST:localhost}") {
		t.Fatal("Caddyfile sem default_sni: conexão por IP sem SNI falharia no TLS")
	}
	// Prometheus metrics stay on the internal network: the public site answers
	// 404 for /metrics and never proxies to the metrics port.
	if strings.Contains(string(raw), "reverse_proxy chatisp-api:9090") || strings.Contains(string(raw), "reverse_proxy chatisp-worker:9090") || !strings.Contains(string(raw), "handle /metrics* {\n\t\trespond 404") {
		t.Fatal("Caddyfile expõe métricas na superfície pública")
	}
}

// A configuração de produção não pode ativar domínio real nem expor métricas, e os nomes que o
// navegador usa precisam vir das mesmas variáveis em todos os pontos (origem do painel, storage
// público e certificado), senão cookie Secure, CSRF, WebSocket e URL assinada divergem.
func TestProductionDeployFilesStayInactiveAndCoherent(t *testing.T) {
	root := repoRoot(t)
	caddy, err := os.ReadFile(filepath.Join(root, "deploy", "Caddyfile.production"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(caddy)
	for _, want := range []string{"{$CHATISP_SITE_HOST}", "{$CHATISP_STORAGE_HOST}", "{$CHATISP_ACME_EMAIL}", "handle /metrics* {\n\t\trespond 404", "Strict-Transport-Security"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Caddyfile.production sem %q", want)
		}
	}
	for _, bad := range []string{"local_certs", "tls internal", "reverse_proxy chatisp-api:9090", "reverse_proxy chatisp-worker:9090", "garage:3903"} {
		if strings.Contains(text, bad) {
			t.Fatalf("Caddyfile.production contém %q", bad)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "compose.production.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	if err := rejectSecretScalars(&node); err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, want := range []string{
		"CHATISP_PANEL_ORIGINS: https://${CHATISP_SITE_HOST:?",
		"CHATISP_ATTACHMENTS_S3_PUBLIC_ENDPOINT: https://${CHATISP_STORAGE_HOST}",
		"CHATISP_ATTACHMENTS_S3_ENDPOINT: https://${CHATISP_STORAGE_HOST:?",
		"CHATISP_ACME_EMAIL:?",
		"- ${CHATISP_STORAGE_HOST}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose.production.yaml sem %q", want)
		}
	}
	if !strings.Contains(compose, "http://127.0.0.1:8081/edge-health") || !strings.Contains(text, "http://:8081") || !strings.Contains(compose, "mkdir -p /trust && chmod 0755 /trust") {
		t.Fatal("produção mantém dependência circular da CA interna do laboratório")
	}
	// Nenhum domínio, e-mail ou IP real escrito nos arquivos de produção.
	for _, line := range strings.Split(compose+text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, bad := range []string{".com.br", ".com\n", "@gmail", "@exemplo"} {
			if strings.Contains(trimmed+"\n", bad) {
				t.Fatalf("domínio ou contato literal em arquivo de produção: %q", trimmed)
			}
		}
	}
	// O Compose de referência só publica o HTTPS do Caddy em loopback e nunca as métricas.
	base, err := os.ReadFile(filepath.Join(root, "deploy", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), ":9090\"") || strings.Contains(string(base), "- \"9090") {
		t.Fatal("compose.yaml publica a porta de métricas")
	}
	if !strings.Contains(string(base), `"${CHATISP_PUBLIC_BIND:-127.0.0.1}:${CHATISP_PUBLIC_PORT:-8443}:443"`) {
		t.Fatal("compose.yaml deixou de publicar o HTTPS só em loopback por padrão")
	}
}
