// Package httpapi publishes modular attachment routes; central composition is
// intentionally left to the integration task.
package httpapi

import (
	"bytes"
	"context"
	"errors"

	"chatisp/internal/attachments"
	attendance "chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	Attachments    *attachments.Service
	TrustedOrigins []string
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type Scope struct {
	Credentials
	InboxID        uuid.UUID `path:"inbox_id"`
	ConversationID uuid.UUID `path:"conversation_id"`
}
type Item struct {
	Scope
	AttachmentID uuid.UUID `path:"attachment_id"`
}
type registration struct {
	Scope
	Body struct {
		RequestID uuid.UUID `json:"request_id"`
		Name      string    `json:"name"`
		Bytes     int64     `json:"bytes"`
	}
}

// RawBody avoids JSON/base64 expansion. Huma bounds reads before allocation.
type upload struct {
	Item
	RawBody []byte
}
type recordOutput struct{ Body attachments.Record }
type downloadOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
}
type handler struct{ d Dependencies }

func Register(api huma.API, d Dependencies) {
	h := handler{d}
	base := "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments"
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"attachments"}, MaxBodyBytes: 4096, Errors: []int{401, 403, 404, 409, 413, 422, 503}}
	}
	huma.Register(api, op("attachment-register", "POST", base), h.register)
	u := op("attachment-upload", "PUT", base+"/{attachment_id}/content")
	u.MaxBodyBytes = attachments.MaxFileBytes
	huma.Register(api, u, h.upload)
	huma.Register(api, op("attachment-get", "GET", base+"/{attachment_id}"), h.get)
	huma.Register(api, op("attachment-validate", "POST", base+"/{attachment_id}/validate"), h.validate)
	huma.Register(api, op("attachment-download", "POST", base+"/{attachment_id}/download"), h.download)
	huma.Register(api, op("attachment-cleanup", "POST", base+"/cleanup"), h.cleanup)
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.d.Auth == nil || h.d.Attachments == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		ok := false
		for _, o := range h.d.TrustedOrigins {
			if o != "" && c.Origin == o {
				ok = true
			}
		}
		if !ok {
			return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return p, problem(e)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return p, problem(attachments.ErrForbidden)
	}
	return p, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrForbidden), errors.Is(e, attachments.ErrForbidden), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, attendance.ErrNotFound):
		return huma.Error404NotFound("não encontrado")
	case errors.Is(e, attachments.ErrInvalid):
		return huma.Error422UnprocessableEntity("anexo inválido")
	case errors.Is(e, attachments.ErrConflict):
		return huma.Error409Conflict("estado do anexo incompatível")
	case errors.Is(e, attachments.ErrLimit):
		return huma.Error413RequestEntityTooLarge("limite de armazenamento atingido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h handler) register(ctx context.Context, in *registration) (*recordOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	r, e := h.d.Attachments.Register(ctx, p, in.InboxID, in.ConversationID, attachments.RegisterInput{RequestID: in.Body.RequestID, Name: in.Body.Name, Bytes: in.Body.Bytes, Direction: "outgoing"})
	if e != nil {
		return nil, problem(e)
	}
	return &recordOutput{r}, nil
}
func (h handler) upload(ctx context.Context, in *upload) (*struct{}, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.d.Attachments.Upload(ctx, p, in.InboxID, in.ConversationID, in.AttachmentID, bytes.NewReader(in.RawBody)); e != nil {
		return nil, problem(e)
	}
	return nil, nil
}
func (h handler) get(ctx context.Context, in *Item) (*recordOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	r, e := h.d.Attachments.Get(ctx, p, in.InboxID, in.ConversationID, in.AttachmentID)
	if e != nil {
		return nil, problem(e)
	}
	return &recordOutput{r}, nil
}
func (h handler) validate(ctx context.Context, in *Item) (*recordOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	r, e := h.d.Attachments.Validate(ctx, p, in.InboxID, in.ConversationID, in.AttachmentID)
	if e != nil {
		return nil, problem(e)
	}
	return &recordOutput{r}, nil
}
func (h handler) download(ctx context.Context, in *Item) (*downloadOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	u, e := h.d.Attachments.Download(ctx, p, in.InboxID, in.ConversationID, in.AttachmentID)
	if e != nil {
		return nil, problem(e)
	}
	out := &downloadOutput{CacheControl: "no-store"}
	out.Body.URL = u
	out.Body.ExpiresIn = int(attachments.DownloadTTL.Seconds())
	return out, nil
}
func (h handler) cleanup(ctx context.Context, in *Scope) (*struct{}, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if _, e = h.d.Attachments.Cleanup(ctx, p, in.InboxID, in.ConversationID); e != nil {
		return nil, problem(e)
	}
	return nil, nil
}
