package pipeline

import "errors"

var (
	// ErrUnavailable indicates an uninitialized or closed pipeline component.
	ErrUnavailable = errors.New("pipeline indisponível")
	// ErrInvalid indicates invalid configuration, arguments or payload format.
	ErrInvalid = errors.New("parâmetro inválido")
	// ErrNotFound indicates a missing delivery, route, attempt or item.
	ErrNotFound = errors.New("recurso não encontrado")
	// ErrForbidden indicates authorization failure or revoked permission.
	ErrForbidden = errors.New("acesso não autorizado")
	// ErrSimulationOnly indicates a test double or fake was requested in official mode.
	ErrSimulationOnly = errors.New("driver simulado permitido apenas em modo simulação")
	// ErrDriverNotFound indicates no driver was available for the connector.
	ErrDriverNotFound = errors.New("driver de envio não encontrado")
	// ErrLeaseHeld indicates the inbox send lease is currently owned by another worker.
	ErrLeaseHeld = errors.New("posse de envio ocupada por outro worker")
	// ErrCapacity indicates the tenant's worker queue capacity has been reached.
	ErrCapacity = errors.New("capacidade da fila esgotada para o provedor")
	// ErrAppUnresolved indicates the Meta app could not be resolved from trusted config.
	ErrAppUnresolved = errors.New("aplicativo Meta não configurado ou inativo")
	// ErrVerifyTokenMismatch indicates the verification token does not match.
	ErrVerifyTokenMismatch = errors.New("token de verificação não corresponde")
	// ErrPersistFailed indicates a failure during atomic delivery persistence.
	ErrPersistFailed = errors.New("falha na persistência do envelope")
)
