package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"
)

const (
	// SessionCookieName is the panel session cookie.
	// __Host- requires Secure, Path=/ and no Domain attribute.
	SessionCookieName = "__Host-session"

	// CSRFCookieName carries the session-bound CSRF token for the panel script.
	CSRFCookieName = "__Host-csrf"

	// CSRFHeader is the header the panel sends back on writes.
	CSRFHeader = "X-CSRF-Token"

	// SessionRevocationMaxLag is how long a revoked session may still be accepted.
	// Sessions are not cached. The next request reads PostgreSQL, so the lag is zero.
	SessionRevocationMaxLag time.Duration = 0

	tokenBytes = 32
)

// NewSecret returns a raw URL-safe token and the SHA-256 stored in the database.
func NewSecret() (string, []byte, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, errors.New("falha ao gerar token")
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashSecret(raw), nil
}

// HashSecret is the database image of a session id, CSRF token or one-time token.
func HashSecret(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out
}

// SecretEqual compares two digests without leaking the mismatch position.
func SecretEqual(left, right []byte) bool {
	return hmac.Equal(left, right)
}

// SubjectHash domain-separates lockout keys so an e-mail and an IP cannot collide.
func SubjectHash(kind, value string) []byte {
	mac := sha256.New()
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

// SessionSetCookie builds the __Host-session cookie. Domain is left unset on purpose.
func SessionSetCookie(value string, expires time.Time) string {
	return (&http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires.UTC(),
	}).String()
}

// CSRFSetCookie builds the readable CSRF cookie bound to the same expiry.
func CSRFSetCookie(value string, expires time.Time) string {
	return (&http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires.UTC(),
	}).String()
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie() string {
	return clearCookie(SessionCookieName, true)
}

// ClearCSRFCookie expires the CSRF cookie.
func ClearCSRFCookie() string {
	return clearCookie(CSRFCookieName, false)
}

func clearCookie(name string, httpOnly bool) string {
	return (&http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}).String()
}
