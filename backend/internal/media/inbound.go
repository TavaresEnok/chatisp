package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	// ErrCodeSize is stored when the download exceeds the type limit.
	ErrCodeSize = "tamanho_excedido"
	// ErrCodeMIME is stored when the declared type does not match the bytes.
	ErrCodeMIME = "mime_divergente"
	// ErrCodeUnknown is stored when sniffing cannot name a supported type.
	ErrCodeUnknown = "mime_nao_reconhecido"
	// ErrCodeEmpty is stored when the provider returned no bytes.
	ErrCodeEmpty = "vazio"
)

// Limit returns the maximum accepted size for an inbound media kind.
func Limit(kind string) int64 {
	switch kind {
	case "image":
		return 5 << 20
	case "sticker":
		return 100 << 10
	case "audio", "voice":
		return 16 << 20
	case "video":
		return 16 << 20
	case "document":
		return 32 << 20
	default:
		return 0
	}
}

// InspectInbound sniffs the bytes and compares them with the declared MIME.
// An empty code means the object can be stored. sum is the SHA-256 hex.
func InspectInbound(kind, declared string, body []byte) (sniffed, sum, code string) {
	limit := Limit(kind)
	if limit <= 0 || int64(len(body)) > limit {
		return "", "", ErrCodeSize
	}
	if len(body) == 0 {
		return "", "", ErrCodeEmpty
	}
	sniffed = sniff(body)
	if sniffed == "" || !kindAllows(kind, sniffed) {
		return sniffed, "", ErrCodeUnknown
	}
	if declared != "" && !mimeCompatible(declared, sniffed) {
		return sniffed, "", ErrCodeMIME
	}
	digest := sha256.Sum256(body)
	return sniffed, hex.EncodeToString(digest[:]), ""
}

func sniff(b []byte) string {
	if len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	if len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) {
		return sniffMP4(b)
	}
	if office := sniffOffice(b); office != "" {
		return office
	}
	switch typ := canonicalMIME(http.DetectContentType(b)); typ {
	case "", "application/octet-stream":
		return ""
	default:
		return typ
	}
}

func sniffOffice(b []byte) string {
	if bytes.HasPrefix(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
		return "application/vnd.ms-office"
	}
	if bytes.HasPrefix(b, []byte("{\\rtf")) {
		return "application/rtf"
	}
	if len(b) < 4 || !bytes.Equal(b[:4], []byte("PK\x03\x04")) {
		return ""
	}
	window := b
	if len(window) > 4096 {
		window = window[:4096]
	}
	switch {
	case bytes.Contains(window, []byte("word/")):
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case bytes.Contains(window, []byte("xl/")):
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case bytes.Contains(window, []byte("ppt/")):
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	default:
		return "application/zip"
	}
}

// sniffMP4 classifies the container by its tracks. A generic major brand such
// as isom is audio only when the file has audio and no video. A video track
// stays video/mp4 even when the declared MIME or the brand says audio.
func sniffMP4(b []byte) string {
	audio, video := false, false
	walkMP4(b, &audio, &video)
	if video {
		return "video/mp4"
	}
	if audio {
		return "audio/mp4"
	}
	switch string(b[8:12]) {
	case "M4A ", "M4A\x00", "M4B ", "M4B\x00":
		return "audio/mp4"
	default:
		return "video/mp4"
	}
}

func walkMP4(b []byte, audio, video *bool) {
	for off := 0; off+8 <= len(b) && !*video; {
		size := int(binary.BigEndian.Uint32(b[off : off+4]))
		typ := string(b[off+4 : off+8])
		header := 8
		if size == 0 {
			size = len(b) - off
		}
		if size == 1 {
			if off+16 > len(b) {
				return
			}
			wide := binary.BigEndian.Uint64(b[off+8 : off+16])
			if wide < 16 || wide > uint64(len(b)-off) {
				return
			}
			size = int(wide)
			header = 16
		}
		if size < header || off+size > len(b) {
			return
		}
		payload := b[off+header : off+size]
		switch typ {
		case "hdlr":
			if len(payload) >= 12 {
				switch string(payload[8:12]) {
				case "soun":
					*audio = true
				case "vide":
					*video = true
				}
			}
		case "mp4a", "ac-3", "ec-3", "samr", "sawb", "Opus", "fLaC", "alac", "twos", "sowt":
			*audio = true
		case "avc1", "avc3", "hev1", "hvc1", "mp4v", "vp09", "av01", "encv":
			*video = true
		default:
			if body, ok := mp4Children(typ, payload); ok {
				walkMP4(body, audio, video)
			}
		}
		off += size
	}
}

func mp4Children(typ string, payload []byte) ([]byte, bool) {
	switch typ {
	case "moov", "trak", "mdia", "minf", "stbl", "edts", "moof", "traf", "mvex", "dinf":
		return payload, true
	case "stsd", "meta":
		if len(payload) < 8 {
			return nil, false
		}
		return payload[8:], true
	default:
		return nil, false
	}
}

func kindAllows(kind, sniffed string) bool {
	switch kind {
	case "image", "sticker":
		return strings.HasPrefix(sniffed, "image/")
	case "audio", "voice":
		return strings.HasPrefix(sniffed, "audio/") || sniffed == "application/ogg"
	case "video":
		return strings.HasPrefix(sniffed, "video/")
	case "document":
		switch sniffed {
		case "application/pdf", "text/plain", "text/xml", "application/xml", "application/rtf", "application/zip",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"application/vnd.ms-office":
			return true
		default:
			return strings.HasPrefix(sniffed, "image/") || strings.HasPrefix(sniffed, "audio/") || strings.HasPrefix(sniffed, "video/") || sniffed == "application/ogg"
		}
	default:
		return false
	}
}

func mimeCompatible(declared, sniffed string) bool {
	declared = canonicalMIME(declared)
	sniffed = canonicalMIME(sniffed)
	if declared == sniffed {
		return true
	}
	switch declared {
	case "image/jpg":
		return sniffed == "image/jpeg"
	case "audio/ogg", "audio/opus":
		return sniffed == "application/ogg" || sniffed == "audio/ogg"
	case "audio/wav", "audio/x-wav":
		return sniffed == "audio/wave" || sniffed == "audio/wav"
	case "audio/mp4":
		return sniffed == "audio/mp4" || sniffed == "video/mp4"
	case "application/octet-stream":
		return true
	case "application/msword", "application/vnd.ms-excel", "application/vnd.ms-powerpoint":
		return sniffed == "application/vnd.ms-office"
	case "application/zip":
		return sniffed == "application/zip" || strings.HasPrefix(sniffed, "application/vnd.openxmlformats-officedocument.")
	case "text/rtf":
		return sniffed == "application/rtf"
	default:
		return false
	}
}

func canonicalMIME(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return value
}
