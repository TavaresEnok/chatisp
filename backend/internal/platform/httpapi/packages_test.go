package httpapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chatisp/internal/platform/mail"
)

func TestPackagesAuthorizationAndDownload(t *testing.T) {
	dir := t.TempDir()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.Create("leia-me.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("documentação")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b.Bytes())
	p := packageMetadata{ID: "inventario-funcional", Title: "Inventário", Description: "Documentação", Filename: packageFiles["inventario-funcional"], Size: int64(b.Len()), SHA256: hex.EncodeToString(sum[:])}
	manifest, err := json.Marshal(struct {
		Packages []packageMetadata `json:"packages"`
	}{[]packageMetadata{p}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, p.Filename), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	svc := panelService(t, &mail.Fake{})
	api := New(Dependencies{Auth: svc, PackageDir: dir})
	password := "senha-correta-10"
	admin := seedPanelUser(t, "admin", password)
	agent := seedPanelUser(t, "agent", password)
	adminCookie, _, _ := panelLogin(t, nil, admin.email, password)
	agentCookie, _, _ := panelLogin(t, nil, agent.email, password)
	for _, path := range []string{"/api/panel/packages", "/api/panel/packages/" + p.ID + "/download"} {
		for _, tc := range []struct {
			cookie string
			status int
		}{{"", 401}, {agentCookie, 403}} {
			got := panelCall(api.Handler, http.MethodGet, path, "198.51.100.10", nil, tc.cookie, "", "", "")
			if got.Code != tc.status {
				t.Fatalf("%s: got %d want %d: %s", path, got.Code, tc.status, got.Body.String())
			}
		}
	}
	got := panelCall(api.Handler, http.MethodGet, "/api/panel/packages", "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 200 || !strings.Contains(got.Body.String(), p.SHA256) || strings.Contains(got.Body.String(), dir) {
		t.Fatalf("catalog: %d %s", got.Code, got.Body.String())
	}
	path := "/api/panel/packages/" + p.ID + "/download"
	got = panelCall(api.Handler, http.MethodGet, path, "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), b.Bytes()) || got.Header().Get("Content-Type") != "application/zip" || got.Header().Get("X-Content-Type-Options") != "nosniff" || got.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(got.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %v", got.Code, got.Header())
	}
	got = panelCall(api.Handler, http.MethodGet, "/api/panel/packages/unknown/download", "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 404 {
		t.Fatalf("unknown id: %d", got.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, p.Filename), bytes.Repeat([]byte{'x'}, b.Len()), 0600); err != nil {
		t.Fatal(err)
	}
	got = panelCall(api.Handler, http.MethodGet, path, "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 503 {
		t.Fatalf("wrong hash: %d", got.Code)
	}
	if err := os.Remove(filepath.Join(dir, p.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "manifest.json"), filepath.Join(dir, p.Filename)); err != nil {
		t.Fatal(err)
	}
	got = panelCall(api.Handler, http.MethodGet, path, "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 503 {
		t.Fatalf("symlink: %d", got.Code)
	}
	p.Filename = "../outside.zip"
	manifest, _ = json.Marshal(struct {
		Packages []packageMetadata `json:"packages"`
	}{[]packageMetadata{p}})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	got = panelCall(api.Handler, http.MethodGet, "/api/panel/packages", "198.51.100.10", nil, adminCookie, "", "", "")
	if got.Code != 503 {
		t.Fatalf("traversal manifest: %d", got.Code)
	}
}
