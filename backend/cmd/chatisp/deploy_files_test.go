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
		filepath.Join(root, "deploy", "compose.yml"),
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
