// Package httpapi registers the contact-document panel routes.
// The central router is left for a later composition task.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/contactdocs"
	"chatisp/internal/platform/auth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Dependencies wires session authentication, the document service and the
// exact trusted origins. Writes, reveal and search require Origin and CSRF.
type Dependencies struct {
	Auth           *auth.Service
	Documents      contactdocs.Service
	TrustedOrigins []string
}

type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}

// ItemInput is exported so the router can see the session, CSRF, origin and path.
type ItemInput struct {
	Credentials
	ContactID uuid.UUID `path:"contact_id"`
}

// WriteInput carries an idempotent set or clear command.
type WriteInput struct {
	ItemInput
	Body contactdocs.Command
}

// SearchInput carries the document used for an exact HMAC lookup.
type SearchInput struct {
	Credentials
	Body struct {
		Document string `json:"document"`
	}
}

type viewOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         contactdocs.View
}

type revealOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         contactdocs.Revealed
}

type matchOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         contactdocs.Match
}

type handler struct{ deps Dependencies }

// Register publishes the modular routes. It does not mount them on the process router.
func Register(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{
			OperationID:   id,
			Method:        method,
			Path:          path,
			Tags:          []string{"documentos do contato"},
			DefaultStatus: http.StatusOK,
			MaxBodyBytes:  4096,
			Errors:        []int{401, 403, 404, 409, 422, 503},
			Middlewares:   huma.Middlewares{redactDocumentErrors},
		}
	}
	base := "/api/panel/contacts/{contact_id}/document"
	huma.Register(api, op("contact-document-get", http.MethodGet, base), h.get)
	huma.Register(api, op("contact-document-put", http.MethodPut, base), h.put)
	huma.Register(api, op("contact-document-delete", http.MethodDelete, base), h.clear)
	huma.Register(api, op("contact-document-reveal", http.MethodPost, base+"/reveal"), h.reveal)
	huma.Register(api, op("contact-document-search", http.MethodPost, "/api/panel/contacts/document-search"), h.search)
}

func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Documents.DB == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, origin := range h.deps.TrustedOrigins {
			if origin != "" && c.Origin == origin {
				allowed = true
				break
			}
		}
		if !allowed {
			return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, err := h.deps.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return auth.Principal{}, problem(err)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return auth.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return p, nil
}

func (h handler) get(ctx context.Context, in *ItemInput) (*viewOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	view, err := h.deps.Documents.Masked(ctx, p, in.ContactID)
	if err != nil {
		return nil, problem(err)
	}
	return &viewOutput{CacheControl: "no-store", Body: view}, nil
}

func (h handler) put(ctx context.Context, in *WriteInput) (*viewOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	view, err := h.deps.Documents.Set(ctx, p, in.ContactID, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &viewOutput{CacheControl: "no-store", Body: view}, nil
}

func (h handler) clear(ctx context.Context, in *WriteInput) (*viewOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	view, err := h.deps.Documents.Clear(ctx, p, in.ContactID, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &viewOutput{CacheControl: "no-store", Body: view}, nil
}

func (h handler) reveal(ctx context.Context, in *ItemInput) (*revealOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	opened, err := h.deps.Documents.Reveal(ctx, p, in.ContactID)
	if err != nil {
		return nil, problem(err)
	}
	return &revealOutput{CacheControl: "no-store", Body: opened}, nil
}

func (h handler) search(ctx context.Context, in *SearchInput) (*matchOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	match, err := h.deps.Documents.FindByDocumentHMAC(ctx, p, in.Body.Document)
	if err != nil {
		return nil, problem(err)
	}
	return &matchOutput{CacheControl: "no-store", Body: match}, nil
}

func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, contactdocs.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, contactdocs.ErrNotFound):
		return huma.Error404NotFound("contato não encontrado")
	case errors.Is(err, contactdocs.ErrConflict):
		return huma.Error409Conflict("versão ou comando conflitante")
	case errors.Is(err, contactdocs.ErrInvalid):
		return huma.Error422UnprocessableEntity("documento inválido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
