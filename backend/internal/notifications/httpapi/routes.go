package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/notifications"
	"chatisp/internal/platform/auth"
	"github.com/danielgtaylor/huma/v2"
)

type Dependencies struct {
	Auth           *auth.Service
	Notifications  *notifications.Service
	TrustedOrigins []string
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type input struct {
	Credentials
	Body notifications.Patch
}
type output struct{ Body notifications.Preferences }
type handler struct{ deps Dependencies }

func Register(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: "/api/panel/preferences/notifications", Summary: "Preferências de notificação do usuário", Tags: []string{"notificações"}, MaxBodyBytes: 4096, Errors: []int{401, 403, 422, 503}}
	}
	huma.Register(api, op("get-notification-preferences", http.MethodGet), h.get)
	huma.Register(api, op("patch-notification-preferences", http.MethodPatch), h.patch)
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Notifications == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, origin := range h.deps.TrustedOrigins {
			if c.Origin != "" && c.Origin == origin {
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
		if errors.Is(err, auth.ErrUnauthenticated) {
			return p, huma.Error401Unauthorized("não autenticado")
		}
		if errors.Is(err, auth.ErrCSRF) || errors.Is(err, auth.ErrForbidden) {
			return p, huma.Error403Forbidden("acesso negado")
		}
		return p, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	return p, nil
}
func problem(err error) error {
	if errors.Is(err, notifications.ErrForbidden) {
		return huma.Error403Forbidden("acesso negado")
	}
	if errors.Is(err, notifications.ErrInvalid) {
		return huma.Error422UnprocessableEntity("preferências inválidas")
	}
	return huma.Error503ServiceUnavailable("serviço indisponível")
}
func (h handler) get(ctx context.Context, c *Credentials) (*output, error) {
	p, err := h.principal(ctx, *c, false)
	if err != nil {
		return nil, err
	}
	v, err := h.deps.Notifications.Get(ctx, p)
	if err != nil {
		return nil, problem(err)
	}
	return &output{Body: v}, nil
}
func (h handler) patch(ctx context.Context, in *input) (*output, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	v, err := h.deps.Notifications.Update(ctx, p, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &output{Body: v}, nil
}
