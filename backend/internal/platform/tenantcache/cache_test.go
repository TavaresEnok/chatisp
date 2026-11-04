package tenantcache

import (
	"testing"
	"time"
)

func TestCacheIsolatesTenantAndRejectsMessages(t *testing.T) {
	cache, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)

	const body = "mensagem-secreta-do-cliente"
	if err := cache.Set("tenant-a", "message", "m1", 0, body); err == nil {
		t.Fatal("mensagem entrou no cache")
	}
	if err := cache.Set("tenant-a", "conversation", "c1", 0, body); err == nil {
		t.Fatal("conversa entrou no cache")
	}
	if err := cache.Set("", KindConfig, "identity", 0, "Nexo"); err == nil {
		t.Fatal("tenant vazio")
	}
	if err := cache.Set("tenant-a", KindConfig, "identity", 0, "Nexo"); err != nil {
		t.Fatal(err)
	}
	var other string
	if cache.Get("tenant-b", KindConfig, "identity", 0, &other) || other == "Nexo" {
		t.Fatal("outro tenant leu a configuração")
	}
	var got string
	if !cache.Get("tenant-a", KindConfig, "identity", 0, &got) || got != "Nexo" {
		t.Fatalf("leitura: %q", got)
	}
	cache.Invalidate("tenant-a", KindConfig, "identity")
	if cache.Get("tenant-a", KindConfig, "identity", 0, &got) {
		t.Fatal("invalidação não tirou a entrada")
	}
}

func TestPermissionRequiresMatchingVersion(t *testing.T) {
	cache, err := New()
	if err != nil {
		t.Fatal(err)
	}
	version, ok := PermissionVersion("admin")
	if !ok || version != 1 {
		t.Fatalf("versão: %d %v", version, ok)
	}
	if _, known := PermissionVersion("owner"); known {
		t.Fatal("papel desconhecido")
	}
	if err := cache.Set("tenant-a", KindPermission, "member-1", 0, "admin"); err == nil {
		t.Fatal("permissão sem versão")
	}
	if err := cache.Set("tenant-a", KindPermission, "member-1", version, "admin"); err != nil {
		t.Fatal(err)
	}
	var role string
	if !cache.Get("tenant-a", KindPermission, "member-1", version, &role) || role != "admin" {
		t.Fatalf("papel: %q", role)
	}
	if cache.Get("tenant-a", KindPermission, "member-1", version+1, &role) {
		t.Fatal("versão diferente foi aceita")
	}
	if cache.Get("tenant-a", KindPermission, "member-1", version, &role) {
		t.Fatal("versão divergente não derrubou a entrada")
	}
}

func TestTemplateAndInboxRoundTrip(t *testing.T) {
	cache, err := New()
	if err != nil {
		t.Fatal(err)
	}
	type hours struct {
		Version  int64  `json:"version"`
		Timezone string `json:"timezone"`
	}
	if err := cache.Set("tenant-a", KindInbox, "inbox-1", 4, hours{Version: 4, Timezone: "America/Sao_Paulo"}); err != nil {
		t.Fatal(err)
	}
	var stored hours
	if !cache.Get("tenant-a", KindInbox, "inbox-1", 0, &stored) || stored.Version != 4 || stored.Timezone != "America/Sao_Paulo" {
		t.Fatalf("caixa: %+v", stored)
	}
	type template struct {
		Name string `json:"name"`
	}
	if err := cache.Set("tenant-a", KindTemplate, "waba-1", 0, []template{{Name: "reinicio"}}); err != nil {
		t.Fatal(err)
	}
	var catalog []template
	if !cache.Get("tenant-a", KindTemplate, "waba-1", 0, &catalog) || len(catalog) != 1 || catalog[0].Name != "reinicio" {
		t.Fatalf("catálogo: %+v", catalog)
	}
	if err := cache.Set("tenant-a", KindConfig, "identity", 0, stringsOf(maxBody)); err == nil {
		t.Fatal("valor grande")
	}
}

func TestEntryExpires(t *testing.T) {
	cache, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cache.ttl = 40 * time.Millisecond
	if err := cache.Set("tenant-a", KindConfig, "identity", 0, "Nexo"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	var got string
	if cache.Get("tenant-a", KindConfig, "identity", 0, &got) {
		t.Fatal("ttl não venceu")
	}
}

func stringsOf(n int) string {
	return string(make([]byte, n))
}
