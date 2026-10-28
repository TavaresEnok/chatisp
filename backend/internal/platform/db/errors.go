package db

import "errors"

var (
	// ErrTenantContextRequired is returned when domain access runs outside WithTenant.
	ErrTenantContextRequired = errors.New("contexto de tenant ausente")

	// ErrPlatformContextRequired is returned when platform access runs outside WithPlatform.
	ErrPlatformContextRequired = errors.New("contexto de plataforma ausente")

	// ErrAuthContextRequired is returned when credential access runs outside WithAuth.
	ErrAuthContextRequired = errors.New("contexto de autenticação ausente")

	// ErrInvalidPasswordHash is returned when a credential hash is empty or too large.
	ErrInvalidPasswordHash = errors.New("hash de senha inválido")

	ErrInvalidTimezone         = errors.New("fuso horário inválido")
	ErrInvalidTenantStatus     = errors.New("status de tenant inválido")
	ErrInvalidLimits           = errors.New("limites do tenant inválidos")
	ErrInvalidEmail            = errors.New("e-mail inválido")
	ErrInvalidUserStatus       = errors.New("status de usuário inválido")
	ErrInvalidMembershipRole   = errors.New("papel de membership inválido")
	ErrInvalidMembershipStatus = errors.New("status de membership inválido")
	ErrInvalidAudit            = errors.New("registro de auditoria inválido")
	ErrNotFound                = errors.New("registro não encontrado")
)
