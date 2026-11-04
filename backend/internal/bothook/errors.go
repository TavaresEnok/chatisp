package bothook

import "errors"

var (
	ErrUnavailable     = errors.New("serviço indisponível")
	ErrForbidden       = errors.New("acesso negado")
	ErrNotFound        = errors.New("gancho não encontrado")
	ErrConflict        = errors.New("conflito")
	ErrInvalid         = errors.New("comando inválido")
	ErrDisabled        = errors.New("gancho desligado")
	ErrEpoch           = errors.New("epoch obsoleta")
	ErrUnauthenticated = errors.New("token inválido")
)
