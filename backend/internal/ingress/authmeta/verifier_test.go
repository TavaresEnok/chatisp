package authmeta

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignatureUsesOriginalBytesAndSecret(t *testing.T) {
	secret := "app-secret-9c1e"
	other := "app-secret-outro"
	body := []byte("{\"a\":1}\n")
	spaced := []byte("{\"a\": 1}\n")
	header := signBody(secret, body)

	if err := VerifySignature([]byte(secret), body, header); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature([]byte(other), body, header); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("outro app: %v", err)
	}
	if err := VerifySignature([]byte(secret), spaced, header); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("whitespace: %v", err)
	}
	if err := VerifySignature(nil, body, header); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("segredo vazio: %v", err)
	}
	upper := "sha256=" + strings.ToUpper(hex.EncodeToString(mustMAC(secret, body)))
	if err := VerifySignature([]byte(secret), body, "  "+upper+"  "); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySignatureRejectsMissingAndMalformed(t *testing.T) {
	body := []byte("abc")
	secret := []byte("app-secret-9c1e")
	cases := []struct {
		name   string
		header string
		want   error
	}{
		{name: "ausente", header: "", want: ErrSignatureMissing},
		{name: "espacos", header: "   ", want: ErrSignatureMissing},
		{name: "prefixo", header: "sha1=" + strings.Repeat("ab", 32), want: ErrSignatureMalformed},
		{name: "curta", header: "sha256=" + strings.Repeat("ab", 16), want: ErrSignatureMalformed},
		{name: "hex", header: "sha256=" + strings.Repeat("zz", 32), want: ErrSignatureMalformed},
		{name: "longa", header: "sha256=" + strings.Repeat("ab", 33), want: ErrSignatureMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySignature(secret, body, tc.header)
			if !errors.Is(err, tc.want) {
				t.Fatalf("erro %v", err)
			}
			if strings.Contains(err.Error(), "app-secret-9c1e") || strings.Contains(err.Error(), "abc") {
				t.Fatal("erro expôs material")
			}
		})
	}
	wrong := signBody("app-secret-9c1e", []byte("abd"))
	if err := VerifySignature(secret, body, wrong); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("inválida: %v", err)
	}
}

func TestVerifyChallengeUsesSeparateToken(t *testing.T) {
	secret := []byte("app-secret-9c1e")
	token := []byte("verify-token-44ab")
	challenge := "challenge-9f3c2a"

	got, err := VerifyChallenge(token, "subscribe", string(token), challenge)
	if err != nil {
		t.Fatal(err)
	}
	if got != challenge {
		t.Fatalf("desafio %q", got)
	}
	got, err = VerifyChallenge(token, "subscribe", string(secret), challenge)
	if !errors.Is(err, ErrTokenInvalid) || got != "" {
		t.Fatalf("segredo aceito como token: %v %q", err, got)
	}
	got, err = VerifyChallenge(token, "subscribe", "outro-token", challenge)
	if !errors.Is(err, ErrTokenInvalid) || got != "" {
		t.Fatalf("token inválido: %v %q", err, got)
	}
	if _, err := VerifyChallenge(nil, "subscribe", "", challenge); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("vazio: %v", err)
	}
	got, err = VerifyChallenge(token, "unsubscribe", string(token), challenge)
	if !errors.Is(err, ErrChallengeRejected) || got != "" {
		t.Fatalf("modo: %v %q", err, got)
	}
	got, err = VerifyChallenge(token, "subscribe", string(token), "")
	if !errors.Is(err, ErrChallengeRejected) || got != "" {
		t.Fatalf("challenge vazio: %v %q", err, got)
	}
	for _, err := range []error{ErrTokenInvalid, ErrChallengeRejected} {
		text := err.Error()
		if strings.Contains(text, string(secret)) || strings.Contains(text, string(token)) || strings.Contains(text, challenge) {
			t.Fatal("erro expôs material")
		}
	}
}

func mustMAC(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}
