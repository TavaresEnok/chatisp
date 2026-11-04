package outwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// HeaderSignature carries HMAC-SHA256 of the raw body.
	HeaderSignature = "X-ChatISP-Signature-256"
	// HeaderEventID is the stable id the receiver uses to detect replay.
	HeaderEventID = "X-ChatISP-Event-Id"
	// HeaderEventType names the occurrence.
	HeaderEventType = "X-ChatISP-Event-Type"
	// HeaderAttempt counts deliveries. It is not part of the signed body.
	HeaderAttempt = "X-ChatISP-Attempt"
	// HeaderReplay tells the receiver to treat HeaderEventID as an idempotency key.
	HeaderReplay = "X-ChatISP-Replay"

	signaturePrefix = "sha256="
	replayValue     = "event-id"
)

var (
	ErrSignatureMissing   = errors.New("assinatura ausente")
	ErrSignatureMalformed = errors.New("assinatura malformada")
	ErrSignatureInvalid   = errors.New("assinatura inválida")
	ErrSecret             = errors.New("segredo de webhook ausente")
)

// Sign returns the header value for the raw body. An empty secret never signs.
func Sign(secret, body []byte) (string, error) {
	if len(secret) == 0 || len(body) == 0 {
		return "", ErrSecret
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the header against HMAC-SHA256(secret, body).
// body must be the original bytes. The error text includes neither value.
func Verify(secret, body []byte, header string) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrSignatureMissing
	}
	if len(header) != len(signaturePrefix)+sha256.Size*2 || !strings.HasPrefix(header, signaturePrefix) {
		return ErrSignatureMalformed
	}
	mac, err := hex.DecodeString(header[len(signaturePrefix):])
	if err != nil || len(mac) != sha256.Size {
		return ErrSignatureMalformed
	}
	if len(secret) == 0 || len(body) == 0 {
		return ErrSignatureInvalid
	}
	sum := hmac.New(sha256.New, secret)
	_, _ = sum.Write(body)
	if !hmac.Equal(mac, sum.Sum(nil)) {
		return ErrSignatureInvalid
	}
	return nil
}
