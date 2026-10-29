package configuration

import "errors"

var (
	// ErrUnauthenticated means the session hash is missing, expired or revoked.
	ErrUnauthenticated = errors.New("não autenticado")
	// ErrForbidden means the membership is not an active administrator.
	ErrForbidden = errors.New("acesso negado")
	// ErrMFARequired means the session is not a confirmed full MFA session.
	ErrMFARequired = errors.New("conclua o MFA antes de configurar o canal")
	// ErrInvalid means the manual configuration breaks a local rule.
	ErrInvalid = errors.New("configuração inválida")
	// ErrConflict means the app, WABA or phone already belongs to a binding.
	ErrConflict = errors.New("identificador já vinculado")
	// ErrNotFound means the row is absent for this tenant.
	ErrNotFound = errors.New("registro não encontrado")
	// ErrDegraded means dispatch must stop until an administrator saves a new credential.
	ErrDegraded = errors.New("canal degradado")
	// ErrStaleVersion means a job was stamped with an older configuration version.
	ErrStaleVersion = errors.New("versão de configuração obsoleta")
	// ErrSecret means the ciphertext could not be opened for this tenant, entity and field.
	ErrSecret = errors.New("não foi possível ler o segredo")
	// ErrUnavailable means the write failed without a stable domain cause.
	ErrUnavailable = errors.New("não foi possível gravar a configuração")
)
