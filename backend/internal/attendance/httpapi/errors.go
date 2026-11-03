package httpapi

import (
	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
	"errors"
	"github.com/danielgtaylor/huma/v2"
)

func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired), errors.Is(err, service.ErrForbidden), errors.Is(err, service.ErrInboxForbidden), errors.Is(err, service.ErrTenantInactive):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("registro não encontrado")
	case errors.Is(err, store.ErrResync):
		return huma.Error409Conflict("ressincronização necessária")
	case errors.Is(err, store.ErrStaleVersion), errors.Is(err, store.ErrIdempotencyConflict), errors.Is(err, store.ErrClosed), errors.Is(err, store.ErrCapacityFull), errors.Is(err, service.ErrStaleEpoch), errors.Is(err, service.ErrActiveConversation), errors.Is(err, service.ErrChannelUnavailable):
		return huma.Error409Conflict("conflito de atendimento")
	case errors.Is(err, store.ErrInvalid):
		return huma.Error422UnprocessableEntity("comando inválido")
	case errors.Is(err, service.ErrPolicyDenied):
		return huma.Error422UnprocessableEntity("política de canal recusou o envio")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
