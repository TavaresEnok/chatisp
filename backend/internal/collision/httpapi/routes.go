package httpapi

import (
	"context"
	"errors"
	"net/http"

	attendanceStore "chatisp/internal/attendance/store"
	"chatisp/internal/collision"
	"chatisp/internal/platform/auth"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	Collision      *collision.Service
	TrustedOrigins []string
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type Input struct {
	Credentials
	InboxID        uuid.UUID `path:"inbox_id"`
	ConversationID uuid.UUID `path:"conversation_id"`
}
type mutation struct {
	Input
	Body struct {
		TabID  uuid.UUID `json:"tab_id"`
		Typing bool      `json:"typing"`
	}
}
type releaseInput struct {
	Input
	TabID uuid.UUID `path:"tab_id"`
}
type result struct {
	Body struct {
		Indicators []collision.Indicator `json:"indicators"`
		TTLSeconds int                   `json:"ttl_seconds"`
	}
}
type handler struct{ deps Dependencies }

func Register(api huma.API, d Dependencies) {
	h := handler{d}
	base := "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision"
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, DefaultStatus: status, Tags: []string{"collision"}, MaxBodyBytes: 1024, Errors: []int{401, 403, 404, 422, 503}}
	}
	huma.Register(api, op("collision-list", http.MethodGet, base, 200), h.list)
	huma.Register(api, op("collision-heartbeat", http.MethodPost, base, 204), h.heartbeat)
	huma.Register(api, op("collision-release", http.MethodDelete, base+"/{tab_id}", 204), h.release)
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.deps.Auth == nil || h.deps.Collision == nil || h.deps.Collision.DB == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		ok := false
		for _, o := range h.deps.TrustedOrigins {
			if c.Origin != "" && c.Origin == o {
				ok = true
				break
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
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return p, huma.Error403Forbidden("acesso negado")
	}
	return p, nil
}
func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrForbidden), errors.Is(e, collision.ErrForbidden), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, attendanceStore.ErrNotFound):
		return huma.Error404NotFound("não encontrado")
	case errors.Is(e, collision.ErrInvalid):
		return huma.Error422UnprocessableEntity("identificador inválido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h handler) list(ctx context.Context, in *Input) (*result, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Collision.List(ctx, p, in.InboxID, in.ConversationID)
	if e != nil {
		return nil, problem(e)
	}
	out := &result{}
	out.Body.Indicators = v
	out.Body.TTLSeconds = int(collision.TTL.Seconds())
	return out, nil
}
func (h handler) heartbeat(ctx context.Context, in *mutation) (*struct{}, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.deps.Collision.Heartbeat(ctx, p, in.InboxID, in.ConversationID, in.Body.TabID, in.Body.Typing); e != nil {
		return nil, problem(e)
	}
	return nil, nil
}
func (h handler) release(ctx context.Context, in *releaseInput) (*struct{}, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	if e = h.deps.Collision.Release(ctx, p, in.InboxID, in.ConversationID, in.TabID); e != nil {
		return nil, problem(e)
	}
	return nil, nil
}
