// Package httpapi registers the contact directory panel endpoints.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"chatisp/internal/contactdirectory"
	"chatisp/internal/platform/auth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	Contacts       contactdirectory.Service
	TrustedOrigins []string
}
type handler struct{ deps Dependencies }
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type itemInput struct {
	Credentials
	ID string `path:"contact_id"`
}
type bodyInput struct {
	Credentials
	RawBody []byte
}
type itemBodyInput struct {
	Credentials
	ID      string `path:"contact_id"`
	RawBody []byte
}
type listInput struct {
	Credentials
	Query  string `query:"q"`
	Order  string `query:"order"`
	Offset int    `query:"offset"`
	Limit  int    `query:"limit"`
}
type historyInput struct {
	Credentials
	ID     string `path:"contact_id"`
	Offset int    `query:"offset"`
	Limit  int    `query:"limit"`
}
type blockBody struct {
	Blocked *bool  `json:"blocked"`
	Reason  string `json:"reason"`
}

// OpenAPI-only schemas mirroring the bodies accepted by the handlers.
type createSchema struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email,omitempty"`
	Notes string `json:"notes,omitempty"`
}
type updateSchema struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
	Notes string `json:"notes,omitempty"`
}
type blockSchema struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason"`
}
type contactOutput struct{ Body contactdirectory.Contact }
type listOutput struct{ Body contactdirectory.Page }
type historyOutput struct{ Body contactdirectory.HistoryPage }

func Register(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, DefaultStatus: status, Tags: []string{"contatos"}, MaxBodyBytes: 16384, Errors: []int{401, 403, 404, 422, 503}}
	}
	base := "/api/panel/contacts"
	huma.Register(api, op("contacts-list", http.MethodGet, base, 200), h.list)
	huma.Register(api, op("contacts-get", http.MethodGet, base+"/{contact_id}", 200), h.get)
	huma.Register(api, op("contacts-history", http.MethodGet, base+"/{contact_id}/history", 200), h.history)
	registerBody(api, op("contacts-create", http.MethodPost, base, 201), createSchema{}, h.create)
	registerBody(api, op("contacts-update", http.MethodPut, base+"/{contact_id}", 200), updateSchema{}, h.update)
	registerBody(api, op("contacts-block", http.MethodPut, base+"/{contact_id}/block", 200), blockSchema{}, h.block)
}
func registerBody[I, O any](api huma.API, op huma.Operation, body any, fn func(context.Context, *I) (*O, error)) {
	op.SkipValidateBody = true
	op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "Contact")}}}
	huma.Register(api, op, fn)
	if op.Method == http.MethodPost {
		delete(api.OpenAPI().Paths[op.Path].Post.RequestBody.Content, "application/octet-stream")
	} else {
		delete(api.OpenAPI().Paths[op.Path].Put.RequestBody.Content, "application/octet-stream")
	}
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Contacts.DB == nil || h.deps.Contacts.Visibility == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		ok := false
		for _, o := range h.deps.TrustedOrigins {
			if c.Origin != "" && c.Origin == o {
				ok = true
			}
		}
		if !ok {
			return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.deps.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return p, problem(e)
	}
	return p, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrForbidden), errors.Is(e, auth.ErrCSRF), errors.Is(e, contactdirectory.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, contactdirectory.ErrNotFound):
		return huma.Error404NotFound("contato não encontrado")
	case errors.Is(e, contactdirectory.ErrInvalid):
		return huma.Error422UnprocessableEntity("dados inválidos")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func parseID(s string) (uuid.UUID, error) {
	id, e := uuid.Parse(s)
	if e != nil || id == uuid.Nil {
		return uuid.Nil, huma.Error422UnprocessableEntity("identificador inválido")
	}
	return id, nil
}
func decode(b []byte, v any) error {
	if json.Unmarshal(b, v) != nil {
		return huma.Error422UnprocessableEntity("corpo inválido")
	}
	return nil
}
func limit(n int) int {
	if n == 0 {
		return 50
	}
	return n
}
func (h handler) create(ctx context.Context, in *bodyInput) (*contactOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b contactdirectory.Input
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Contacts.Create(ctx, p, b)
	if e != nil {
		return nil, problem(e)
	}
	return &contactOutput{v}, nil
}
func (h handler) update(ctx context.Context, in *itemBodyInput) (*contactOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	id, e := parseID(in.ID)
	if e != nil {
		return nil, e
	}
	var b contactdirectory.Input
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Contacts.Update(ctx, p, id, b)
	if e != nil {
		return nil, problem(e)
	}
	return &contactOutput{v}, nil
}
func (h handler) block(ctx context.Context, in *itemBodyInput) (*contactOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	id, e := parseID(in.ID)
	if e != nil {
		return nil, e
	}
	var b blockBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	if b.Blocked == nil {
		return nil, huma.Error422UnprocessableEntity("blocked deve ser booleano")
	}
	v, e := h.deps.Contacts.SetBlocked(ctx, p, id, *b.Blocked, b.Reason)
	if e != nil {
		return nil, problem(e)
	}
	return &contactOutput{v}, nil
}
func (h handler) get(ctx context.Context, in *itemInput) (*contactOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	id, e := parseID(in.ID)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Contacts.Get(ctx, p, id)
	if e != nil {
		return nil, problem(e)
	}
	return &contactOutput{v}, nil
}
func (h handler) list(ctx context.Context, in *listInput) (*listOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	order := in.Order
	if order == "" {
		order = "name"
	}
	v, e := h.deps.Contacts.List(ctx, p, in.Query, order, in.Offset, limit(in.Limit))
	if e != nil {
		return nil, problem(e)
	}
	return &listOutput{v}, nil
}
func (h handler) history(ctx context.Context, in *historyInput) (*historyOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	id, e := parseID(in.ID)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Contacts.History(ctx, p, id, in.Offset, limit(in.Limit))
	if e != nil {
		return nil, problem(e)
	}
	return &historyOutput{v}, nil
}
