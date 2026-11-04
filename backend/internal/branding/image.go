package branding

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"mime"
	"strings"
	"unicode"
)

// Normalize checks the declared media type and the bytes, then re-encodes a
// PNG or JPEG. SVG, HTML, a remote URL, a misleading type, a broken image and
// anything over the byte or pixel limits are refused. The returned bytes are
// the re-encoded image, so trailing markup is not stored.
func Normalize(declared string, raw []byte) (Image, error) {
	if len(raw) == 0 || len(raw) > MaxLogoBytes {
		return Image{}, ErrInvalid
	}
	if looksActive(raw) || looksRemote(raw) {
		return Image{}, ErrInvalid
	}
	media, _, err := mime.ParseMediaType(strings.TrimSpace(declared))
	if err != nil {
		return Image{}, ErrInvalid
	}
	var want string
	switch media {
	case "image/png":
		want = "png"
	case "image/jpeg":
		want = "jpeg"
	default:
		return Image{}, ErrInvalid
	}
	if sniff(raw) != media {
		return Image{}, ErrInvalid
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != want || !edgeOK(cfg.Width, cfg.Height) {
		return Image{}, ErrInvalid
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil || format != want {
		return Image{}, ErrInvalid
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width != cfg.Width || height != cfg.Height || !edgeOK(width, height) {
		return Image{}, ErrInvalid
	}
	var buf bytes.Buffer
	switch want {
	case "png":
		err = png.Encode(&buf, img)
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	if err != nil || buf.Len() == 0 || buf.Len() > MaxLogoBytes || sniff(buf.Bytes()) != media {
		return Image{}, ErrInvalid
	}
	return Image{ContentType: media, Bytes: buf.Bytes(), Width: width, Height: height}, nil
}

// Matches reports whether raw has the magic bytes of contentType.
func Matches(contentType string, raw []byte) bool {
	return sniff(raw) == contentType && (contentType == "image/png" || contentType == "image/jpeg")
}

func edgeOK(width, height int) bool {
	return width >= 1 && height >= 1 && width <= MaxLogoEdge && height <= MaxLogoEdge
}

func sniff(raw []byte) string {
	if len(raw) >= 8 && bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png"
	}
	if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		return "image/jpeg"
	}
	return ""
}

func looksActive(raw []byte) bool {
	text := strings.TrimLeftFunc(stripBOM(raw), unicode.IsSpace)
	lower := strings.ToLower(text)
	for _, prefix := range []string{"<svg", "<html", "<!doctype", "<?xml", "<script", "<iframe"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksRemote(raw []byte) bool {
	text := strings.TrimSpace(stripBOM(raw))
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "data:")
}

func stripBOM(raw []byte) string {
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		raw = raw[3:]
	}
	return string(raw)
}
