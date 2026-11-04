package media

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestWAVDurationFromDeclaredByteRate(t *testing.T) {
	b := wavFixture(8000, 1, 8, 8000)
	ms, ok := ContainerDuration("audio/wave", b)
	if !ok || ms != 1000 {
		t.Fatalf("duration=%d ok=%v", ms, ok)
	}
}

func TestWAVWithoutDataIsAbsent(t *testing.T) {
	b := wavFixture(8000, 1, 8, 0)
	if _, ok := ContainerDuration("audio/wave", b); ok {
		t.Fatal("duração sem chunk data")
	}
}

func TestMP4MovieHeader(t *testing.T) {
	mvhd := make([]byte, 24)
	mvhd[0] = 0
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 2500)
	moov := box("mvhd", mvhd)
	file := append(box("ftyp", []byte("M4A M4A ")), box("moov", moov)...)
	ms, ok := ContainerDuration("audio/mp4", file)
	if !ok || ms != 2500 {
		t.Fatalf("duration=%d ok=%v", ms, ok)
	}
}

func TestMP4ZeroDurationIsAbsent(t *testing.T) {
	mvhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	file := box("moov", box("mvhd", mvhd))
	if _, ok := ContainerDuration("video/mp4", file); ok {
		t.Fatal("duração zero não é declaração")
	}
}

func TestID3TLEN(t *testing.T) {
	text := append([]byte{0}, []byte("1500")...)
	frame := append([]byte("TLEN"), synchsafeBytes(len(text))...)
	frame = append(frame, 0, 0)
	frame = append(frame, text...)
	body := frame
	header := []byte{'I', 'D', '3', 3, 0, 0}
	header = append(header, synchsafeBytes(len(body))...)
	file := append(header, body...)
	file = append(file, 0xff, 0xfb, 0x90, 0x00)
	ms, ok := ContainerDuration("audio/mpeg", file)
	if !ok || ms != 1500 {
		t.Fatalf("duration=%d ok=%v", ms, ok)
	}
}

func TestMPEGWithoutTLENIsAbsent(t *testing.T) {
	if _, ok := ContainerDuration("audio/mpeg", []byte{0xff, 0xfb, 0x90, 0x00}); ok {
		t.Fatal("frame mpeg não declara duração")
	}
}

func TestOggWithoutExplicitDurationIsAbsent(t *testing.T) {
	b := append([]byte("OggS"), make([]byte, 32)...)
	if _, ok := ContainerDuration("application/ogg", b); ok {
		t.Fatal("ogg sem campo de duração")
	}
}

func TestWebMDeclaredDuration(t *testing.T) {
	scale := []byte{0x00, 0x0f, 0x42, 0x40}
	var bits [8]byte
	binary.BigEndian.PutUint64(bits[:], math.Float64bits(1500))
	info := append(ebmlElement(ebmlTimecodeScale, scale), ebmlElement(ebmlDuration, bits[:])...)
	segment := ebmlElement(ebmlInfo, info)
	file := ebmlElement(ebmlSegment, segment)
	ms, ok := ContainerDuration("audio/webm", file)
	if !ok || ms != 1500 {
		t.Fatalf("duration=%d ok=%v", ms, ok)
	}
}

func TestImageHasNoDuration(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	if _, ok := ContainerDuration("image/png", png); ok {
		t.Fatal("imagem não tem duração de container")
	}
}

func wavFixture(rate, channels, bits, samples int) []byte {
	dataSize := samples * channels * bits / 8
	byteRate := rate * channels * bits / 8
	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:2], 1)
	binary.LittleEndian.PutUint16(fmtChunk[2:4], uint16(channels))
	binary.LittleEndian.PutUint32(fmtChunk[4:8], uint32(rate))
	binary.LittleEndian.PutUint32(fmtChunk[8:12], uint32(byteRate))
	binary.LittleEndian.PutUint16(fmtChunk[12:14], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(fmtChunk[14:16], uint16(bits))
	out := append([]byte("RIFF"), make([]byte, 4)...)
	out = append(out, []byte("WAVE")...)
	out = append(out, []byte("fmt ")...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, 16)
	out = append(out, size...)
	out = append(out, fmtChunk...)
	out = append(out, []byte("data")...)
	binary.LittleEndian.PutUint32(size, uint32(dataSize))
	out = append(out, size...)
	out = append(out, make([]byte, dataSize)...)
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

func box(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

func synchsafeBytes(n int) []byte {
	return []byte{byte(n >> 21), byte(n >> 14), byte(n >> 7), byte(n)}
}

func ebmlElement(id uint64, payload []byte) []byte {
	idb := binaryID(id)
	return append(append(idb, vintSize(len(payload))...), payload...)
}

func binaryID(id uint64) []byte {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], id)
	i := 0
	for i < 7 && raw[i] == 0 {
		i++
	}
	return append([]byte(nil), raw[i:]...)
}

func vintSize(n int) []byte {
	return []byte{0x80 | byte(n)}
}
