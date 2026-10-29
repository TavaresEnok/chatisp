package crypto

import (
	"bytes"
	"encoding/base64"
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
