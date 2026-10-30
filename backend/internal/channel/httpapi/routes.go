// Package httpapi exposes the panel's tenant-scoped Meta configuration operations.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"chatisp/internal/channel/configuration"
	"chatisp/internal/platform/auth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	Channels       *configuration.Service
	TrustedOrigins []string
}
type handler struct{ deps Dependencies }

// Register registers all operations, including when services are unavailable.
func Register(api huma.API, deps Dependencies) {
	h := &handler{deps}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"canais"}, DefaultStatus: status, MaxBodyBytes: 16384, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	huma.Register(api, op("meta-channel-status", http.MethodGet, "/api/panel/channels/meta", "Listar caixas e alertas Meta", 200), h.status)
	configure := op("meta-channel-configure", http.MethodPost, "/api/panel/channels/meta", "Configurar caixa Meta", 200)
	configure.SkipValidateBody = true
	configure.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(manualBody{}), true, "MetaManual")}}}
	huma.Register(api, configure, h.configure)
	delete(api.OpenAPI().Paths[configure.Path].Post.RequestBody.Content, "application/octet-stream")
	huma.Register(api, op("meta-channel-inspection", http.MethodGet, "/api/panel/channels/meta/inspection", "Inspecionar referências Meta", 200), h.inspect)
	huma.Register(api, op("meta-channel-alert-ack", http.MethodPost, "/api/panel/channels/meta/alerts/{alert_id}/ack", "Reconhecer alerta Meta", 204), h.ack)
}
func (h *handler) authenticate(ctx context.Context, c Credentials, write bool) ([]byte, error) {
	if h.deps.Auth == nil || h.deps.Channels == nil {
		return nil, huma.Error503ServiceUnavailable("serviço indisponível")
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
			return nil, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, err := h.deps.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return nil, problem(err)
	}
	if p.IsSuperadmin || p.Role != "admin" || p.TenantID == uuid.Nil || p.EnrollmentOnly {
		return nil, huma.Error403Forbidden("acesso negado")
	}
	return auth.HashSecret(c.Session), nil
}
func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, configuration.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, configuration.ErrForbidden), errors.Is(err, configuration.ErrMFARequired):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, configuration.ErrNotFound):
		return huma.Error404NotFound("registro não encontrado")
	case errors.Is(err, configuration.ErrConflict), errors.Is(err, configuration.ErrStaleVersion), errors.Is(err, configuration.ErrDegraded):
		return huma.Error409Conflict("conflito de configuração")
	case errors.Is(err, configuration.ErrInvalid):
		return huma.Error422UnprocessableEntity("configuração inválida")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
func (h *handler) configure(ctx context.Context, in *configureInput) (*boxOutput, error) {
	hash, err := h.authenticate(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	var b manualBody
	if err := json.Unmarshal(in.RawBody, &b); err != nil {
		return nil, huma.Error422UnprocessableEntity("corpo inválido")
	}
	v, err := h.deps.Channels.Configure(ctx, hash, configuration.Manual{InboxName: b.InboxName, DisplayNumber: b.DisplayNumber, AppID: b.AppID, AppSecret: b.AppSecret, WABAID: b.WABAID, PhoneNumberID: b.PhoneNumberID, AccessToken: b.AccessToken})
	if err != nil {
		return nil, problem(err)
	}
	return &boxOutput{Body: boxFrom(v)}, nil
}
func (h *handler) status(ctx context.Context, in *Credentials) (*statusOutput, error) {
	hash, err := h.authenticate(ctx, *in, false)
	if err != nil {
		return nil, err
	}
	v, err := h.deps.Channels.Status(ctx, hash)
	if err != nil {
		return nil, problem(err)
	}
	out := &statusOutput{}
	out.Body.Boxes = make([]box, 0, len(v.Boxes))
	out.Body.Alerts = make([]alert, 0, len(v.Alerts))
	for _, b := range v.Boxes {
		out.Body.Boxes = append(out.Body.Boxes, boxFrom(b))
	}
	for _, a := range v.Alerts {
		out.Body.Alerts = append(out.Body.Alerts, alert{a.ID, a.ConnectorID, a.Action, a.Result, a.CreatedAt})
	}
	return out, nil
}
func (h *handler) inspect(ctx context.Context, in *Credentials) (*inspectionOutput, error) {
	hash, err := h.authenticate(ctx, *in, false)
	if err != nil {
		return nil, err
	}
	v, err := h.deps.Channels.Inspect(ctx, hash)
	if err != nil {
		return nil, problem(err)
	}
	out := &inspectionOutput{}
	out.Body.Quarantine = make([]quarantine, 0, len(v.Quarantine))
	out.Body.Items = make([]item, 0, len(v.Items))
	out.Body.Discarded = make([]job, 0, len(v.Discarded))
	for _, x := range v.Quarantine {
		out.Body.Quarantine = append(out.Body.Quarantine, quarantine{x.ID, x.DeliveryID, x.Reason, x.CreatedAt})
	}
	for _, x := range v.Items {
		out.Body.Items = append(out.Body.Items, item{x.ID, x.InboxID, x.ExternalKey, x.Kind, x.State})
	}
	for _, x := range v.Discarded {
		out.Body.Discarded = append(out.Body.Discarded, job{x.ID, x.Kind, x.Queue, x.State, x.Attempt})
	}
	return out, nil
}
func (h *handler) ack(ctx context.Context, in *ackInput) (*struct{}, error) {
	hash, err := h.authenticate(ctx, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(in.AlertID)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("identificador inválido")
	}
	if err = h.deps.Channels.AcknowledgeAlert(ctx, hash, id); err != nil {
		return nil, problem(err)
	}
	return &struct{}{}, nil
}
