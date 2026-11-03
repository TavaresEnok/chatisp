package contactdocs

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeAndMask(t *testing.T) {
	kind, digits, err := normalize("529.982.247-25")
	if err != nil || kind != kindCPF || digits != "52998224725" {
		t.Fatalf("cpf %s %s %v", kind, digits, err)
	}
	kind, digits, err = normalize("11.222.333/0001-81")
	if err != nil || kind != kindCNPJ || digits != "11222333000181" {
		t.Fatalf("cnpj %s %s %v", kind, digits, err)
	}
	for _, raw := range []string{"", "111.111.111-11", "52998224724", "11.222.333/0001-80", "52998224725a", "123"} {
		if _, _, err = normalize(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
	if displayMask(kindCPF) != maskCPF || strings.ContainsAny(displayMask(kindCPF), "0123456789") {
		t.Fatal("cpf mask")
	}
	if displayMask(kindCNPJ) != maskCNPJ || strings.ContainsAny(displayMask(kindCNPJ), "0123456789") {
		t.Fatal("cnpj mask")
	}
	a := documentMAC([]byte("pepper-a"), uuid.MustParse("11111111-1111-1111-1111-111111111111"), digits)
	b := documentMAC([]byte("pepper-b"), uuid.MustParse("11111111-1111-1111-1111-111111111111"), digits)
	c := documentMAC([]byte("pepper-a"), uuid.MustParse("22222222-2222-2222-2222-222222222222"), digits)
	if macsEqual(a, b) || macsEqual(a, c) || len(a) != 32 {
		t.Fatal("hmac separation")
	}
}
