// Package httpapi registers channel policy endpoints; central routing is composed separately.
package httpapi

import (
	"chatisp/internal/channelpolicy"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"net/http"
)

type Dependencies struct {
	Auth           *auth.Service
	Policy         *channelpolicy.Service
	TrustedOrigins []string
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type InboxInput struct {
	Credentials
	InboxID uuid.UUID `path:"inbox_id"`
}
type ConsentInput struct {
	InboxInput
	ContactID uuid.UUID `path:"contact_id"`
	Body      struct {
		Purpose  string `json:"purpose" maxLength:"80" minLength:"1"`
		Source   string `json:"source" maxLength:"120" minLength:"1"`
		Evidence string `json:"evidence" maxLength:"500" minLength:"1"`
		Revoke   bool   `json:"revoke"`
	}
}
type ConsentOutput struct {
	Body struct {
		InvalidatedIDs []uuid.UUID `json:"invalidated_ids"`
	}
}
type TemplateOutput struct {
	Body struct {
		Templates []channelpolicy.Template `json:"templates"`
	}
}
type handler struct{ d Dependencies }

func Register(api huma.API, d Dependencies) {
	h := handler{d}
	base := "/api/panel/inboxes/{inbox_id}/channel-policy"
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"channel-policy"}, MaxBodyBytes: 4096, Errors: []int{401, 403, 409, 422, 503}}
	}
	huma.Register(api, op("channel-policy-consent", http.MethodPost, base+"/contacts/{contact_id}/consents"), h.consent)
	huma.Register(api, op("channel-policy-templates", http.MethodGet, base+"/templates"), h.templates)
	huma.Register(api, op("channel-policy-sync", http.MethodPost, base+"/templates/sync"), h.sync)
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (sectors.Principal, error) {
	var out sectors.Principal
	if h.d.Auth == nil || h.d.Policy == nil || h.d.Policy.DB == nil {
		return out, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		ok := false
		for _, o := range h.d.TrustedOrigins {
			if c.Origin != "" && o == c.Origin {
				ok = true
			}
		}
		if !ok {
			return out, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return out, problem(e)
	}
	if p.EnrollmentOnly || p.IsSuperadmin || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return out, huma.Error403Forbidden("acesso negado")
	}
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrForbidden), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, channelpolicy.ErrInvalid), errors.Is(e, channelpolicy.ErrUnsupported):
		return huma.Error422UnprocessableEntity("operação inválida")
	case errors.Is(e, channelpolicy.ErrStaleCatalog):
		return huma.Error409Conflict("sincronização superada por solicitação mais recente")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h handler) consent(ctx context.Context, in *ConsentInput) (*ConsentOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	ids, e := h.d.Policy.RecordConsent(ctx, p, channelpolicy.ConsentInput{Scope: channelpolicy.Scope{InboxID: in.InboxID, ContactID: in.ContactID, Purpose: in.Body.Purpose}, Source: in.Body.Source, Evidence: in.Body.Evidence, Revoke: in.Body.Revoke})
	if e != nil {
		return nil, problem(e)
	}
	out := &ConsentOutput{}
	out.Body.InvalidatedIDs = ids
	return out, nil
}
func (h handler) templates(ctx context.Context, in *InboxInput) (*TemplateOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.d.Policy.ListTemplates(ctx, p, in.InboxID)
	if e != nil {
		return nil, problem(e)
	}
	out := &TemplateOutput{}
	out.Body.Templates = v
	return out, nil
}
func (h handler) sync(ctx context.Context, in *InboxInput) (*struct{}, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.d.Policy.SyncTemplates(ctx, p, in.InboxID); e != nil {
		return nil, problem(e)
	}
	return nil, nil
}
