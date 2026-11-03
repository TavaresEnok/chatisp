package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// documentLeak matches a CPF/CNPJ in plain digits, punctuation or scientific form.
// Validation runs before the handler, so the submitted document must be removed
// from the response even when the caller is not authenticated yet.
var documentLeak = regexp.MustCompile(`\d{11,14}|\d{3}\.\d{3}\.\d{3}-\d{2}|\d{2}\.\d{3}\.\d{3}/\d{4}-\d{2}|(?i)\d+\.\d+e\+\d+`)

// redactDocumentErrors buffers the operation response and strips submitted
// documents from error bodies. Huma echoes the invalid value, or the whole
// object when another field fails, before the handler and before authentication.
func redactDocumentErrors(ctx huma.Context, next func(huma.Context)) {
	state := &captureState{}
	next(&bodyCapture{inner: ctx, state: state})
	body := state.buf.Bytes()
	if state.status >= 400 {
		body = scrubDocumentError(state.status, body)
	}
	if state.status != 0 {
		ctx.SetStatus(state.status)
	}
	if len(body) > 0 {
		_, _ = ctx.BodyWriter().Write(body)
	}
}

type captureState struct {
	buf    bytes.Buffer
	status int
}

// bodyCapture keeps the response in memory. The inner context is a named field
// because embedding huma.Context hides the Context method of the same name.
type bodyCapture struct {
	inner huma.Context
	state *captureState
}

func (c *bodyCapture) Operation() *huma.Operation { return c.inner.Operation() }
func (c *bodyCapture) Context() context.Context   { return c.inner.Context() }
func (c *bodyCapture) TLS() *tls.ConnectionState  { return c.inner.TLS() }
func (c *bodyCapture) Version() huma.ProtoVersion { return c.inner.Version() }
func (c *bodyCapture) Method() string             { return c.inner.Method() }
func (c *bodyCapture) Host() string               { return c.inner.Host() }
func (c *bodyCapture) RemoteAddr() string         { return c.inner.RemoteAddr() }
func (c *bodyCapture) URL() url.URL               { return c.inner.URL() }
func (c *bodyCapture) Param(name string) string   { return c.inner.Param(name) }
func (c *bodyCapture) Query(name string) string   { return c.inner.Query(name) }
func (c *bodyCapture) Header(name string) string  { return c.inner.Header(name) }
func (c *bodyCapture) EachHeader(cb func(name, value string)) {
	c.inner.EachHeader(cb)
}
func (c *bodyCapture) BodyReader() io.Reader { return c.inner.BodyReader() }
func (c *bodyCapture) GetMultipartForm() (*multipart.Form, error) {
	return c.inner.GetMultipartForm()
}
func (c *bodyCapture) SetReadDeadline(deadline time.Time) error {
	return c.inner.SetReadDeadline(deadline)
}
func (c *bodyCapture) SetStatus(code int) { c.state.status = code }
func (c *bodyCapture) Status() int {
	if c.state.status != 0 {
		return c.state.status
	}
	return c.inner.Status()
}
func (c *bodyCapture) SetHeader(name, value string)    { c.inner.SetHeader(name, value) }
func (c *bodyCapture) AppendHeader(name, value string) { c.inner.AppendHeader(name, value) }
func (c *bodyCapture) BodyWriter() io.Writer           { return &c.state.buf }

func (c *bodyCapture) WithContext(ctx context.Context) huma.Context {
	next := c.inner
	if w, ok := next.(interface {
		WithContext(context.Context) huma.Context
	}); ok {
		next = w.WithContext(ctx)
	}
	return &bodyCapture{inner: next, state: c.state}
}

func scrubDocumentError(status int, body []byte) []byte {
	if len(body) == 0 || !documentLeak.Match(body) && !bytes.Contains(body, []byte(`"value"`)) {
		return body
	}
	var node any
	if err := json.Unmarshal(body, &node); err != nil {
		if documentLeak.Match(body) {
			return genericProblem(status)
		}
		return body
	}
	node = scrubNode(node)
	out, err := json.Marshal(node)
	if err != nil || documentLeak.Match(out) {
		return genericProblem(status)
	}
	return out
}

func scrubNode(v any) any {
	switch n := v.(type) {
	case map[string]any:
		delete(n, "value")
		for key, child := range n {
			n[key] = scrubNode(child)
		}
		return n
	case []any:
		for i := range n {
			n[i] = scrubNode(n[i])
		}
		return n
	case string:
		if documentLeak.MatchString(n) {
			return "valor inválido"
		}
		return n
	case float64:
		abs := math.Abs(n)
		if abs >= 1e10 && abs < 1e15 {
			return nil
		}
		return n
	default:
		return v
	}
}

func genericProblem(status int) []byte {
	kind := "urn:chatisp:problem:bad-request"
	title := "Requisição inválida"
	if status == http.StatusUnprocessableEntity {
		kind = "urn:chatisp:problem:invalid"
		title = "Dados inválidos"
	}
	if status >= 500 {
		kind = "urn:chatisp:problem:unavailable"
		title = "Serviço indisponível"
	}
	raw, err := json.Marshal(map[string]any{
		"type":   kind,
		"title":  title,
		"status": status,
		"detail": "a requisição é inválida",
	})
	if err != nil {
		return []byte(`{"detail":"a requisição é inválida"}`)
	}
	return raw
}
