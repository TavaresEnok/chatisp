package auth

import (
	"strings"
	"testing"
)

func TestArgon2idHashIsVersionedAndNotReversible(t *testing.T) {
	const password = "senha-correta-10"
	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "$argon2id$v=19$chatisp=v1$m=19456,t=2,p=1$") {
		t.Fatal("hash fora do catálogo v1")
	}
	if strings.Contains(encoded, password) {
		t.Fatal("a senha apareceu no hash")
	}
	ok, err := VerifyPassword(password, encoded)
	if err != nil || !ok {
		t.Fatalf("verificação: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("senha-errada-10", encoded)
	if err != nil || ok {
		t.Fatalf("senha errada: ok=%v err=%v", ok, err)
	}

	parts := strings.Split(encoded, "$")
	parts[3] = "chatisp=v9"
	if _, err := VerifyPassword(password, strings.Join(parts, "$")); err == nil {
		t.Fatal("versão desconhecida foi aceita")
	}
	parts = strings.Split(encoded, "$")
	parts[4] = "m=65536,t=2,p=1"
	if _, err := VerifyPassword(password, strings.Join(parts, "$")); err == nil {
		t.Fatal("parâmetro fora do catálogo foi aceito")
	}
	if PasswordAcceptable("curta") || PasswordAcceptable(strings.Repeat("a", 129)) || PasswordAcceptable("          ") {
		t.Fatal("política aceitou senha inválida")
	}
	if _, err := HashPassword("curta"); err == nil {
		t.Fatal("hash de senha fraca")
	}
}
