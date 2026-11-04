package media

import "testing"

func TestInspectInbound(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0x00}
	sniffed, sum, code := InspectInbound("image", "image/jpeg", jpeg)
	if code != "" || sniffed != "image/jpeg" || sum == "" {
		t.Fatalf("jpeg: %s %s %s", sniffed, sum, code)
	}
	if _, _, code := InspectInbound("image", "image/png", jpeg); code != ErrCodeMIME {
		t.Fatalf("divergente: %s", code)
	}
	big := make([]byte, Limit("sticker")+1)
	copy(big, []byte("RIFF"))
	if _, _, code := InspectInbound("sticker", "image/webp", big); code != ErrCodeSize {
		t.Fatalf("limite: %s", code)
	}
	if _, _, code := InspectInbound("image", "image/jpeg", []byte("texto simples")); code != ErrCodeUnknown {
		t.Fatalf("desconhecido: %s", code)
	}
	oggDeclared, _, code := InspectInbound("voice", "audio/ogg; codecs=opus", []byte("OggS\x00audio"))
	if code != "" || oggDeclared != "application/ogg" {
		t.Fatalf("ogg: %s %s", oggDeclared, code)
	}
	plain := []byte("Contrato de servico\n")
	sniffed, _, code = InspectInbound("document", "text/plain", plain)
	if code != "" || sniffed != "text/plain" {
		t.Fatalf("texto: %s %s", sniffed, code)
	}
	office := append([]byte("PK\x03\x04"), []byte("word/document.xml")...)
	sniffed, _, code = InspectInbound("document", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", office)
	if code != "" || sniffed != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("office: %s %s", sniffed, code)
	}
	if _, _, code = InspectInbound("document", "application/pdf", plain); code != ErrCodeMIME {
		t.Fatalf("documento divergente: %s", code)
	}
	if _, _, code = InspectInbound("document", "text/html", []byte("<html><script>alert(1)</script></html>")); code != ErrCodeUnknown {
		t.Fatalf("html recusado: %s", code)
	}
	audio := audioOnlyMP4("isom")
	sniffed, _, code = InspectInbound("audio", "audio/mp4", audio)
	if code != "" || sniffed != "audio/mp4" {
		t.Fatalf("aac isom: %s %s", sniffed, code)
	}
	if _, _, code = InspectInbound("voice", "audio/mp4", audio); code != "" {
		t.Fatalf("voice aac: %s", code)
	}
	if _, _, code = InspectInbound("audio", "audio/mp4", videoOnlyMP4("isom")); code != ErrCodeUnknown {
		t.Fatalf("vídeo como áudio: %s", code)
	}
	if _, _, code = InspectInbound("audio", "audio/mp4", videoOnlyMP4("M4A ")); code != ErrCodeUnknown {
		t.Fatalf("vídeo com marca M4A: %s", code)
	}
	if sniffed, _, code = InspectInbound("video", "video/mp4", videoOnlyMP4("isom")); code != "" || sniffed != "video/mp4" {
		t.Fatalf("vídeo: %s %s", sniffed, code)
	}
	brandOnly := box("ftyp", []byte("M4A \x00\x00\x02\x00"))
	if sniffed, _, code = InspectInbound("audio", "audio/mp4", brandOnly); code != "" || sniffed != "audio/mp4" {
		t.Fatalf("marca M4A: %s %s", sniffed, code)
	}
	if _, _, code = InspectInbound("audio", "audio/mp4", box("ftyp", []byte("isom\x00\x00\x00\x00"))); code != ErrCodeUnknown {
		t.Fatalf("isom sem faixa: %s", code)
	}
}

func audioOnlyMP4(brand string) []byte {
	return mp4WithTrack(brand, "soun", "mp4a")
}

func videoOnlyMP4(brand string) []byte {
	return mp4WithTrack(brand, "vide", "avc1")
}

func mp4WithTrack(brand, handler, sample string) []byte {
	ftyp := box("ftyp", append([]byte(brand), []byte{0, 0, 0, 0}...))
	hdlrBody := make([]byte, 25)
	copy(hdlrBody[8:12], handler)
	hdlr := box("hdlr", hdlrBody)
	sampleEntry := box(sample, make([]byte, 28))
	stsd := box("stsd", append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, sampleEntry...))
	stbl := box("stbl", stsd)
	minf := box("minf", stbl)
	mdia := box("mdia", append(hdlr, minf...))
	trak := box("trak", mdia)
	moov := box("moov", trak)
	return append(ftyp, moov...)
}
