package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"chatisp/internal/triage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Dependencies is the composition point. Register does not edit the central router.
type Dependencies struct {
	Auth           *auth.Service
	Triage         triage.Service
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
	Body triage.Command
}

type DeleteInput struct {
	InboxInput
	Body struct {
		RequestID       uuid.UUID `json:"request_id"`
		ExpectedVersion int64     `json:"expected_version"`
	}
}

type MenuOutput struct{ Body triage.Menu }

// Register publishes the inbox menu CRUD for a later router composition.
func Register(api huma.API, d Dependencies) {
	h := handler{d}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"triagem"}, DefaultStatus: 200, MaxBodyBytes: 32768, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	path := "/api/panel/inboxes/{inbox_id}/triage-menu"
	huma.Register(api, op("triage-menu-get", http.MethodGet, path), h.get)
	huma.Register(api, op("triage-menu-put", http.MethodPut, path), h.put)
	huma.Register(api, op("triage-menu-delete", http.MethodDelete, path), h.remove)
}

func (h handler) principal(ctx context.Context, c Credentials, write bool) (triage.Principal, error) {
	if h.d.Auth == nil || h.d.Triage.DB == nil {
		return triage.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
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
			return triage.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	session, err := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return triage.Principal{}, problem(err)
	}
	if session.IsSuperadmin || session.EnrollmentOnly {
		return triage.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return triage.Principal{TenantID: session.TenantID, UserID: session.UserID, MembershipID: session.MembershipID, Role: session.Role}, nil
}

func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, sectors.ErrNotFound):
		return huma.Error404NotFound("caixa não encontrada")
	case errors.Is(err, triage.ErrConflict):
		return huma.Error409Conflict("versão ou comando conflitante")
	case errors.Is(err, triage.ErrInvalid):
		return huma.Error422UnprocessableEntity("triagem inválida")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}

func (h handler) get(ctx context.Context, in *InboxInput) (*MenuOutput, error) {
	principal, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	menu, err := h.d.Triage.Get(ctx, principal, in.InboxID)
	if err != nil {
		return nil, problem(err)
	}
	return &MenuOutput{Body: menu}, nil
}

func (h handler) put(ctx context.Context, in *WriteInput) (*MenuOutput, error) {
	principal, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	menu, err := h.d.Triage.Put(ctx, principal, in.InboxID, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &MenuOutput{Body: menu}, nil
}

func (h handler) remove(ctx context.Context, in *DeleteInput) (*MenuOutput, error) {
	principal, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	menu, err := h.d.Triage.Delete(ctx, principal, in.InboxID, in.Body.RequestID, in.Body.ExpectedVersion)
	if err != nil {
		return nil, problem(err)
	}
	return &MenuOutput{Body: menu}, nil
}
