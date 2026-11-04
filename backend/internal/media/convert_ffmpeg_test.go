package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestTranscodeRecordedWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	wav := wavFixture(8000, 1, 16, 8000)
	webm := ffmpegEncode(t, wav, "webm")
	ogg := ffmpegEncode(t, wav, "ogg")
	for _, in := range []struct {
		mime string
		body []byte
	}{
		{"video/webm", webm},
		{"audio/ogg", ogg},
	} {
		out, err := TranscodeRecorded(context.Background(), in.mime, in.body)
		if err != nil {
			t.Fatalf("%s: %v", in.mime, err)
		}
		if !bytes.HasPrefix(out, []byte("OggS")) || !bytes.Contains(out, []byte("OpusHead")) {
			t.Fatalf("%s não saiu OGG/Opus", in.mime)
		}
		if len(out) == 0 || len(out) > convertOutput {
			t.Fatalf("%s tamanho %d", in.mime, len(out))
		}
	}
}

func TestParseProbeDurationUsesDecodedClock(t *testing.T) {
	undeclared := "Duration: N/A, bitrate: N/A\nsize=N/A time=00:02:01.02 bitrate=N/A speed=1x\n"
	ms, ok := ParseProbeDuration(undeclared)
	if !ok || ms != 121020 {
		t.Fatalf("sem declaração: %d %v", ms, ok)
	}
	declared := "Duration: 00:00:01.50, start: 0.000000\nsize=N/A time=00:00:01.00 bitrate=N/A\n"
	ms, ok = ParseProbeDuration(declared)
	if !ok || ms != 1500 {
		t.Fatalf("declarada: %d %v", ms, ok)
	}
}

func TestStripWebMDurationDropsDeclaration(t *testing.T) {
	raw := webmDeclared(121000)
	ms, ok := ContainerDuration("audio/webm", raw)
	if !ok || ms != 121000 {
		t.Fatalf("fixture %d %v", ms, ok)
	}
	stripped, ok := stripWebMDuration(raw)
	if !ok {
		t.Fatal("não removeu Duration")
	}
	if _, ok = ContainerDuration("audio/webm", stripped); ok {
		t.Fatal("duração ainda declarada")
	}
}

func TestTranscodeRecordedRefusesUnderdeclaredWebM(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	raw := ffmpegSilence(t, 121, "webm")
	declared, ok := ContainerDuration("audio/webm", raw)
	if !ok || declared <= convertDuration.Milliseconds() {
		t.Fatalf("fixture real deveria declarar acima do limite: %d %v", declared, ok)
	}
	lied := rewriteWebMDuration(t, raw, 1000)
	got, ok := ContainerDuration("audio/webm", lied)
	if !ok || got <= 0 || got > 5000 {
		t.Fatalf("duração subdeclarada não ficou em 1s: %d %v", got, ok)
	}
	if _, err := TranscodeRecorded(context.Background(), "audio/webm", lied); !errors.Is(err, ErrConvert) {
		t.Fatalf("webm com Duration de 1s e áudio acima de 120s: %v", err)
	}
}

func TestTranscodeRecordedRefusesRealUndeclaredOverLimit(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente; conversão real não executada neste runner")
	}
	ogg := ffmpegSilence(t, 121, "ogg")
	if _, ok := ContainerDuration("audio/ogg", ogg); ok {
		t.Fatal("ogg sintético não deveria ser lido por ContainerDuration")
	}
	if _, err := TranscodeRecorded(context.Background(), "audio/ogg", ogg); !errors.Is(err, ErrConvert) {
		t.Fatalf("ogg acima de 120s: %v", err)
	}
	webm := stripRequired(t, ffmpegSilence(t, 121, "webm"))
	if _, ok := ContainerDuration("video/webm", webm); ok {
		t.Fatal("webm ainda declara duração")
	}
	if _, err := TranscodeRecorded(context.Background(), "video/webm", webm); !errors.Is(err, ErrConvert) {
		t.Fatalf("webm sem duração acima de 120s: %v", err)
	}
	short := stripRequired(t, ffmpegSilence(t, 1, "webm"))
	out, err := TranscodeRecorded(context.Background(), "audio/webm", short)
	if err != nil || !bytes.HasPrefix(out, []byte("OggS")) || !bytes.Contains(out, []byte("OpusHead")) {
		t.Fatalf("webm curto sem duração: %v %d", err, len(out))
	}
}

func TestTranscodeRecordedRefusesOverLimitBeforeBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	over := webmDeclared(121000)
	if _, err := TranscodeRecorded(context.Background(), "audio/webm", over); !errors.Is(err, ErrConvert) {
		t.Fatalf("duração: %v", err)
	}
	exact := webmDeclared(120000)
	if _, err := TranscodeRecorded(context.Background(), "audio/webm", exact); !errors.Is(err, ErrFFmpegMissing) || err.Error() != FFmpegMissingDetail {
		t.Fatalf("limite exato: %v", err)
	}
	big := make([]byte, convertOutput+1)
	if _, err := TranscodeRecorded(context.Background(), "audio/webm", big); !errors.Is(err, ErrConvert) {
		t.Fatalf("tamanho: %v", err)
	}
	small := wavFixture(8000, 1, 16, 800)
	if _, err := TranscodeRecorded(context.Background(), "audio/wave", small); !errors.Is(err, ErrFFmpegMissing) || err.Error() != FFmpegMissingDetail {
		t.Fatalf("sem binário: %v", err)
	}
}

func ffmpegSilence(t *testing.T, seconds int, format string) []byte {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out."+format)
	var last []byte
	var lastErr error
	for _, spec := range []string{"anullsrc=r=8000:cl=mono", "anullsrc=channel_layout=mono:sample_rate=8000"} {
		cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-y", "-f", "lavfi", "-i", spec,
			"-t", strconv.Itoa(seconds), "-c:a", "libopus", "-b:a", "6k", "-f", format, out)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir()}
		msg, err := cmd.CombinedOutput()
		if err == nil {
			body, readErr := os.ReadFile(out)
			if readErr != nil || len(body) == 0 {
				t.Fatalf("saída %s: %v", format, readErr)
			}
			return body
		}
		last, lastErr = msg, err
	}
	t.Fatalf("gerar %s de %ds: %v %s", format, seconds, lastErr, last)
	return nil
}

func rewriteWebMDuration(t *testing.T, raw []byte, units float64) []byte {
	t.Helper()
	header, total, ok := findEBML(raw, 0, ebmlDuration)
	if !ok || header < 0 || total <= 0 || header+total > len(raw) {
		t.Fatal("fixture webm sem Duration")
	}
	_, idn, idok := readID(raw[header:])
	size, sn, unknown, sok := elementSize(raw[header+idn:])
	if !idok || !sok || unknown || (size != 4 && size != 8) {
		t.Fatal("Duration sem payload numérico")
	}
	start := header + idn + sn
	if start+int(size) > len(raw) {
		t.Fatal("Duration fora do arquivo")
	}
	out := append([]byte(nil), raw...)
	if size == 4 {
		binary.BigEndian.PutUint32(out[start:], math.Float32bits(float32(units)))
	} else {
		binary.BigEndian.PutUint64(out[start:], math.Float64bits(units))
	}
	return out
}

func stripRequired(t *testing.T, raw []byte) []byte {
	t.Helper()
	stripped, ok := stripWebMDuration(raw)
	if !ok {
		t.Fatal("fixture webm sem elemento Duration removível")
	}
	return stripped
}

func stripWebMDuration(b []byte) ([]byte, bool) {
	header, total, ok := findEBML(b, 0, ebmlDuration)
	if !ok || total <= 0 || header+total > len(b) {
		return nil, false
	}
	out := make([]byte, 0, len(b)-total)
	out = append(out, b[:header]...)
	out = append(out, b[header+total:]...)
	if !shrinkAncestors(out, b, header, total) {
		return nil, false
	}
	return out, true
}

func findEBML(b []byte, base int, want uint64) (header, total int, ok bool) {
	off := 0
	for off < len(b) {
		id, idn, idok := readID(b[off:])
		if !idok {
			return 0, 0, false
		}
		size, sn, unknown, sok := elementSize(b[off+idn:])
		if !sok {
			return 0, 0, false
		}
		width := idn + sn
		if unknown {
			size = uint64(len(b) - off - width)
		}
		if size > uint64(len(b)-off-width) {
			return 0, 0, false
		}
		body := b[off+width : off+width+int(size)]
		if id == want {
			return base + off, width + int(size), true
		}
		if h, n, found := findEBML(body, base+off+width, want); found {
			return h, n, true
		}
		if unknown {
			break
		}
		off += width + int(size)
	}
	return 0, 0, false
}

func shrinkAncestors(out, original []byte, header, cut int) bool {
	return walkShrink(original, 0, header, cut, out)
}

func walkShrink(b []byte, base, header, cut int, out []byte) bool {
	off := 0
	for off < len(b) {
		_, idn, idok := readID(b[off:])
		if !idok {
			return true
		}
		size, sn, unknown, sok := elementSize(b[off+idn:])
		if !sok {
			return true
		}
		width := idn + sn
		if unknown {
			size = uint64(len(b) - off - width)
		}
		if off+width > len(b) || size > uint64(len(b)-off-width) {
			return false
		}
		dataStart := base + off + width
		dataEnd := dataStart + int(size)
		if header >= dataStart && header < dataEnd {
			if !unknown {
				if size < uint64(cut) || base+off+width > len(out) {
					return false
				}
				if !writeVint(out[base+off+idn:base+off+width], sn, size-uint64(cut)) {
					return false
				}
			}
			if !walkShrink(b[off+width:off+width+int(size)], dataStart, header, cut, out) {
				return false
			}
		}
		if unknown {
			return true
		}
		off += width + int(size)
	}
	return true
}

func elementSize(b []byte) (size uint64, width int, unknown, ok bool) {
	if width, yes := unknownSize(b); yes {
		return 0, width, true, true
	}
	size, width, ok = readDataSize(b)
	return size, width, false, ok
}

func unknownSize(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	width := vintWidth(b[0])
	if width == 0 || len(b) < width {
		return 0, false
	}
	mask := byte(0x80 >> (width - 1))
	if b[0]&(mask-1) != mask-1 {
		return 0, false
	}
	for i := 1; i < width; i++ {
		if b[i] != 0xff {
			return 0, false
		}
	}
	return width, true
}

func writeVint(dst []byte, width int, value uint64) bool {
	if width < 1 || width > 8 || len(dst) < width {
		return false
	}
	if value>>(7*uint(width)) != 0 {
		return false
	}
	for i := width - 1; i >= 0; i-- {
		dst[i] = byte(value)
		value >>= 8
	}
	dst[0] |= 0x80 >> (width - 1)
	return true
}

func ffmpegEncode(t *testing.T, wav []byte, format string) []byte {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	out := filepath.Join(dir, "out."+format)
	if err := os.WriteFile(in, wav, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-y", "-i", in, "-c:a", "libopus", "-f", format, out)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir()}
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gerar %s: %v %s", format, err, msg)
	}
	body, err := os.ReadFile(out)
	if err != nil || len(body) == 0 {
		t.Fatalf("saída %s: %v", format, err)
	}
	return body
}

func webmDeclared(ms float64) []byte {
	scale := []byte{0x00, 0x0f, 0x42, 0x40}
	var bits [8]byte
	binary.BigEndian.PutUint64(bits[:], math.Float64bits(ms))
	info := append(ebmlElement(ebmlTimecodeScale, scale), ebmlElement(ebmlDuration, bits[:])...)
	return ebmlElement(ebmlSegment, ebmlElement(ebmlInfo, info))
}
