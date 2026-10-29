// Package auth implements panel passwords, sessions, invites and password recovery.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// PasswordParamVersion is the catalog entry used for new hashes.
	// Older entries stay in passwordCatalog so existing hashes still verify.
	PasswordParamVersion = "v1"

	passwordAlgorithm = "argon2id"
	passwordAlgoVer   = "v=19"
	passwordSaltLen   = 16
	passwordKeyLen    = 32
)

// passwordParam is one calibrated Argon2id parameter set.
// Memory is KiB, as required by argon2.IDKey.
type passwordParam struct {
	name    string
	memory  uint32
	time    uint32
	threads uint8
}

// passwordCatalog is the allow-list. Verification refuses parameters that are
// not listed here, so a stored hash cannot request an unbounded amount of memory.
var passwordCatalog = []passwordParam{
	{name: PasswordParamVersion, memory: 19 * 1024, time: 2, threads: 1},
}

func currentPasswordParam() passwordParam {
	return passwordCatalog[0]
}

// HashPassword returns a versioned Argon2id PHC string.
// The same password is never stored in a reversible form.
func HashPassword(password string) (string, error) {
	if !PasswordAcceptable(password) {
		return "", errWeakPassword
	}
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("falha ao gerar a senha")
	}
	param := currentPasswordParam()
	sum := argon2.IDKey([]byte(password), salt, param.time, param.memory, param.threads, passwordKeyLen)
	return encodePassword(param, salt, sum), nil
}

// VerifyPassword reports whether password matches a versioned hash.
// A mismatch is false with a nil error. A malformed or unknown parameter set is an error.
func VerifyPassword(password, encoded string) (bool, error) {
	parsed, err := decodePassword(encoded)
	if err != nil {
		return false, err
	}
	sum := argon2.IDKey([]byte(password), parsed.salt, parsed.param.time, parsed.param.memory, parsed.param.threads, uint32(len(parsed.key)))
	if subtle.ConstantTimeCompare(sum, parsed.key) != 1 {
		return false, nil
	}
	return true, nil
}

// PasswordAcceptable is the panel password policy: 10 to 128 bytes, not only spaces.
func PasswordAcceptable(password string) bool {
	if len(password) < 10 || len(password) > 128 {
		return false
	}
	if strings.TrimSpace(password) == "" || strings.ContainsRune(password, 0) {
		return false
	}
	return true
}

var errWeakPassword = errors.New("a senha não atende à política")

type parsedPassword struct {
	param passwordParam
	salt  []byte
	key   []byte
}

func encodePassword(param passwordParam, salt, key []byte) string {
	return fmt.Sprintf(
		"$%s$%s$chatisp=%s$m=%d,t=%d,p=%d$%s$%s",
		passwordAlgorithm,
		passwordAlgoVer,
		param.name,
		param.memory,
		param.time,
		param.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func decodePassword(encoded string) (parsedPassword, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 7 || parts[0] != "" {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	if parts[1] != passwordAlgorithm || parts[2] != passwordAlgoVer {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	name, ok := strings.CutPrefix(parts[3], "chatisp=")
	if !ok || name == "" {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	param, ok := catalogParam(name)
	if !ok {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	memory, timeCost, threads, err := parseArgonParams(parts[4])
	if err != nil || memory != param.memory || timeCost != param.time || threads != param.threads {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[6])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return parsedPassword{}, errors.New("hash de senha inválido")
	}
	return parsedPassword{param: param, salt: salt, key: key}, nil
}

func catalogParam(name string) (passwordParam, bool) {
	for _, param := range passwordCatalog {
		if param.name == name {
			return param, true
		}
	}
	return passwordParam{}, false
}

func parseArgonParams(raw string) (uint32, uint32, uint8, error) {
	var memory, timeCost uint32
	var threads uint8
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || seen[key] {
			return 0, 0, 0, errors.New("hash de senha inválido")
		}
		seen[key] = true
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return 0, 0, 0, errors.New("hash de senha inválido")
		}
		switch key {
		case "m":
			memory = uint32(number)
		case "t":
			timeCost = uint32(number)
		case "p":
			if number == 0 || number > 255 {
				return 0, 0, 0, errors.New("hash de senha inválido")
			}
			threads = uint8(number)
		default:
			return 0, 0, 0, errors.New("hash de senha inválido")
		}
	}
	if !seen["m"] || !seen["t"] || !seen["p"] {
		return 0, 0, 0, errors.New("hash de senha inválido")
	}
	return memory, timeCost, threads, nil
}
