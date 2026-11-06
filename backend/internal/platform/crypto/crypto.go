// Package crypto seals recoverable secrets with AES-GCM.
// Master keys stay outside the database. Each ciphertext names the key version
// that produced it, so a later version can still open older rows.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	keySize   = 32
	nonceSize = 12
	maxPart   = 200
)

// Context is the authenticated data bound to a ciphertext.
// A value sealed for one tenant, entity and field does not open for another.
type Context struct {
	TenantID string
	Entity   string
	EntityID string
	Field    string
}

// Sealed is one AES-GCM ciphertext and the master-key version that created it.
// Ciphertext starts with a random nonce. The version is not a secret.
type Sealed struct {
	Version    string
	Ciphertext []byte
}

// Ring holds versioned master keys. It is safe to share for concurrent reads.
type Ring struct {
	current string
	keys    map[string][]byte
}

// Parse decodes versioned keys. material is "version:base64" entries separated
// by commas or newlines. current must name one of those versions.
// The error text never includes key bytes.
func Parse(material, current string) (*Ring, error) {
	keys, err := decodeMaterial(material)
	if err != nil {
		return nil, err
	}
	current = strings.TrimSpace(current)
	if _, ok := keys[current]; !ok {
		return nil, errors.New("versão de chave mestra inválida")
	}
	return &Ring{current: current, keys: keys}, nil
}

// Load reads keys from an environment value or a file. The file wins when both
// are empty-checked by the caller. An empty selection returns a nil ring.
func Load(material, filePath, current string) (*Ring, error) {
	if strings.TrimSpace(material) != "" && strings.TrimSpace(filePath) != "" {
		return nil, errors.New("origem de chave mestra ambígua")
	}
	if strings.TrimSpace(filePath) != "" {
		body, err := os.ReadFile(filePath)
		if err != nil || len(body) == 0 || len(body) > 1<<20 {
			return nil, errors.New("arquivo de chave mestra inválido")
		}
		material = string(body)
	}
	if strings.TrimSpace(material) == "" {
		return nil, errors.New("chave mestra ausente")
	}
	return Parse(material, current)
}

// Current is the version used to seal new secrets.
func (r *Ring) Current() string {
	if r == nil {
		return ""
	}
	return r.current
}

// Fingerprint computes a stable, domain-separated identity marker for a key
// version. Store the version alongside the marker so it can be recomputed
// after rotation. Context binds the marker to its tenant and intended use.
func (r *Ring) Fingerprint(version string, plaintext []byte, aad Context) ([]byte, error) {
	invalid := func() ([]byte, error) { return nil, errors.New("não foi possível calcular a impressão") }
	if r == nil || len(plaintext) == 0 || len(plaintext) > 4096 {
		return invalid()
	}
	key, ok := r.keys[version]
	if !ok {
		return invalid()
	}
	associated, err := aad.bytes()
	if err != nil {
		return invalid()
	}
	derive := hmac.New(sha256.New, key)
	_, _ = derive.Write([]byte("chatisp/privacy-identity/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	_, _ = mac.Write(associated)
	_, _ = mac.Write([]byte{0}) // Context parts cannot contain NUL.
	_, _ = mac.Write(plaintext)
	return mac.Sum(nil), nil
}

// Encrypt seals plaintext under the current key and the supplied context.
func (r *Ring) Encrypt(plaintext []byte, aad Context) (Sealed, error) {
	if r == nil {
		return Sealed{}, errors.New("chave mestra ausente")
	}
	if len(plaintext) == 0 || len(plaintext) > 4096 {
		return Sealed{}, errors.New("segredo inválido")
	}
	key, ok := r.keys[r.current]
	if !ok {
		return Sealed{}, errors.New("versão de chave mestra inválida")
	}
	return seal(r.current, key, plaintext, aad)
}

// Decrypt opens a ciphertext with the key version that sealed it.
// The same context must be presented. A transplanted blob fails.
func (r *Ring) Decrypt(version string, ciphertext []byte, aad Context) ([]byte, error) {
	if r == nil {
		return nil, errors.New("chave mestra ausente")
	}
	key, ok := r.keys[version]
	if !ok {
		return nil, errors.New("não foi possível ler o segredo")
	}
	return open(key, ciphertext, aad)
}

func seal(version string, key, plaintext []byte, aad Context) (Sealed, error) {
	associated, err := aad.bytes()
	if err != nil {
		return Sealed{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Sealed{}, errors.New("não foi possível cifrar o segredo")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Sealed{}, errors.New("não foi possível cifrar o segredo")
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Sealed{}, errors.New("não foi possível cifrar o segredo")
	}
	sealed := gcm.Seal(nil, nonce, plaintext, associated)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return Sealed{Version: version, Ciphertext: out}, nil
}

func open(key, ciphertext []byte, aad Context) ([]byte, error) {
	associated, err := aad.bytes()
	if err != nil {
		return nil, errors.New("não foi possível ler o segredo")
	}
	if len(ciphertext) < nonceSize+16 {
		return nil, errors.New("não foi possível ler o segredo")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("não foi possível ler o segredo")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("não foi possível ler o segredo")
	}
	nonce := ciphertext[:nonceSize]
	body := ciphertext[nonceSize:]
	plain, err := gcm.Open(nil, nonce, body, associated)
	if err != nil {
		return nil, errors.New("não foi possível ler o segredo")
	}
	return plain, nil
}

func (c Context) bytes() ([]byte, error) {
	parts := []string{c.TenantID, c.Entity, c.EntityID, c.Field}
	for _, part := range parts {
		if err := validPart(part); err != nil {
			return nil, err
		}
	}
	return []byte(strings.Join(parts, "\x1f")), nil
}

func validPart(part string) error {
	if part == "" || len(part) > maxPart || strings.ContainsAny(part, "\x00\x1f") {
		return errors.New("contexto de cifragem inválido")
	}
	return nil
}

func decodeMaterial(material string) (map[string][]byte, error) {
	material = strings.ReplaceAll(material, "\r\n", "\n")
	material = strings.ReplaceAll(material, ",", "\n")
	keys := make(map[string][]byte)
	for _, line := range strings.Split(material, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		version, encoded, ok := strings.Cut(line, ":")
		if !ok || !validVersion(version) {
			return nil, errors.New("chave mestra inválida")
		}
		if _, exists := keys[version]; exists {
			return nil, errors.New("chave mestra inválida")
		}
		raw, err := decodeKey(encoded)
		if err != nil {
			return nil, errors.New("chave mestra inválida")
		}
		keys[version] = raw
	}
	if len(keys) == 0 {
		return nil, errors.New("chave mestra inválida")
	}
	return keys, nil
}

func validVersion(version string) bool {
	if version == "" || len(version) > 32 {
		return false
	}
	for i, r := range version {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func decodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var last error
	for _, enc := range encodings {
		raw, err := enc.DecodeString(encoded)
		if err != nil {
			last = err
			continue
		}
		if len(raw) != keySize {
			return nil, fmt.Errorf("tamanho")
		}
		return raw, nil
	}
	if last == nil {
		last = errors.New("chave")
	}
	return nil, last
}
