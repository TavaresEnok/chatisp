// Package httpapi exposes lifecycle routes for a later router composition.
// Register does not edit the central runtime, OpenAPI document or generated schema.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/lifecycle"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Dependencies are the already constructed auth and lifecycle services.
type Dependencies struct {
	Auth           *auth.Service
	Lifecycle      lifecycle.Service
	TrustedOrigins []string
}

type handler struct{ d Dependencies }

// Credentials carries the panel session. Writes also require Origin and CSRF.
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}

type snoozeInput struct {
	Credentials
	ConversationID uuid.UUID `path:"conversation_id"`
	Body           lifecycle.SnoozeCommand
}

type getInput struct {
	Credentials
	ConversationID uuid.UUID `path:"conversation_id"`
	InboxID        uuid.UUID `query:"inbox_id"`
}

type settingsInput struct {
	Credentials
	Body lifecycle.InactivityCommand
}

type snoozeOutput struct{ Body lifecycle.Snooze }
type settingsOutput struct{ Body lifecycle.InactivitySettings }

// Register mounts the panel routes on an existing Huma API.
func Register(api huma.API, d Dependencies) {
	h := handler{d}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"ciclo"}, DefaultStatus: 200, MaxBodyBytes: 8192, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	huma.Register(api, op("lifecycle-snooze-put", http.MethodPut, "/api/panel/conversations/{conversation_id}/snooze"), h.snooze)
	huma.Register(api, op("lifecycle-snooze-get", http.MethodGet, "/api/panel/conversations/{conversation_id}/snooze"), h.get)
	huma.Register(api, op("lifecycle-inactivity-get", http.MethodGet, "/api/panel/lifecycle/inactivity"), h.getSettings)
	huma.Register(api, op("lifecycle-inactivity-put", http.MethodPut, "/api/panel/lifecycle/inactivity"), h.putSettings)
}

func (h handler) principal(ctx context.Context, c Credentials, write bool) (lifecycle.Principal, error) {
	if h.d.Auth == nil || h.d.Lifecycle.DB == nil {
		return lifecycle.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, origin := range h.d.TrustedOrigins {
			if origin != "" && c.Origin == origin {
				allowed = true
				break
			}
		}
		if !allowed {
			return lifecycle.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, err := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return lifecycle.Principal{}, problem(err)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return lifecycle.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return lifecycle.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}, nil
}

func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, lifecycle.ErrForbidden), errors.Is(err, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, lifecycle.ErrNotFound), errors.Is(err, sectors.ErrNotFound):
		return huma.Error404NotFound("adiamento não encontrado")
	case errors.Is(err, lifecycle.ErrConflict):
		return huma.Error409Conflict("versão ou comando conflitante")
	case errors.Is(err, lifecycle.ErrInvalid):
		return huma.Error422UnprocessableEntity("comando inválido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}

func (h handler) snooze(ctx context.Context, in *snoozeInput) (*snoozeOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Lifecycle.Snooze(ctx, p, in.ConversationID, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &snoozeOutput{Body: body}, nil
}

func (h handler) get(ctx context.Context, in *getInput) (*snoozeOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Lifecycle.Get(ctx, p, in.InboxID, in.ConversationID)
	if err != nil {
		return nil, problem(err)
	}
	return &snoozeOutput{Body: body}, nil
}

func (h handler) getSettings(ctx context.Context, in *Credentials) (*settingsOutput, error) {
	p, err := h.principal(ctx, *in, false)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Lifecycle.GetInactivity(ctx, p)
	if err != nil {
		return nil, problem(err)
	}
	return &settingsOutput{Body: body}, nil
}

func (h handler) putSettings(ctx context.Context, in *settingsInput) (*settingsOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Lifecycle.PutInactivity(ctx, p, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &settingsOutput{Body: body}, nil
}
