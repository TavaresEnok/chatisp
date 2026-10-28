package httpapi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCommittedOpenAPIMatchesGenerator(t *testing.T) {
	root := moduleRoot(t)
	committed, err := os.ReadFile(filepath.Join(root, "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatal("backend/api/openapi.json divergiu; rode chatisp openapi a partir de backend/")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod não encontrado")
		}
		dir = parent
	}
}
