package presence

import (
	"errors"

	"chatisp/internal/sectors"
)

var (
	// ErrForbidden is the sectors authorization sentinel.
	ErrForbidden = sectors.ErrForbidden
	// ErrNotFound means the sector is missing or inactive for the HTTP list.
	ErrNotFound = sectors.ErrNotFound
	// ErrInvalid rejects malformed commands and instants.
	ErrInvalid = errors.New("comando inválido")
	// ErrConflict is a stale version or a reused request id with another body.
	ErrConflict = errors.New("conflito")
	// ErrUnavailable is a missing store. SQL details stay on the concrete error.
	ErrUnavailable = errors.New("serviço indisponível")
)
