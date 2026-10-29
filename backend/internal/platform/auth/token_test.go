package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSessionCookieFlagsAndHashedSecret(t *testing.T) {
	raw, sum, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 40 || len(sum) != 32 || string(sum) == raw {
		t.Fatal("token não ficou só com o hash armazenável")
	}
	if !SecretEqual(HashSecret(raw), sum) || SecretEqual(HashSecret("outro"), sum) {
		t.Fatal("comparação de segredo falhou")
	}
	expires := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		name     string
		value    string
		httpOnly bool
	}{
		{SessionCookieName, SessionSetCookie(raw, expires), true},
		{CSRFCookieName, CSRFSetCookie("csrf-publico", expires), false},
	} {
		if strings.Contains(strings.ToLower(item.value), "domain=") {
			t.Fatalf("cookie com Domain: %s", item.name)
		}
		parsed, err := http.ParseSetCookie(item.value)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Name != item.name || parsed.Path != "/" || !parsed.Secure || parsed.HttpOnly != item.httpOnly || parsed.SameSite != http.SameSiteLaxMode {
			t.Fatalf("flags de %s: %#v", item.name, parsed)
		}
	}
	cleared, err := http.ParseSetCookie(ClearSessionCookie())
	if err != nil {
		t.Fatal(err)
	}
	if cleared.MaxAge >= 0 || !cleared.HttpOnly || !cleared.Secure || cleared.Path != "/" {
		t.Fatalf("cookie de limpeza: %#v", cleared)
	}
}
