package metacloud

import "errors"

var (
	errMode                 = errors.New("modo de endpoint inválido")
	errVersionMissing       = errors.New("versão da Graph API ausente")
	errVersionInvalid       = errors.New("versão da Graph API inválida")
	errDocMissing           = errors.New("documentação primária ausente")
	errDocInvalid           = errors.New("documentação primária inválida")
	errOfficialEndpoint     = errors.New("endpoint oficial exige HTTPS em graph.facebook.com")
	errSimulationEndpoint   = errors.New("simulação exige URL local de loopback")
	errPhoneMissing         = errors.New("número ausente")
	errPhoneInvalid         = errors.New("número inválido")
	errTokenMissing         = errors.New("token ausente")
	errTokenInvalid         = errors.New("token inválido")
	errDriverUnavailable    = errors.New("driver indisponível")
	errContextMissing       = errors.New("contexto ausente")
	errConnectorUnavailable = errors.New("conector indisponível")
	errTextInvalid          = errors.New("texto inválido")
	errAppMissing           = errors.New("aplicativo ausente")
	errServerUnavailable    = errors.New("servidor de simulação indisponível")
)
