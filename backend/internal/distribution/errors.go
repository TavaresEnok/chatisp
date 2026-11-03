package distribution

import (
	"errors"

	"chatisp/internal/sectors"
)

var (
	// ErrInvalid rejects a blank transaction, identifier or instant, or a threshold outside 0..604800.
	ErrInvalid = errors.New("comando inválido")
	// ErrForbidden is a tenant scope mismatch or a suspended tenant.
	ErrForbidden = sectors.ErrForbidden
	// ErrConflict is a stale settings version.
	ErrConflict = errors.New("conflito")
)
