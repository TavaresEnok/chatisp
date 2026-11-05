package attachments

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// TestS3ContractAgainstRealService runs the storage contract against a real
// S3-compatible endpoint. It is skipped unless CHATISP_S3_CONTRACT_ENDPOINT is
// set (with _REGION, _BUCKET, _ACCESS_KEY, _SECRET_KEY, optional _CA_FILE,
// _UNVERSIONED=true and _PUBLIC_ENDPOINT). Use synthetic bytes only.
func TestS3ContractAgainstRealService(t *testing.T) {
	endpoint := os.Getenv("CHATISP_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("CHATISP_S3_CONTRACT_ENDPOINT ausente: contrato contra S3 real não executado")
	}
	cfg := S3Config{
		Endpoint: endpoint, Region: os.Getenv("CHATISP_S3_CONTRACT_REGION"), Bucket: os.Getenv("CHATISP_S3_CONTRACT_BUCKET"),
		AccessKey: os.Getenv("CHATISP_S3_CONTRACT_ACCESS_KEY"), SecretKey: os.Getenv("CHATISP_S3_CONTRACT_SECRET_KEY"),
		PathStyle: true, Unversioned: os.Getenv("CHATISP_S3_CONTRACT_UNVERSIONED") == "true",
		CAFile: os.Getenv("CHATISP_S3_CONTRACT_CA_FILE"), PublicEndpoint: os.Getenv("CHATISP_S3_CONTRACT_PUBLIC_ENDPOINT"),
	}
	st, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("configuração: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	key := "contrato-" + uuid.NewString() + "/" + uuid.NewString()
	payload := []byte("bytes sintéticos do contrato " + uuid.NewString())
	version, err := st.Create(ctx, key, bytes.NewReader(payload), int64(len(payload)))
	if err != nil || version == "" {
		t.Fatalf("criação: %q %v", version, err)
	}
	t.Cleanup(func() { _ = st.Delete(context.Background(), key, version) })
	if _, err := st.Create(ctx, key, bytes.NewReader([]byte("outra coisa")), 11); err == nil {
		t.Fatal("criação sobrescreveu uma chave existente")
	}
	reader, err := st.Read(ctx, key, version)
	if err != nil {
		t.Fatalf("leitura: %v", err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("bytes lidos diferem: %q", got)
	}
	if _, err := st.Read(ctx, key, etagVersionPrefix+"00000000000000000000000000000000"); err == nil && cfg.Unversioned {
		t.Fatal("leitura aceitou identidade de objeto errada")
	}

	link, err := st.Sign(ctx, key, version, "contrato.txt", DownloadTTL)
	if err != nil {
		t.Fatalf("assinatura: %v", err)
	}
	if strings.Contains(link, cfg.SecretKey) {
		t.Fatal("link carrega o segredo")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	resp, err := client.Get(link)
	if err != nil {
		t.Fatalf("download assinado: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, payload) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download assinado: %d cd=%q cc=%q", resp.StatusCode, resp.Header.Get("Content-Disposition"), resp.Header.Get("Cache-Control"))
	}
	tampered := strings.Replace(link, "X-Amz-Signature=", "X-Amz-Signature=0", 1)
	if resp, err := client.Get(tampered); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("assinatura adulterada foi aceita")
		}
	}
	unsigned := strings.SplitN(link, "?", 2)[0]
	if resp, err := client.Get(unsigned); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("objeto acessível sem assinatura (bucket público)")
		}
	}

	if err := st.Delete(ctx, key, version); err != nil {
		t.Fatalf("exclusão: %v", err)
	}
	if _, err := st.Read(ctx, key, version); err == nil {
		t.Fatal("objeto excluído ainda legível")
	}
}

// Unversioned mode never sends versionId and refuses an existing key.
func TestS3UnversionedModeWithSimulatedTransport(t *testing.T) {
	var methods []string
	exists := false
	s, e := newS3(S3Config{Endpoint: "https://storage.example", PublicEndpoint: "https://arquivos.example:8443", Region: "garage", Bucket: "private", AccessKey: "FAKE-KEY", SecretKey: "FAKE-SECRET", PathStyle: true, Unversioned: true}, simulatedTransport(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		if r.URL.Query().Has("versionId") {
			t.Fatalf("versionId enviado no modo sem versionamento: %s", r.URL)
		}
		h := http.Header{"Etag": []string{`"abc123"`}}
		switch r.Method {
		case http.MethodHead:
			if exists {
				return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		case http.MethodPut:
			exists = true
		case http.MethodGet:
			if r.Header.Get("If-Match") != `"abc123"` {
				t.Fatalf("leitura sem If-Match: %q", r.Header.Get("If-Match"))
			}
			return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("bytes")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	v, e := s.Create(ctx, "ns/obj", bytes.NewReader([]byte("bytes")), 5)
	if e != nil || v != "etag:abc123" {
		t.Fatalf("criação: %q %v", v, e)
	}
	if _, e := s.Create(ctx, "ns/obj", bytes.NewReader([]byte("bytes")), 5); e == nil {
		t.Fatal("chave existente sobrescrita")
	}
	r, e := s.Read(ctx, "ns/obj", v)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	link, e := s.Sign(ctx, "ns/obj", v, "a.pdf", DownloadTTL)
	if e != nil || !strings.HasPrefix(link, "https://arquivos.example:8443/private/ns/obj?") || strings.Contains(link, "versionId") {
		t.Fatalf("link: %q %v", link, e)
	}
	if e := s.Delete(ctx, "ns/obj", v); e != nil {
		t.Fatal(e)
	}
	if _, e := newS3(S3Config{Endpoint: "https://storage.example", PublicEndpoint: "http://inseguro.example", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}, nil); e == nil {
		t.Fatal("endpoint público sem TLS aceito")
	}
	if _, e := NewS3(S3Config{Endpoint: "https://storage.example", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s", CAFile: "/nao/existe.pem"}); e == nil {
		t.Fatal("CA ausente aceita")
	}
	_ = aws.String
	_ = s3.GetObjectInput{}
}
