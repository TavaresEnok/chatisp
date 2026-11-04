package httpapi

import (
	"chatisp/internal/attachments"
	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/media"
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
	case errors.Is(err, media.ErrFFmpegMissing), errors.Is(err, media.ErrConvert):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, attachments.ErrLimit):
		return huma.Error422UnprocessableEntity("limite de armazenamento de anexos")
	case errors.Is(err, media.ErrStorage):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, service.ErrPolicyDenied):
		detail := "política de canal recusou o envio"
		var denied *service.PolicyDenied
		if errors.As(err, &denied) && denied != nil && denied.Reason != "" {
			detail += ": " + denied.Reason
		}
		return huma.Error422UnprocessableEntity(detail)
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
