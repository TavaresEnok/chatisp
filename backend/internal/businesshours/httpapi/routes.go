package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"chatisp/internal/businesshours"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	BusinessHours  businesshours.Service
	TrustedOrigins []string
}
type handler struct{ d Dependencies }
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type InboxInput struct {
	Credentials
	InboxID uuid.UUID `path:"inbox_id"`
}
type WriteInput struct {
	InboxInput
	Body businesshours.Command
}
type DeleteInput struct {
	InboxInput
	Body struct {
		RequestID       uuid.UUID `json:"request_id"`
		ExpectedVersion int64     `json:"expected_version"`
	}
}
type EvaluateInput struct {
	InboxInput
	Instant time.Time `query:"instant" required:"true"`
}
type ConfigOutput struct{ Body businesshours.Configuration }
type DecisionOutput struct{ Body businesshours.Decision }

func Register(api huma.API, d Dependencies) {
	h := handler{d}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"horários"}, DefaultStatus: 200, MaxBodyBytes: 32768, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	path := "/api/panel/inboxes/{inbox_id}/business-hours"
	huma.Register(api, op("business-hours-get", http.MethodGet, path), h.get)
	huma.Register(api, op("business-hours-put", http.MethodPut, path), h.put)
	huma.Register(api, op("business-hours-delete", http.MethodDelete, path), h.remove)
	huma.Register(api, op("business-hours-evaluate", http.MethodGet, path+"/evaluation"), h.evaluate)
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (businesshours.Principal, error) {
	if h.d.Auth == nil || h.d.BusinessHours.DB == nil {
		return businesshours.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, o := range h.d.TrustedOrigins {
			if o != "" && c.Origin == o {
				allowed = true
				break
			}
		}
		if !allowed {
			return businesshours.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return businesshours.Principal{}, problem(e)
	}
	if p.IsSuperadmin || p.EnrollmentOnly {
		return businesshours.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return businesshours.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role}, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrForbidden), errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrEnrollmentRequired), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, sectors.ErrNotFound):
		return huma.Error404NotFound("caixa não encontrada")
	case errors.Is(e, businesshours.ErrConflict):
		return huma.Error409Conflict("versão ou comando conflitante")
	case errors.Is(e, businesshours.ErrInvalid):
		return huma.Error422UnprocessableEntity("horários inválidos")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h handler) get(ctx context.Context, in *InboxInput) (*ConfigOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	c, e := h.d.BusinessHours.Get(ctx, p, in.InboxID)
	if e != nil {
		return nil, problem(e)
	}
	return &ConfigOutput{Body: c}, nil
}
func (h handler) put(ctx context.Context, in *WriteInput) (*ConfigOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	c, e := h.d.BusinessHours.Put(ctx, p, in.InboxID, in.Body)
	if e != nil {
		return nil, problem(e)
	}
	return &ConfigOutput{Body: c}, nil
}
func (h handler) remove(ctx context.Context, in *DeleteInput) (*ConfigOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	c, e := h.d.BusinessHours.Delete(ctx, p, in.InboxID, businesshours.Command{RequestID: in.Body.RequestID, ExpectedVersion: in.Body.ExpectedVersion})
	if e != nil {
		return nil, problem(e)
	}
	return &ConfigOutput{Body: c}, nil
}
func (h handler) evaluate(ctx context.Context, in *EvaluateInput) (*DecisionOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	c, e := h.d.BusinessHours.Evaluate(ctx, p, in.InboxID, in.Instant)
	if e != nil {
		return nil, problem(e)
	}
	return &DecisionOutput{Body: c}, nil
}
