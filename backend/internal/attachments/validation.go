package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
)

func safeName(s string) (string, error) {
	if s == "" || len(s) > 180 || strings.ContainsAny(s, "/\\") || filepath.Base(s) != s || s == "." || s == ".." {
		return "", ErrInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", ErrInvalid
		}
	}
	return s, nil
}

// Signature validation is deliberately conservative. No HTML, SVG, XML, ZIP,
// office macros or unknown binary fallback; everything is downloaded as octets.
// A signature is not malware scanning or a duration/codec certificate.
func inspect(b []byte, direction string) (string, string, error) {
	typ := http.DetectContentType(b)
	switch typ {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf", "audio/mpeg", "application/ogg", "audio/wave":
	default:
		if len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
			typ = "image/webp"
		} else if len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) && bytes.Equal(b[8:12], []byte("M4A ")) {
			typ = "audio/mp4"
		} else if typ == "video/mp4" || ((typ == "video/webm" || typ == "audio/webm") && direction == "outgoing") {
		} else if direction == "outgoing" && isWebM(b) {
			typ = "video/webm"
		} else {
			return "", "", ErrInvalid
		}
	}
	sum := sha256.Sum256(b)
	return typ, hex.EncodeToString(sum[:]), nil
}

func isWebM(b []byte) bool {
	return len(b) >= 4 && b[0] == 0x1a && b[1] == 0x45 && b[2] == 0xdf && b[3] == 0xa3
}
