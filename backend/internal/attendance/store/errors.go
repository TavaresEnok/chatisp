package store

import "errors"

var (
	// ErrTenantContextRequired means the transaction has no app.tenant_id
	// or the argument does not match that setting.
	ErrTenantContextRequired = errors.New("contexto de tenant ausente")

	// ErrIngressContextRequired means the transaction is not the ingress role
	// with app.access_scope=ingress.
	ErrIngressContextRequired = errors.New("contexto de ingresso ausente")

	// ErrNotFound means the row is absent under the current tenant.
	ErrNotFound = errors.New("registro não encontrado")

	// ErrStaleVersion means the conversation version changed.
	ErrStaleVersion = errors.New("versão obsoleta da conversa")

	// ErrIdempotencyConflict means the same key was reused with other bytes.
	ErrIdempotencyConflict = errors.New("conflito de idempotência")

	// ErrInvalid means the command breaks a local rule.
	ErrInvalid = errors.New("comando inválido")

	// ErrClosed means the attendance is already closed.
	ErrClosed = errors.New("conversa encerrada")

	// ErrLeaseHeld means another owner still holds the send lease.
	ErrLeaseHeld = errors.New("lease de envio indisponível")

	// ErrCapacity means the tenant has no free job reservation.
	ErrCapacity = errors.New("reserva de concorrência esgotada")

	// ErrResync means retained events no longer cover the requested cursor.
	ErrResync = errors.New("ressincronização explícita")

	// ErrUncertain means an uncertain attempt was asked to send again.
	ErrUncertain = errors.New("envio incerto não se repete")
)
