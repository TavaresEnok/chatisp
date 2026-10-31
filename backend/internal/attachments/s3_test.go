package attachments

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type simulatedTransport func(*http.Request) (*http.Response, error)

func (f simulatedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// SDK serialization/signature evidence ONLY: transport never reaches S3.
func TestS3SDKWithExplicitlySimulatedTransport(t *testing.T) {
	calls := 0
	s, e := newS3(S3Config{Endpoint: "https://storage.example", Region: "test-1", Bucket: "private", AccessKey: "FAKE-KEY", SecretKey: "FAKE-SECRET", PathStyle: true}, simulatedTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Fatal("missing SDK signature")
		}
		body := ""
		switch r.Method {
		case "PUT":
			if r.Header.Get("If-None-Match") != "*" || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatal("unsafe put")
			}
		case "GET":
			if r.URL.Query().Get("versionId") != "immutable-v1" {
				t.Fatal("unversioned read")
			}
			body = "bytes"
		case "DELETE":
			if r.URL.Query().Get("versionId") != "immutable-v1" {
				t.Fatal("unversioned deletion")
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Amz-Version-Id": []string{"immutable-v1"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	v, e := s.Create(ctx, "opaque/object", bytes.NewReader([]byte("bytes")), 5)
	if e != nil || v != "immutable-v1" {
		t.Fatalf("create %q %v", v, e)
	}
	r, e := s.Read(ctx, "opaque/object", v)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	link, e := s.Sign(ctx, "opaque/object", v, "document.pdf", DownloadTTL)
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(link)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	if q.Get("X-Amz-Expires") != "60" || q.Get("versionId") != v || q.Get("response-content-type") != "application/octet-stream" || !strings.HasPrefix(q.Get("response-content-disposition"), "attachment;") || q.Get("X-Amz-Signature") == "" {
		t.Fatal("unsafe signed contract")
	}
	if _, e = s.Sign(ctx, "opaque/object", v, "x", time.Hour); e == nil {
		t.Fatal("long URL allowed")
	}
	if e = s.Delete(ctx, "opaque/object", v); e != nil {
		t.Fatal(e)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if _, e = NewS3(S3Config{Endpoint: "https://storage.example", Region: "test", Bucket: "private"}); e == nil {
		t.Fatal("missing credentials accepted")
	}
}
func TestTypeAndNameValidation(t *testing.T) {
	for _, b := range [][]byte{[]byte("<svg onload=alert(1)>"), []byte("<html>not an image</html>"), []byte("MZbinary"), []byte("PK\x03\x04macro")} {
		if _, _, e := inspect(b, "outgoing"); e == nil {
			t.Fatal("active/unknown content accepted")
		}
	}
	for _, n := range []string{"../file.pdf", "a\\b", "x\r\nInjected: yes", "\u202efile.pdf", ""} {
		if _, e := safeName(n); e == nil {
			t.Fatal("unsafe filename accepted")
		}
	}
	if typ, hash, e := inspect([]byte("%PDF-1.7\n%test\n"), "outgoing"); e != nil || typ != "application/pdf" || len(hash) != 64 {
		t.Fatal(typ, e)
	}
	mp4 := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}
	if _, _, e := inspect(mp4, "outgoing"); e == nil {
		t.Fatal("outgoing video enabled")
	}
	if typ, _, e := inspect(mp4, "incoming"); e != nil || typ != "video/mp4" {
		t.Fatal("incoming video", typ, e)
	}
}
