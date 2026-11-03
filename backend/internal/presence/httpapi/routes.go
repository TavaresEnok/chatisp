// Package httpapi exposes presence routes for a later router composition.
// Register does not edit the central runtime, OpenAPI document or generated schema.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/platform/auth"
	"chatisp/internal/presence"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Dependencies are the already constructed auth and presence services.
type Dependencies struct {
	Auth           *auth.Service
	Presence       presence.Service
	TrustedOrigins []string
}

type handler struct{ d Dependencies }

// Credentials carries the panel session. Writes also require Origin and CSRF.
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}

type heartbeatInput struct {
	Credentials
	Body struct {
		TabID uuid.UUID `json:"tab_id"`
	}
}
type tabInput struct {
	Credentials
	TabID uuid.UUID `path:"tab_id"`
}
type manualInput struct {
	Credentials
	Body presence.ManualCommand
}
type settingsInput struct {
	Credentials
	Body presence.ToleranceCommand
}
type sectorInput struct {
	Credentials
	SectorID uuid.UUID `path:"sector_id"`
}
type snapshotOutput struct {
	Body presence.Snapshot
}
type settingsOutput struct {
	Body presence.Settings
}
type listOutput struct {
	Body struct {
		Agents              []presence.Snapshot `json:"agents"`
		HeartbeatTTLSeconds int                 `json:"heartbeat_ttl_seconds"`
		ToleranceNote       string              `json:"tolerance_note"`
	}
}

// Register mounts the panel routes on an existing Huma API.
func Register(api huma.API, d Dependencies) {
	h := handler{d}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"presença"}, DefaultStatus: 200, MaxBodyBytes: 4096, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	huma.Register(api, op("presence-current", http.MethodGet, "/api/panel/presence"), h.current)
	huma.Register(api, op("presence-heartbeat", http.MethodPost, "/api/panel/presence/heartbeat"), h.heartbeat)
	huma.Register(api, op("presence-close", http.MethodDelete, "/api/panel/presence/tabs/{tab_id}"), h.close)
	huma.Register(api, op("presence-manual", http.MethodPut, "/api/panel/presence/manual"), h.manual)
	huma.Register(api, op("presence-settings-get", http.MethodGet, "/api/panel/presence/settings"), h.getSettings)
	huma.Register(api, op("presence-settings-put", http.MethodPut, "/api/panel/presence/settings"), h.putSettings)
	huma.Register(api, op("presence-sector", http.MethodGet, "/api/panel/sectors/{sector_id}/presence"), h.list)
}

func (h handler) principal(ctx context.Context, c Credentials, write bool) (presence.Principal, error) {
	if h.d.Auth == nil || h.d.Presence.DB == nil {
		return presence.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
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
			return presence.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, err := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return presence.Principal{}, problem(err)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return presence.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return p, nil
}

func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, presence.ErrForbidden), errors.Is(err, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, presence.ErrNotFound), errors.Is(err, sectors.ErrNotFound):
		return huma.Error404NotFound("setor não encontrado")
	case errors.Is(err, presence.ErrConflict):
		return huma.Error409Conflict("versão ou comando conflitante")
	case errors.Is(err, presence.ErrInvalid):
		return huma.Error422UnprocessableEntity("presença inválida")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}

func (h handler) current(ctx context.Context, in *Credentials) (*snapshotOutput, error) {
	p, err := h.principal(ctx, *in, false)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Presence.Current(ctx, p)
	if err != nil {
		return nil, problem(err)
	}
	return &snapshotOutput{Body: body}, nil
}

func (h handler) heartbeat(ctx context.Context, in *heartbeatInput) (*snapshotOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Presence.Heartbeat(ctx, p, in.Body.TabID)
	if err != nil {
		return nil, problem(err)
	}
	return &snapshotOutput{Body: body}, nil
}

func (h handler) close(ctx context.Context, in *tabInput) (*snapshotOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Presence.Close(ctx, p, in.TabID)
	if err != nil {
		return nil, problem(err)
	}
	return &snapshotOutput{Body: body}, nil
}

func (h handler) manual(ctx context.Context, in *manualInput) (*snapshotOutput, error) {
	p, err := h.principal(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Presence.SetManual(ctx, p, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &snapshotOutput{Body: body}, nil
}

func (h handler) getSettings(ctx context.Context, in *Credentials) (*settingsOutput, error) {
	p, err := h.principal(ctx, *in, false)
	if err != nil {
		return nil, err
	}
	body, err := h.d.Presence.GetSettings(ctx, p)
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
	body, err := h.d.Presence.PutTolerance(ctx, p, in.Body)
	if err != nil {
		return nil, problem(err)
	}
	return &settingsOutput{Body: body}, nil
}

func (h handler) list(ctx context.Context, in *sectorInput) (*listOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	agents, err := h.d.Presence.List(ctx, p, in.SectorID)
	if err != nil {
		return nil, problem(err)
	}
	out := &listOutput{}
	out.Body.Agents = agents
	out.Body.HeartbeatTTLSeconds = int(presence.HeartbeatTTL.Seconds())
	out.Body.ToleranceNote = "offline_for_unassign apenas sinaliza; esta rota não desatribui"
	return out, nil
}
