package authmeta

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testSecret  = "app-secret-9c1e-not-the-verify-token"
	testToken   = "verify-token-44ab-not-the-app-secret"
	testMarker  = "msg-cliente-8b21-nao-logar"
	testAppID   = "app-trusted"
	testOtherID = "app-outro"
)

func testBinding(max int64) App {
	return App{
		ID:                testAppID,
		Secret:            testSecret,
		VerificationToken: testToken,
		MaxBodyBytes:      max,
	}
}

func multiTenantBody() []byte {
	return []byte(`{"object":"whatsapp_business_account","entry":[{"id":"waba-a","changes":[{"value":{"metadata":{"phone_number_id":"551111"},"messages":[{"id":"m1","text":{"body":"` + testMarker + `"}}]},"field":"messages"}]},{"id":"waba-b","changes":[{"value":{"metadata":{"phone_number_id":"552222"},"tenant_id":"default","app_id":"from-payload"},"field":"messages"}]}]}`)
}

func signedPost(app App, secret string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/meta/webhook", bytes.NewReader(body))
	req.Header.Set(SignatureHeader, signBody(secret, body))
	return req
}

func challengeRequest(token, challenge string) *http.Request {
	query := url.Values{}
	query.Set(queryMode, "subscribe")
	query.Set(queryToken, token)
	query.Set(queryChallenge, challenge)
	return httptest.NewRequest(http.MethodGet, "/meta/webhook?"+query.Encode(), nil)
}

func newLogged(app App, persist Persister) (*Handler, *bytes.Buffer) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	return New(app, persist, logger), &logs
}

func assertClean(t *testing.T, rec *httptest.ResponseRecorder, logs string, forbidden ...string) {
	t.Helper()
	var blob strings.Builder
	blob.WriteString(rec.Body.String())
	blob.WriteString("\n")
	blob.WriteString(logs)
	for key, values := range rec.Header() {
		blob.WriteString(key)
		blob.WriteString("=")
		blob.WriteString(strings.Join(values, ","))
		blob.WriteString("\n")
	}
	text := blob.String()
	for _, item := range forbidden {
		if item != "" && strings.Contains(text, item) {
			t.Fatal("material restrito exposto")
		}
	}
}

type capture struct {
	mu       sync.Mutex
	calls    int
	envelope Envelope
	err      error
	block    chan struct{}
	release  chan struct{}
	started  chan struct{}
}

func (c *capture) Persist(ctx context.Context, envelope Envelope) error {
	c.mu.Lock()
	c.calls++
	c.envelope = Envelope{AppID: envelope.AppID, Raw: append([]byte(nil), envelope.Raw...)}
	err := c.err
	block := c.block
	release := c.release
	started := c.started
	c.mu.Unlock()
	if started != nil {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *capture) got() Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envelope
}

func TestHMACAcceptsOriginalBodyAndRejectsWhitespaceAndOtherApp(t *testing.T) {
	body := []byte("{\"object\":\"whatsapp_business_account\",\"entry\":[]}\n")
	spaced := []byte("{\"object\": \"whatsapp_business_account\", \"entry\": []}\n")
	app := testBinding(0)
	store := &capture{}
	handler, logs := newLogged(app, store)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedPost(app, testSecret, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if store.count() != 1 {
		t.Fatalf("persistências %d", store.count())
	}
	got := store.got()
	if got.AppID != testAppID || !bytes.Equal(got.Raw, body) {
		t.Fatal("envelope divergiu do corpo original")
	}
	assertClean(t, rec, logs.String(), testSecret, testToken, "entry")

	other := &capture{}
	otherHandler, otherLogs := newLogged(app, other)
	bad := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/meta/webhook", bytes.NewReader(spaced))
	req.Header.Set(SignatureHeader, signBody(testSecret, body))
	otherHandler.ServeHTTP(bad, req)
	if bad.Code != http.StatusUnauthorized || other.count() != 0 {
		t.Fatalf("whitespace status %d calls %d", bad.Code, other.count())
	}
	assertClean(t, bad, otherLogs.String(), testSecret, testToken, string(spaced))

	foreign := testBinding(0)
	foreign.ID = testOtherID
	foreign.Secret = "app-secret-outro-aplicativo"
	foreignStore := &capture{}
	foreignHandler, foreignLogs := newLogged(foreign, foreignStore)
	rejected := httptest.NewRecorder()
	foreignHandler.ServeHTTP(rejected, signedPost(app, testSecret, body))
	if rejected.Code != http.StatusUnauthorized || foreignStore.count() != 0 {
		t.Fatalf("outro app status %d calls %d", rejected.Code, foreignStore.count())
	}
	assertClean(t, rejected, foreignLogs.String(), testSecret, foreign.Secret, testToken)

	accepted := httptest.NewRecorder()
	foreignHandler.ServeHTTP(accepted, signedPost(foreign, foreign.Secret, body))
	if accepted.Code != http.StatusOK || foreignStore.count() != 1 || foreignStore.got().AppID != testOtherID {
		t.Fatal("app vinculado não aceitou o próprio segredo")
	}
}

func TestInvalidSignatureDoesNotPersist(t *testing.T) {
	body := multiTenantBody()
	store := &capture{}
	handler, logs := newLogged(testBinding(0), store)
	cases := []struct {
		name   string
		header string
	}{
		{name: "ausente", header: ""},
		{name: "malformada", header: "sha256=" + strings.Repeat("zz", 32)},
		{name: "invalida", header: signBody(testToken, body)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := store.count()
			req := httptest.NewRequest(http.MethodPost, "/meta/webhook", bytes.NewReader(body))
			if tc.header != "" {
				req.Header.Set(SignatureHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || store.count() != before {
				t.Fatalf("status %d calls %d", rec.Code, store.count())
			}
			assertClean(t, rec, logs.String(), testSecret, testToken, testMarker, "default", "551111")
		})
	}
}

func TestVerificationTokenIndependentFromAppSecret(t *testing.T) {
	challenge := "challenge-9f3c2a"
	handler, logs := newLogged(testBinding(0), &capture{})

	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, challengeRequest(testToken, challenge))
	if ok.Code != http.StatusOK || ok.Body.String() != challenge {
		t.Fatalf("desafio status %d", ok.Code)
	}
	assertClean(t, ok, logs.String(), testSecret, testToken)

	usingSecret := httptest.NewRecorder()
	handler.ServeHTTP(usingSecret, challengeRequest(testSecret, challenge))
	if usingSecret.Code != http.StatusUnauthorized || strings.Contains(usingSecret.Body.String(), challenge) {
		t.Fatalf("app secret como token status %d", usingSecret.Code)
	}
	assertClean(t, usingSecret, logs.String(), testSecret, testToken, challenge)

	wrong := httptest.NewRecorder()
	handler.ServeHTTP(wrong, challengeRequest("token-qualquer", challenge))
	if wrong.Code != http.StatusUnauthorized || strings.Contains(wrong.Body.String(), challenge) {
		t.Fatalf("token inválido status %d", wrong.Code)
	}
	assertClean(t, wrong, logs.String(), testSecret, testToken, challenge)

	same := testBinding(0)
	same.VerificationToken = same.Secret
	sameHandler, _ := newLogged(same, &capture{})
	collapsed := httptest.NewRecorder()
	sameHandler.ServeHTTP(collapsed, challengeRequest(same.Secret, challenge))
	if collapsed.Code != http.StatusUnauthorized || strings.Contains(collapsed.Body.String(), challenge) {
		t.Fatal("token igual ao app secret recebeu o desafio")
	}
}

func TestOversizeBodyRejected(t *testing.T) {
	body := bytes.Repeat([]byte(testMarker), 4)
	if len(body) < 2 {
		t.Fatal("corpo de teste curto")
	}
	store := &capture{}
	handler, logs := newLogged(testBinding(int64(len(body)-1)), store)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedPost(testBinding(0), testSecret, body))
	if rec.Code != http.StatusRequestEntityTooLarge || store.count() != 0 {
		t.Fatalf("status %d calls %d", rec.Code, store.count())
	}
	assertClean(t, rec, logs.String(), testSecret, testToken, testMarker)

	exactStore := &capture{}
	exactHandler, _ := newLogged(testBinding(int64(len(body))), exactStore)
	exact := httptest.NewRecorder()
	exactHandler.ServeHTTP(exact, signedPost(testBinding(0), testSecret, body))
	if exact.Code != http.StatusOK || exactStore.count() != 1 || !bytes.Equal(exactStore.got().Raw, body) {
		t.Fatal("corpo no limite foi recusado")
	}
}

func TestAckOnlyAfterPersistConfirmation(t *testing.T) {
	body := multiTenantBody()
	app := testBinding(0)

	t.Run("blocked", func(t *testing.T) {
		release := make(chan struct{})
		store := &capture{release: release, started: make(chan struct{})}
		handler, _ := newLogged(app, store)
		rec := httptest.NewRecorder()
		gate := &gateWriter{rec: rec}
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(gate, signedPost(app, testSecret, body))
		}()
		select {
		case <-store.started:
		case <-time.After(3 * time.Second):
			t.Fatal("persistência não iniciou")
		}
		if gate.written() {
			t.Fatal("ACK escrito antes da persistência confirmar")
		}
		close(release)
		waitDone(t, done)
		if rec.Code != http.StatusOK || store.count() != 1 {
			t.Fatalf("status %d calls %d", rec.Code, store.count())
		}
	})

	t.Run("failing", func(t *testing.T) {
		store := &capture{err: errors.New("insert failed " + testSecret + " " + testToken + " " + testMarker)}
		handler, logs := newLogged(app, store)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, signedPost(app, testSecret, body))
		if rec.Code != http.StatusInternalServerError || store.count() != 1 {
			t.Fatalf("status %d calls %d", rec.Code, store.count())
		}
		assertClean(t, rec, logs.String(), testSecret, testToken, testMarker, "default")
	})

	t.Run("missing", func(t *testing.T) {
		handler, logs := newLogged(app, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, signedPost(app, testSecret, body))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", rec.Code)
		}
		assertClean(t, rec, logs.String(), testSecret, testToken, testMarker)
	})

	t.Run("canceled", func(t *testing.T) {
		store := &capture{}
		handler, logs := newLogged(app, store)
		req := signedPost(app, testSecret, body)
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK || store.count() != 0 {
			t.Fatalf("status %d calls %d", rec.Code, store.count())
		}
		assertClean(t, rec, logs.String(), testSecret, testToken, testMarker)
	})

	t.Run("canceled-while-blocked", func(t *testing.T) {
		store := &capture{block: make(chan struct{}), started: make(chan struct{})}
		handler, logs := newLogged(app, store)
		req := signedPost(app, testSecret, body)
		ctx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		gate := &gateWriter{rec: rec}
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(gate, req)
		}()
		select {
		case <-store.started:
		case <-time.After(3 * time.Second):
			t.Fatal("persistência não iniciou")
		}
		if gate.written() {
			t.Fatal("ACK escrito antes da persistência confirmar")
		}
		cancel()
		waitDone(t, done)
		if rec.Code == http.StatusOK || store.count() != 1 {
			t.Fatalf("status %d calls %d", rec.Code, store.count())
		}
		assertClean(t, rec, logs.String(), testSecret, testToken, testMarker, "default")
	})
}

func TestMultitenantPayloadDoesNotAssignDefaultTenant(t *testing.T) {
	raw := multiTenantBody()
	if !bytes.Contains(raw, []byte("551111")) || !bytes.Contains(raw, []byte("552222")) || !bytes.Contains(raw, []byte("default")) {
		t.Fatal("corpo de teste sem dois números")
	}
	typ := reflect.TypeOf(Envelope{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "tenant") || strings.Contains(name, "inbox") || strings.Contains(name, "default") {
			t.Fatalf("campo %s", typ.Field(i).Name)
		}
	}
	store := &capture{}
	handler, logs := newLogged(testBinding(0), store)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedPost(testBinding(0), testSecret, raw))
	if rec.Code != http.StatusOK || store.count() != 1 {
		t.Fatalf("status %d calls %d", rec.Code, store.count())
	}
	got := store.got()
	if got.AppID != testAppID || !bytes.Equal(got.Raw, raw) {
		t.Fatal("envelope não preservou o bruto do app vinculado")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("default")) || bytes.Contains(rec.Body.Bytes(), []byte(testMarker)) {
		t.Fatal("resposta devolveu o envelope")
	}
	assertClean(t, rec, logs.String(), testSecret, testToken, testMarker, "default", "from-payload")

	opaque := []byte("%%% not-json " + testMarker)
	opaqueStore := &capture{}
	opaqueHandler, _ := newLogged(testBinding(0), opaqueStore)
	opaqueRec := httptest.NewRecorder()
	opaqueHandler.ServeHTTP(opaqueRec, signedPost(testBinding(0), testSecret, opaque))
	if opaqueRec.Code != http.StatusOK || !bytes.Equal(opaqueStore.got().Raw, opaque) || opaqueStore.got().AppID != testAppID {
		t.Fatal("corpo opaco não foi preservado")
	}
}

func TestCanceledDeadlineAndMethod(t *testing.T) {
	body := []byte(`{"ok":true}`)
	store := &capture{err: context.DeadlineExceeded}
	handler, _ := newLogged(testBinding(0), store)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedPost(testBinding(0), testSecret, body))
	if rec.Code == http.StatusOK {
		t.Fatal("prazo esgotado confirmou a entrega")
	}

	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPut, "/meta/webhook", nil))
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", method.Code)
	}
}

type gateWriter struct {
	mu   sync.Mutex
	rec  *httptest.ResponseRecorder
	seen bool
}

func (g *gateWriter) Header() http.Header {
	return g.rec.Header()
}

func (g *gateWriter) WriteHeader(status int) {
	g.mu.Lock()
	g.seen = true
	g.mu.Unlock()
	g.rec.WriteHeader(status)
}

func (g *gateWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	g.seen = true
	g.mu.Unlock()
	return g.rec.Write(p)
}

func (g *gateWriter) written() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seen
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler não retornou")
	}
}
