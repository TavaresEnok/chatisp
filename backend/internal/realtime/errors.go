package realtime

import "errors"

var (
	// ErrUnauthorized indicates missing or invalid credentials.
	ErrUnauthorized = errors.New("não autenticado")

	// ErrForbidden indicates lack of permissions, deactivation or suspended tenant.
	ErrForbidden = errors.New("não autorizado")

	// ErrInboxForbidden indicates access to an inbox belonging to another tenant or nonexistent.
	ErrInboxForbidden = errors.New("caixa de entrada não permitida")

	// ErrOriginNotAllowed indicates an untrusted or missing Origin header.
	ErrOriginNotAllowed = errors.New("origem não permitida")

	// ErrInvalidToken indicates malformed session data.
	ErrInvalidToken = errors.New("token inválido")

	// ErrResyncRequired indicates cursor is behind retention floor and resync is needed.
	ErrResyncRequired = errors.New("ressincronização necessária")

	// ErrLoadLimitExceeded indicates query exceeded maximum volume ceiling before materialization.
	ErrLoadLimitExceeded = errors.New("limite de carga excedido")

	// ErrSlowClient indicates client buffer overflowed and connection was terminated.
	ErrSlowClient = errors.New("cliente lento desconectado")

	// ErrConnectionClosed indicates connection is already terminated.
	ErrConnectionClosed = errors.New("conexão encerrada")
)
