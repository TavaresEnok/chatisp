package httpapi

import (
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
	Sectors        sectors.Service
	TrustedOrigins []string
}
type handler struct{ d Dependencies }
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type input struct{ Credentials }
type SectorInput struct {
	Credentials
	SectorID uuid.UUID `path:"sector_id"`
}
type bindingInput struct {
	SectorInput
	TargetID uuid.UUID `path:"target_id"`
}
type createInput struct {
	Credentials
	Body struct {
		Name string `json:"name"`
	}
}
type updateInput struct {
	SectorInput
	Body struct {
		Name   *string `json:"name,omitempty"`
		Active *bool   `json:"active,omitempty"`
	}
}
type listOutput struct {
	Body struct {
		Sectors []sectors.Sector `json:"sectors"`
	}
}
type createOutput struct{ Body sectors.Sector }
type detailsOutput struct{ Body sectors.Details }
type emptyOutput struct{}

func Register(api huma.API, d Dependencies) {
	h := handler{d}
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"setores"}, DefaultStatus: status, MaxBodyBytes: 16384, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	base := "/api/panel/sectors"
	item := base + "/{sector_id}"
	huma.Register(api, op("sectors-list", http.MethodGet, base, 200), h.list)
	huma.Register(api, op("sectors-details", http.MethodGet, item, 200), h.details)
	huma.Register(api, op("sectors-create", http.MethodPost, base, 201), h.create)
	huma.Register(api, op("sectors-update", http.MethodPatch, item, 200), h.update)
	huma.Register(api, op("sectors-members-add", http.MethodPut, item+"/memberships/{target_id}", 200), h.addMember)
	huma.Register(api, op("sectors-members-remove", http.MethodDelete, item+"/memberships/{target_id}", 200), h.removeMember)
	huma.Register(api, op("sectors-inboxes-add", http.MethodPut, item+"/inboxes/{target_id}", 200), h.addInbox)
	huma.Register(api, op("sectors-inboxes-remove", http.MethodDelete, item+"/inboxes/{target_id}", 200), h.removeInbox)
	huma.Register(api, op("sectors-inbox-default", http.MethodPut, item+"/inboxes/{target_id}/default", 200), h.setDefault)
}
func (h handler) details(ctx context.Context, in *SectorInput) (*detailsOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.d.Sectors.Details(ctx, p, in.SectorID)
	if e != nil {
		return nil, problem(e)
	}
	return &detailsOutput{Body: v}, nil
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (sectors.Principal, error) {
	if h.d.Auth == nil || h.d.Sectors.DB == nil {
		return sectors.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, o := range h.d.TrustedOrigins {
			if c.Origin != "" && c.Origin == o {
				allowed = true
				break
			}
		}
		if !allowed {
			return sectors.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return sectors.Principal{}, problem(e)
	}
	if p.IsSuperadmin || p.EnrollmentOnly {
		return sectors.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrForbidden), errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrEnrollmentRequired), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, sectors.ErrNotFound):
		return huma.Error404NotFound("registro não encontrado")
	case errors.Is(e, sectors.ErrConflict):
		return huma.Error409Conflict("conflito")
	case errors.Is(e, sectors.ErrInvalid):
		return huma.Error422UnprocessableEntity("comando inválido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h handler) list(ctx context.Context, in *input) (*listOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.d.Sectors.List(ctx, p)
	if e != nil {
		return nil, problem(e)
	}
	o := &listOutput{}
	o.Body.Sectors = v
	return o, nil
}
func (h handler) create(ctx context.Context, in *createInput) (*createOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	v, e := h.d.Sectors.Create(ctx, p, in.Body.Name)
	if e != nil {
		return nil, problem(e)
	}
	return &createOutput{Body: v}, nil
}
func (h handler) update(ctx context.Context, in *updateInput) (*emptyOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.d.Sectors.Update(ctx, p, in.SectorID, in.Body.Name, in.Body.Active); e != nil {
		return nil, problem(e)
	}
	return &emptyOutput{}, nil
}
func (h handler) bind(ctx context.Context, in *bindingInput, kind string, add bool) (*emptyOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.d.Sectors.Bind(ctx, p, in.SectorID, in.TargetID, kind, add); e != nil {
		return nil, problem(e)
	}
	return &emptyOutput{}, nil
}
func (h handler) addMember(c context.Context, i *bindingInput) (*emptyOutput, error) {
	return h.bind(c, i, "membership", true)
}
func (h handler) removeMember(c context.Context, i *bindingInput) (*emptyOutput, error) {
	return h.bind(c, i, "membership", false)
}
func (h handler) addInbox(c context.Context, i *bindingInput) (*emptyOutput, error) {
	return h.bind(c, i, "inbox", true)
}
func (h handler) removeInbox(c context.Context, i *bindingInput) (*emptyOutput, error) {
	return h.bind(c, i, "inbox", false)
}
func (h handler) setDefault(ctx context.Context, in *bindingInput) (*emptyOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.d.Sectors.SetDefault(ctx, p, in.SectorID, in.TargetID); e != nil {
		return nil, problem(e)
	}
	return &emptyOutput{}, nil
}
