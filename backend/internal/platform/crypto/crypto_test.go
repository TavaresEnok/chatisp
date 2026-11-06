package crypto

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestSealRejectsTransplantedCiphertext(t *testing.T) {
	ring := testRing(t, "v1")
	original := Context{TenantID: "tenant-a", Entity: "user", EntityID: "user-1", Field: "totp_secret"}
	sealed, err := ring.Encrypt([]byte("segredo-totp"), original)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.Decrypt(sealed.Version, sealed.ciphertextCopy(), original)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "segredo-totp" {
		t.Fatalf("plaintext: %q", plain)
	}

	transplants := []Context{
		{TenantID: "tenant-b", Entity: "user", EntityID: "user-1", Field: "totp_secret"},
		{TenantID: "tenant-a", Entity: "inbox", EntityID: "user-1", Field: "totp_secret"},
		{TenantID: "tenant-a", Entity: "user", EntityID: "user-2", Field: "totp_secret"},
		{TenantID: "tenant-a", Entity: "user", EntityID: "user-1", Field: "app_secret"},
	}
	for _, aad := range transplants {
		if _, err := ring.Decrypt(sealed.Version, sealed.ciphertextCopy(), aad); err == nil {
			t.Fatalf("transplante aceito: %#v", aad)
		}
	}

	flipped := sealed.ciphertextCopy()
	flipped[len(flipped)-1] ^= 0x01
	if _, err := ring.Decrypt(sealed.Version, flipped, original); err == nil {
		t.Fatal("ciphertext alterado foi aceito")
	}
	again, err := ring.Encrypt([]byte("segredo-totp"), original)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed.Ciphertext, again.Ciphertext) {
		t.Fatal("nonce repetido")
	}
}

func TestKeyRotationDecryptsOlderVersion(t *testing.T) {
	first := testRing(t, "v1")
	aad := Context{TenantID: "tenant-a", Entity: "user", EntityID: "user-1", Field: "totp_secret"}
	sealed, err := first.Encrypt([]byte("segredo-antigo"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Version != "v1" {
		t.Fatalf("versão: %s", sealed.Version)
	}

	rotated := testRingVersions(t, "v2", "v1", "v2")
	plain, err := rotated.Decrypt(sealed.Version, sealed.Ciphertext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "segredo-antigo" {
		t.Fatalf("plaintext: %q", plain)
	}
	fresh, err := rotated.Encrypt([]byte("segredo-novo"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version != "v2" {
		t.Fatalf("versão nova: %s", fresh.Version)
	}
	if _, err := first.Decrypt(fresh.Version, fresh.Ciphertext, aad); err == nil {
		t.Fatal("chave antiga abriu ciphertext da versão nova")
	}
}

func TestParseRejectsBadMaterialWithoutEcho(t *testing.T) {
	secret := "super-secret-key-material-value"
	_, err := Parse("v1:"+secret, "v1")
	if err == nil {
		t.Fatal("esperava falha")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("erro expôs material: %s", err)
	}
	_, err = Parse("", "v1")
	if err == nil {
		t.Fatal("esperava material vazio")
	}
}

func testRing(t *testing.T, current string) *Ring {
	t.Helper()
	return testRingVersions(t, current, current)
}

func testRingVersions(t *testing.T, current string, versions ...string) *Ring {
	t.Helper()
	var lines []string
	for i, version := range versions {
		raw := bytes.Repeat([]byte{byte(i + 3)}, 32)
		lines = append(lines, version+":"+base64.StdEncoding.EncodeToString(raw))
	}
	ring, err := Parse(strings.Join(lines, "\n"), current)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func (s Sealed) ciphertextCopy() []byte {
	return bytes.Clone(s.Ciphertext)
}

func TestFingerprintContextAndRotation(t *testing.T) {
	first := testRing(t, "v1")
	aad := Context{TenantID: "tenant-a", Entity: "connector", EntityID: "connector-a", Field: "identity"}
	fingerprint, err := first.Fingerprint("v1", []byte("subject"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", fingerprint); got != "372f79aef39cedae2f169b77c0bd6334e64b7a6bef613b28a173e0fa9fbd593e" {
		t.Fatalf("impressão divergente: %s", got)
	}
	rotated := testRingVersions(t, "v2", "v1", "v2")
	repeat, err := rotated.Fingerprint("v1", []byte("subject"), aad)
	if err != nil || !bytes.Equal(fingerprint, repeat) {
		t.Fatalf("versão antiga mudou após rotação: %v", err)
	}
	fresh, err := rotated.Fingerprint("v2", []byte("subject"), aad)
	if err != nil || bytes.Equal(fingerprint, fresh) {
		t.Fatalf("versão nova não separada: %v", err)
	}
	for _, changed := range []Context{
		{TenantID: "tenant-b", Entity: "connector", EntityID: "connector-a", Field: "identity"},
		{TenantID: "tenant-a", Entity: "connector", EntityID: "connector-b", Field: "identity"},
		{TenantID: "tenant-a", Entity: "other", EntityID: "connector-a", Field: "identity"},
		{TenantID: "tenant-a", Entity: "connector", EntityID: "connector-a", Field: "other"},
	} {
		got, err := first.Fingerprint("v1", []byte("subject"), changed)
		if err != nil || bytes.Equal(got, fingerprint) {
			t.Fatalf("contexto não separado: %#v, %v", changed, err)
		}
	}
	other, err := first.Fingerprint("v1", []byte("other"), aad)
	if err != nil || bytes.Equal(other, fingerprint) {
		t.Fatalf("identidade não separada: %v", err)
	}
}

func TestFingerprintRejectsInvalidInputs(t *testing.T) {
	ring := testRing(t, "v1")
	ctx := Context{TenantID: "t", Entity: "connector", EntityID: "c", Field: "identity"}
	cases := []struct {
		ring    *Ring
		version string
		input   []byte
		context Context
	}{
		{nil, "v1", []byte("x"), ctx},
		{ring, "missing", []byte("x"), ctx},
		{ring, "", []byte("x"), ctx},
		{ring, "v1", nil, ctx},
		{ring, "v1", make([]byte, 4097), ctx},
		{ring, "v1", []byte("x"), Context{}},
		{ring, "v1", []byte("x"), Context{TenantID: "t\x00x", Entity: "e", EntityID: "i", Field: "f"}},
		{ring, "v1", []byte("x"), Context{TenantID: "t\x1fx", Entity: "e", EntityID: "i", Field: "f"}},
	}
	for _, tc := range cases {
		if got, err := tc.ring.Fingerprint(tc.version, tc.input, tc.context); err == nil || got != nil {
			t.Fatal("entrada inválida aceita")
		}
	}
	if _, err := ring.Fingerprint("v1", make([]byte, 4096), ctx); err != nil {
		t.Fatalf("limite válido recusado: %v", err)
	}
}
