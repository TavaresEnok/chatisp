package triage

import "errors"

var (
	// ErrInvalid rejects a malformed menu, command or decision input.
	ErrInvalid = errors.New("triagem inválida")
	// ErrConflict is a stale version or a reused request id with another body.
	ErrConflict = errors.New("versão ou comando conflitante")
)
