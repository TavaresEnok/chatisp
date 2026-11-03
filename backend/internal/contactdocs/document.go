package contactdocs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	kindCPF  = "cpf"
	kindCNPJ = "cnpj"
	maskCPF  = "***.***.***-**"
	maskCNPJ = "**.***.***/****-**"
)

func normalize(raw string) (kind, digits string, err error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '/' || unicode.IsSpace(r):
		default:
			return "", "", ErrInvalid
		}
	}
	digits = b.String()
	switch {
	case len(digits) == 11 && validCPF(digits):
		return kindCPF, digits, nil
	case len(digits) == 14 && validCNPJ(digits):
		return kindCNPJ, digits, nil
	default:
		return "", "", ErrInvalid
	}
}

func validCPF(d string) bool {
	if allSame(d) {
		return false
	}
	return checkDigit(d[:9], 10) == int(d[9]-'0') && checkDigit(d[:10], 11) == int(d[10]-'0')
}

func validCNPJ(d string) bool {
	if allSame(d) {
		return false
	}
	return cnpjDigit(d[:12]) == int(d[12]-'0') && cnpjDigit(d[:13]) == int(d[13]-'0')
}

func allSame(d string) bool {
	if d == "" {
		return true
	}
	for i := 1; i < len(d); i++ {
		if d[i] != d[0] {
			return false
		}
	}
	return true
}

func checkDigit(base string, weight int) int {
	sum := 0
	for _, c := range base {
		sum += int(c-'0') * weight
		weight--
	}
	rest := sum % 11
	if rest < 2 {
		return 0
	}
	return 11 - rest
}

func cnpjDigit(base string) int {
	weights := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	weights = weights[len(weights)-len(base):]
	sum := 0
	for i, c := range base {
		sum += int(c-'0') * weights[i]
	}
	rest := sum % 11
	if rest < 2 {
		return 0
	}
	return 11 - rest
}

func displayMask(kind string) string {
	switch kind {
	case kindCPF:
		return maskCPF
	case kindCNPJ:
		return maskCNPJ
	default:
		return ""
	}
}

func documentMAC(pepper []byte, tenant uuid.UUID, digits string) []byte {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte("contact-document"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(tenant.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(digits))
	return mac.Sum(nil)
}

func requestMAC(pepper []byte, op string, contact uuid.UUID, version int64, digits string) []byte {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(op))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(contact[:])
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(version))
	_, _ = mac.Write(buf[:])
	_, _ = mac.Write([]byte(digits))
	return mac.Sum(nil)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func macsEqual(a, b []byte) bool {
	return hmac.Equal(a, b)
}
