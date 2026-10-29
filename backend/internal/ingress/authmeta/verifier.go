// Package authmeta authenticates one Meta webhook binding.
// Signature checks use the raw body and that binding's App Secret.
// The verify token is a different secret and is only used for the
// subscription challenge. Nothing in this package parses Graph payloads,
// chooses a tenant, or stores a delivery.
package authmeta

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// SignatureHeader is the Meta header that carries the raw-body HMAC.
	SignatureHeader = "X-Hub-Signature-256"

	signaturePrefix = "sha256="
	signatureHexLen = sha256.Size * 2
	verifyLabel     = "chatisp.meta.verify"
	maxChallengeLen = 4096
)

var (
	// ErrSignatureMissing means the signature header was not sent.
	ErrSignatureMissing = errors.New("assinatura ausente")
	// ErrSignatureMalformed means the header is not sha256 plus 32 raw bytes.
	ErrSignatureMalformed = errors.New("assinatura malformada")
	// ErrSignatureInvalid means the header does not match this app secret and body.
	ErrSignatureInvalid = errors.New("assinatura inválida")
	// ErrTokenInvalid means the verify token does not match the configured token.
	ErrTokenInvalid = errors.New("token de verificação inválido")
	// ErrChallengeRejected means the token matched but the challenge is not acceptable.
	ErrChallengeRejected = errors.New("desafio de verificação rejeitado")
	// ErrBodyTooLarge means the raw body is above the configured limit.
	ErrBodyTooLarge = errors.New("corpo acima do limite")
)

// VerifySignature checks header against HMAC-SHA256(secret, body).
// body must be the original request bytes. Whitespace is significant.
// secret empty never matches. The error text does not include either value.
func VerifySignature(secret, body []byte, header string) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrSignatureMissing
	}
	if len(header) != len(signaturePrefix)+signatureHexLen || !strings.HasPrefix(header, signaturePrefix) {
		return ErrSignatureMalformed
	}
	mac, err := hex.DecodeString(header[len(signaturePrefix):])
	if err != nil || len(mac) != sha256.Size {
		return ErrSignatureMalformed
	}
	sum := hmac.New(sha256.New, secret)
	_, _ = sum.Write(body)
	match := hmac.Equal(mac, sum.Sum(nil))
	if len(secret) == 0 || !match {
		return ErrSignatureInvalid
	}
	return nil
}

// VerifyChallenge checks the subscription handshake.
// expected is the verification token, not the App Secret.
// On success the returned text is the hub.challenge value to echo.
func VerifyChallenge(expected []byte, mode, provided, challenge string) (string, error) {
	if !tokenEqual(expected, []byte(provided)) {
		return "", ErrTokenInvalid
	}
	if mode != "subscribe" || challenge == "" || len(challenge) > maxChallengeLen {
		return "", ErrChallengeRejected
	}
	return challenge, nil
}

// tokenEqual compares two secrets in constant time.
// An empty value never matches, including another empty value.
func tokenEqual(expected, provided []byte) bool {
	left := hmac.New(sha256.New, expected)
	_, _ = left.Write([]byte(verifyLabel))
	right := hmac.New(sha256.New, provided)
	_, _ = right.Write([]byte(verifyLabel))
	match := hmac.Equal(left.Sum(nil), right.Sum(nil))
	if len(expected) == 0 || len(provided) == 0 {
		return false
	}
	return match
}
