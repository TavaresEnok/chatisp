package metacloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"chatisp/internal/channel/whatsapp"
)

func TestSendMediaBodyPerKindAndMappedError(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	var lastPath string
	var lastBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastPath = r.URL.Path
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if strings.HasSuffix(r.URL.Path, "/media") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"media-1"}`))
			return
		}
		_ = json.Unmarshal(raw, &lastBody)
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":131026,"message":"Message Undeliverable"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OUT"}]}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	driver := &Driver{endpoint: endpoint{
		mode: ModeOfficial, version: testVersion, phoneID: testPhone, token: testToken,
		base: base, simulation: false, client: server.Client(),
	}}
	kinds := []whatsapp.MediaKind{whatsapp.MediaImage, whatsapp.MediaDocument, whatsapp.MediaAudio, whatsapp.MediaVideo}
	for _, kind := range kinds {
		hits.Store(0)
		lastBody = nil
		req := sampleMedia(kind)
		res, err := driver.SendMedia(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Local != whatsapp.StateAccepted || res.ProviderMessageID != "wamid.OUT" || hits.Load() != 2 {
			t.Fatalf("%s: %#v hits=%d path=%s", kind, res, hits.Load(), lastPath)
		}
		if lastBody["type"] != string(kind) {
			t.Fatalf("tipo %s corpo %#v", kind, lastBody)
		}
		obj, _ := lastBody[string(kind)].(map[string]any)
		if obj["id"] != "media-1" {
			t.Fatalf("id de mídia %#v", obj)
		}
		if kind != whatsapp.MediaAudio && obj["caption"] != "legenda" {
			t.Fatalf("legenda %#v", obj)
		}
		if kind == whatsapp.MediaDocument && obj["filename"] != "boleto.pdf" {
			t.Fatalf("arquivo %#v", obj)
		}
		ctxObj, _ := lastBody["context"].(map[string]any)
		if ctxObj["message_id"] != "wamid.CITADO" {
			t.Fatalf("citação %#v", lastBody["context"])
		}
	}
	fail.Store(true)
	res, err := driver.SendMedia(context.Background(), sampleMedia(whatsapp.MediaImage))
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateFailed || res.RejectionCode != "graph_131026" {
		t.Fatalf("erro remoto %#v", res)
	}
}

func TestSendMediaSimulationDoesNotCallHTTP(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "não deveria", http.StatusInternalServerError)
	}))
	defer server.Close()
	driver, err := Activate(Config{
		Mode: ModeSimulation, GraphVersion: testVersion, PhoneNumberID: testPhone,
		AccessToken: testToken, BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := driver.SendMedia(context.Background(), sampleMedia(whatsapp.MediaImage))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Simulation || res.Local != whatsapp.StateAccepted || res.ProviderMessageID == "" || hits.Load() != 0 {
		t.Fatalf("simulação %#v hits=%d", res, hits.Load())
	}
}

func TestSendMediaURLGuardRejectsPrivateLink(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.LINK"}]}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	driver := &Driver{endpoint: endpoint{
		mode: ModeOfficial, version: testVersion, phoneID: testPhone, token: testToken,
		base: base, simulation: false, client: server.Client(),
	}}
	for _, link := range []string{
		"http://127.0.0.1/segredo.jpg",
		"http://169.254.169.254/latest",
		"https://user:pass@example.com/a.jpg",
		"http://example.com/a.jpg",
	} {
		req := sampleMedia(whatsapp.MediaImage)
		req.Body = nil
		req.Link = link
		res, err := driver.SendMedia(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Local != whatsapp.StateFailed || res.RejectionCode != "url_recusada" || hits.Load() != 0 {
			t.Fatalf("link %s %#v hits=%d", link, res, hits.Load())
		}
	}
	req := sampleMedia(whatsapp.MediaImage)
	req.Body = nil
	req.Link = "https://example.com/foto.jpg"
	res, err := driver.SendMedia(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Local != whatsapp.StateAccepted || hits.Load() != 1 {
		t.Fatalf("link público %#v hits=%d", res, hits.Load())
	}
}

func TestLoopbackOfficialRejectsRemoteHosts(t *testing.T) {
	if _, err := LoopbackOfficial("https://graph.facebook.com", testVersion, testPhone, testToken, nil); err == nil {
		t.Fatal("aceitou a Graph")
	}
	if _, err := LoopbackOfficial("https://example.com/v1", testVersion, testPhone, testToken, nil); err == nil {
		t.Fatal("aceitou host remoto")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	driver, err := LoopbackOfficial(server.URL, testVersion, testPhone, testToken, server.Client())
	if err != nil || driver == nil || driver.endpoint.simulation || driver.endpoint.base.Host == "graph.facebook.com" {
		t.Fatalf("loopback: %v %#v", err, driver)
	}
}

func TestMediaMultipartAudioOggUsesGraphMIME(t *testing.T) {
	req := sampleMedia(whatsapp.MediaAudio)
	req.MIME = "application/ogg"
	req.FileName = "audio.ogg"
	req.Body = []byte("OggSopus")
	assertMultipartMIME(t, req, "audio/ogg")

	req.MIME = "video/ogg"
	assertMultipartMIME(t, req, "audio/ogg")

	req.MIME = "audio/ogg"
	assertMultipartMIME(t, req, "audio/ogg")

	req.MIME = "audio/mpeg"
	req.FileName = "nota.mp3"
	assertMultipartMIME(t, req, "audio/mpeg")

	image := sampleMedia(whatsapp.MediaImage)
	image.MIME = "application/ogg"
	assertMultipartMIME(t, image, "application/ogg")
}

func assertMultipartMIME(t *testing.T, req whatsapp.MediaRequest, want string) {
	t.Helper()
	body, contentType, err := mediaMultipart(req)
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || params["boundary"] == "" {
		t.Fatalf("content-type %s: %v", contentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var sawType, sawFile bool
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		switch part.FormName() {
		case "type":
			sawType = true
			if string(raw) != want {
				t.Fatalf("type %q, esperado %s", raw, want)
			}
		case "file":
			sawFile = true
			if part.Header.Get("Content-Type") != want {
				t.Fatalf("Content-Type %s, esperado %s", part.Header.Get("Content-Type"), want)
			}
		}
	}
	if !sawType || !sawFile {
		t.Fatal("parte type ou file ausente")
	}
}

func sampleMedia(kind whatsapp.MediaKind) whatsapp.MediaRequest {
	name := ""
	if kind == whatsapp.MediaDocument {
		name = "boleto.pdf"
	}
	return whatsapp.MediaRequest{
		AttemptID: "attempt-1", ConnectionID: "connection-1", ConnectorType: whatsapp.ConnectorMetaCloud,
		RecipientID: "16505551234", CallbackData: "callback-opaco", Kind: kind,
		AttachmentID: "att-1", Caption: "legenda", ReplyToProviderID: "wamid.CITADO",
		MIME: "application/octet-stream", FileName: name, Body: []byte("bytes"),
	}
}
