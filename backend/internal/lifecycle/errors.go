package lifecycle

import (
	"errors"

	"chatisp/internal/sectors"
)

var (
	// ErrForbidden is the sectors authorization sentinel.
	ErrForbidden = sectors.ErrForbidden
	// ErrNotFound means the conversation or the active snooze is absent.
	ErrNotFound = sectors.ErrNotFound
	// ErrInvalid rejects a malformed command or instant.
	ErrInvalid = errors.New("comando inválido")
	// ErrConflict is a stale version, a reused request or a second active snooze.
	ErrConflict = errors.New("conflito")
	// ErrUnavailable means the store is missing. SQL text is not wrapped into it.
	ErrUnavailable = errors.New("serviço indisponível")
)
