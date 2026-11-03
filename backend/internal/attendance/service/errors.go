package service

import "errors"

var (
	// ErrForbidden means the principal has no live operational membership
	// in the tenant. A superadmin flag without that membership is included.
	// ReserveDispatch also returns it after committing a cancel or a definitive
	// failure when the persisted human author lost that membership. A sending
	// attempt is left in flight.
	ErrForbidden = errors.New("acesso negado")

	// ErrTenantInactive means the provider is not active. Writes and reads stop.
	ErrTenantInactive = errors.New("tenant inativo")

	// ErrInboxForbidden means the inbox is outside the tenant.
	ErrInboxForbidden = errors.New("caixa não permitida")

	// ErrChannelUnavailable means the inbox connector cannot dispatch.
	ErrChannelUnavailable = errors.New("canal indisponível")

	// ErrJobRequired means the outbox was not given a durable River job.
	// The transaction does not commit a send in that case.
	ErrJobRequired = errors.New("job de envio ausente")

	// ErrStaleEpoch means the attempt does not hold the conversation epoch.
	// ReserveDispatch commits a cancel or a definitive failure for a queued
	// attempt before returning this error. A sending attempt is left in flight.
	ErrStaleEpoch = errors.New("controller_epoch obsoleto")

	// ErrActiveConversation means another open attendance already owns the identity.
	ErrActiveConversation = errors.New("já existe conversa ativa para o contato nesta caixa")

	// ErrPolicyDenied means window, consent or template policy refused the send
	// before any network call. ReserveDispatch commits the local reclassification.
	ErrPolicyDenied = errors.New("política de canal recusou o envio")
)
