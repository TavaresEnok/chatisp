package branding

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestNormalizeAcceptsPNGAndJPEGAndStripsTrailingMarkup(t *testing.T) {
	pngRaw := pngBytes(8, 4)
	pngRaw = append(append([]byte{}, pngRaw...), []byte("<script>alert(1)</script>")...)
	got, err := Normalize("image/png; charset=binary", pngRaw)
	if err != nil || got.ContentType != "image/png" || got.Width != 8 || got.Height != 4 || !Matches(got.ContentType, got.Bytes) {
		t.Fatalf("png: %+v %v", got, err)
	}
	if bytes.Contains(got.Bytes, []byte("<script>")) || bytes.Contains(bytes.ToLower(got.Bytes), []byte("<svg")) {
		t.Fatal("a reencodificação guardou marcação")
	}
	jpg, err := Normalize("image/jpeg", jpegBytes(3, 2))
	if err != nil || jpg.ContentType != "image/jpeg" || jpg.Width != 3 || jpg.Height != 2 || !Matches(jpg.ContentType, jpg.Bytes) {
		t.Fatalf("jpeg: %+v %v", jpg, err)
	}
}

func TestNormalizeRejectsActiveContentMisleadingTypeAndLimits(t *testing.T) {
	pngRaw := pngBytes(2, 2)
	jpegRaw := jpegBytes(2, 2)
	cases := []struct {
		name string
		kind string
		raw  []byte
	}{
		{"svg", "image/svg+xml", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")},
		{"svg com tipo png", "image/png", []byte("<svg></svg>")},
		{"html", "text/html", []byte("<html><script>alert(1)</script></html>")},
		{"url", "image/png", []byte("https://cdn.example/logo.png")},
		{"url protocol-relative", "image/jpeg", []byte("//cdn.example/logo.png")},
		{"data url", "image/png", []byte("data:image/png;base64,aaaa")},
		{"mime falso", "image/png", jpegRaw},
		{"mime jpeg em png", "image/jpeg", pngRaw},
		{"imagem inválida", "image/png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 1, 2}},
		{"dimensão", "image/png", pngBytes(MaxLogoEdge+1, 1)},
		{"altura", "image/jpeg", jpegBytes(1, MaxLogoEdge+1)},
		{"bytes", "image/png", append(pngRaw, bytes.Repeat([]byte{0}, MaxLogoBytes)...)},
		{"vazio", "image/png", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Normalize(tc.kind, tc.raw); err == nil || !strings.Contains(err.Error(), "inválidos") {
				t.Fatalf("aceitou %s: %v", tc.name, err)
			}
		})
	}
}

func pngBytes(width, height int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func jpegBytes(width, height int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 10, B: 10, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
