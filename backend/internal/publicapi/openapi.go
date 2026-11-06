package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// publicRoute is one operation of the public API. The same table registers the
// chi route and builds the OpenAPI document.
type publicRoute struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Scope       string
	Tag         string
	Limit       bool
	Body        bool
}

func publicRoutes() []publicRoute {
	return []publicRoute{
		{Method: http.MethodGet, Path: "/openapi.json", OperationID: "getPublicOpenAPI", Summary: "Documento OpenAPI desta API", Tag: "documento"},
		{Method: http.MethodGet, Path: "/v1/contacts", OperationID: "listContacts", Summary: "Lista contatos do provedor", Scope: "contacts.read", Tag: "contatos", Limit: true},
		{Method: http.MethodPost, Path: "/v1/contacts", OperationID: "createContact", Summary: "Cria um contato ou devolve o external_ref já existente", Scope: "contacts.write", Tag: "contatos", Body: true},
		{Method: http.MethodPatch, Path: "/v1/contacts/{id}", OperationID: "updateContact", Summary: "Atualiza nome, telefone, e-mail ou referência externa", Scope: "contacts.write", Tag: "contatos", Body: true},
		{Method: http.MethodGet, Path: "/v1/conversations", OperationID: "listConversations", Summary: "Lista conversas do provedor", Scope: "conversations.read", Tag: "conversas", Limit: true},
		{Method: http.MethodGet, Path: "/v1/conversations/{ref}", OperationID: "getConversation", Summary: "Lê uma conversa pelo UUID ou protocolo", Scope: "conversations.read", Tag: "conversas"},
		{Method: http.MethodGet, Path: "/v1/conversations/{ref}/messages", OperationID: "listMessages", Summary: "Lista mensagens públicas da conversa", Scope: "messages.read", Tag: "mensagens", Limit: true},
		{Method: http.MethodGet, Path: "/v1/conversations/{ref}/contact", OperationID: "getConversationContact", Summary: "Lê o contato vinculado à conversa", Scope: "contacts.read", Tag: "contatos"},
		{Method: http.MethodPost, Path: "/v1/conversations/{ref}/messages", OperationID: "sendMessage", Summary: "Envia uma mensagem de texto pelo atendimento", Scope: "conversations.write", Tag: "mensagens", Body: true},
		{Method: http.MethodPost, Path: "/v1/conversations/{ref}/assignment", OperationID: "assignConversation", Summary: "Atribui a conversa a um atendente do setor", Scope: "conversations.write", Tag: "conversas", Body: true},
		{Method: http.MethodPost, Path: "/v1/conversations/{ref}/state", OperationID: "setConversationState", Summary: "Encerra ou reabre a conversa pelo atendimento", Scope: "conversations.write", Tag: "conversas", Body: true},
		{Method: http.MethodPost, Path: "/v1/conversations/{ref}/template", OperationID: "sendTemplate", Summary: "Envia um template pelo fluxo de consentimento e outbox", Scope: "templates.send", Tag: "mensagens", Body: true},
		{Method: http.MethodGet, Path: "/v1/agents", OperationID: "listAgents", Summary: "Lista atendentes do provedor", Scope: "directory.read", Tag: "cadastros", Limit: true},
		{Method: http.MethodGet, Path: "/v1/sectors", OperationID: "listSectors", Summary: "Lista setores do provedor", Scope: "directory.read", Tag: "cadastros", Limit: true},
		{Method: http.MethodGet, Path: "/v1/inboxes", OperationID: "listInboxes", Summary: "Lista caixas do provedor", Scope: "directory.read", Tag: "cadastros", Limit: true},
	}
}

func registerPublic(router chi.Router, h *handler) {
	for _, route := range publicRoutes() {
		router.Method(route.Method, route.Path, http.HandlerFunc(h.dispatch(route.OperationID)))
	}
}

func (h *handler) dispatch(operationID string) http.HandlerFunc {
	switch operationID {
	case "getPublicOpenAPI":
		return h.openAPI
	case "listContacts":
		return h.listContacts
	case "createContact":
		return h.createContact
	case "updateContact":
		return h.updateContact
	case "listConversations":
		return h.listConversations
	case "getConversation":
		return h.conversation
	case "listMessages":
		return h.messages
	case "getConversationContact":
		return h.contact
	case "sendMessage":
		return h.sendMessage
	case "assignConversation":
		return h.assignConversation
	case "setConversationState":
		return h.setConversationState
	case "sendTemplate":
		return h.sendTemplate
	case "listAgents":
		return h.listAgents
	case "listSectors":
		return h.listSectors
	case "listInboxes":
		return h.listInboxes
	default:
		return func(w http.ResponseWriter, _ *http.Request) {
			writeProblem(w, http.StatusNotFound, "o caminho solicitado não existe")
		}
	}
}

func (h *handler) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(OpenAPIJSON())
}

type apiDocument struct {
	OpenAPI    string              `json:"openapi"`
	Info       docInfo             `json:"info"`
	Servers    []docServer         `json:"servers"`
	Tags       []docTag            `json:"tags"`
	Paths      map[string]pathItem `json:"paths"`
	Components docComponents       `json:"components"`
}

type docInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

type docServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type docTag struct {
	Name string `json:"name"`
}

type pathItem struct {
	Get   *opObject `json:"get,omitempty"`
	Post  *opObject `json:"post,omitempty"`
	Patch *opObject `json:"patch,omitempty"`
}

type opObject struct {
	OperationID   string                `json:"operationId"`
	Summary       string                `json:"summary"`
	Description   string                `json:"description,omitempty"`
	Tags          []string              `json:"tags"`
	Security      []map[string][]string `json:"security,omitempty"`
	Parameters    []docParam            `json:"parameters,omitempty"`
	RequestBody   *docBody              `json:"requestBody,omitempty"`
	Responses     map[string]docResp    `json:"responses"`
	RequiredScope string                `json:"x-required-scope,omitempty"`
}

type docParam struct {
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Required    bool      `json:"required"`
	Description string    `json:"description"`
	Schema      docSchema `json:"schema"`
}

type docBody struct {
	Required bool                  `json:"required"`
	Content  map[string]docContent `json:"content"`
}

type docContent struct {
	Schema docSchema `json:"schema"`
}

type docResp struct {
	Description string                `json:"description"`
	Content     map[string]docContent `json:"content,omitempty"`
}

type docSchema struct {
	Ref                  string               `json:"$ref,omitempty"`
	Type                 string               `json:"type,omitempty"`
	Format               string               `json:"format,omitempty"`
	Description          string               `json:"description,omitempty"`
	Nullable             bool                 `json:"nullable,omitempty"`
	MinLength            *int                 `json:"minLength,omitempty"`
	MaxLength            *int                 `json:"maxLength,omitempty"`
	Minimum              *int                 `json:"minimum,omitempty"`
	Maximum              *int                 `json:"maximum,omitempty"`
	Pattern              string               `json:"pattern,omitempty"`
	Required             []string             `json:"required,omitempty"`
	Properties           map[string]docSchema `json:"properties,omitempty"`
	Items                *docSchema           `json:"items,omitempty"`
	AdditionalProperties *bool                `json:"additionalProperties,omitempty"`
	Enum                 []string             `json:"enum,omitempty"`
}

type docComponents struct {
	SecuritySchemes map[string]docScheme `json:"securitySchemes"`
	Schemas         map[string]docSchema `json:"schemas"`
}

type docScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme"`
	BearerFormat string `json:"bearerFormat"`
}

// OpenAPIJSON is the public API document, indented, with a trailing newline.
func OpenAPIJSON() []byte {
	falseValue := false
	one := 1
	forty := 40
	eighty := 80
	hundred := 100
	oneTwentyEight := 128
	twoHundred := 200
	twoFiftySix := 256
	threeTwenty := 320
	fiveHundred := 500
	fourThousand := 4000
	sixteenThousand := 16000
	schemas := map[string]docSchema{
		"Problem": {
			Type: "object", Required: []string{"type", "title", "status", "detail"},
			AdditionalProperties: &falseValue,
			Properties: map[string]docSchema{
				"type":   {Type: "string"},
				"title":  {Type: "string"},
				"status": {Type: "integer"},
				"detail": {Type: "string"},
			},
		},
		"Contact": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"id", "name", "phone", "email", "blocked"},
			Properties: map[string]docSchema{
				"id":           {Type: "string", Format: "uuid"},
				"name":         {Type: "string", MaxLength: &twoHundred},
				"phone":        {Type: "string", Nullable: true, Description: "E.164 ou nulo"},
				"email":        {Type: "string", Nullable: true, MaxLength: &threeTwenty},
				"blocked":      {Type: "boolean"},
				"external_ref": {Type: "string", MaxLength: &oneTwentyEight, Description: "Ausente quando o contato não tem referência externa"},
			},
		},
		"ContactCreate": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"name"},
			Properties: map[string]docSchema{
				"name":         {Type: "string", MinLength: &one, MaxLength: &twoHundred},
				"phone":        {Type: "string", Pattern: `^\+[1-9][0-9]{7,14}$`, Description: "E.164. Omita para gravar nulo."},
				"email":        {Type: "string", MinLength: intPtr(3), MaxLength: &threeTwenty},
				"external_ref": {Type: "string", MinLength: &one, MaxLength: &oneTwentyEight, Description: "Sem espaço. Repetir devolve o contato existente."},
			},
		},
		"ContactUpdate": {
			Type: "object", AdditionalProperties: &falseValue,
			Description: "Informe ao menos um campo. Texto vazio em phone, email ou external_ref grava nulo. Documento e observações não são aceitos.",
			Properties: map[string]docSchema{
				"name":         {Type: "string", MinLength: &one, MaxLength: &twoHundred},
				"phone":        {Type: "string", Description: "E.164 ou vazio para limpar."},
				"email":        {Type: "string", MaxLength: &threeTwenty},
				"external_ref": {Type: "string", MaxLength: &oneTwentyEight, Description: "Sem espaço. Vazio limpa. Outro contato com a mesma referência responde 409."},
			},
		},
		"ContactList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"contacts"},
			Properties: map[string]docSchema{
				"contacts": {Type: "array", Items: ref("Contact")},
			},
		},
		"Conversation": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"id", "protocol", "status", "inbox_id", "contact_id", "controller", "cycle_number", "version", "created_at", "updated_at"},
			Properties: map[string]docSchema{
				"id":                {Type: "string", Format: "uuid"},
				"protocol":          {Type: "string"},
				"status":            {Type: "string"},
				"inbox_id":          {Type: "string", Format: "uuid"},
				"contact_id":        {Type: "string", Format: "uuid"},
				"sector_id":         {Type: "string", Format: "uuid", Nullable: true},
				"assignee_id":       {Type: "string", Format: "uuid", Nullable: true},
				"controller":        {Type: "string"},
				"cycle_number":      {Type: "integer", Minimum: &one},
				"version":           {Type: "integer", Minimum: &one, Description: "Precondição expected_version do texto, da atribuição, do estado e do template."},
				"external_protocol": {Type: "string", Nullable: true},
				"created_at":        {Type: "string", Format: "date-time"},
				"updated_at":        {Type: "string", Format: "date-time"},
			},
		},
		"ConversationList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"conversations"},
			Properties: map[string]docSchema{"conversations": {Type: "array", Items: ref("Conversation")}},
		},
		"Message": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"id", "direction", "origin", "body", "ingested_at"},
			Properties: map[string]docSchema{
				"id":          {Type: "string", Format: "uuid"},
				"direction":   {Type: "string", Enum: []string{"inbound", "outbound"}},
				"origin":      {Type: "string"},
				"body":        {Type: "string"},
				"ingested_at": {Type: "string", Format: "date-time"},
				"trusted_at":  {Type: "string", Format: "date-time", Nullable: true},
			},
		},
		"MessageList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"conversation_id", "messages"},
			Properties: map[string]docSchema{
				"conversation_id": {Type: "string", Format: "uuid"},
				"messages":        {Type: "array", Items: ref("Message")},
			},
		},
		"TextSend": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"body", "client_request_id", "expected_version"},
			Properties: map[string]docSchema{
				"body":              {Type: "string", MinLength: &one, MaxLength: &sixteenThousand, Description: "Texto livre em UTF-8. O texto final, incluindo a assinatura do atendente aplicada pelo atendimento, tem limite de 16000 bytes; o excesso é recusado com HTTP 400."},
				"client_request_id": {Type: "string", MinLength: &one, MaxLength: &twoHundred, Description: "Idempotência do token e do tenant. Outro token e o painel não compartilham esta chave."},
				"expected_version":  {Type: "integer", Minimum: &one, Description: "version lida em GET da conversa."},
			},
		},
		"Assignment": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"assignee_id", "client_request_id", "expected_version"},
			Properties: map[string]docSchema{
				"assignee_id":       {Type: "string", Format: "uuid", Description: "Membership ativa do setor da conversa."},
				"client_request_id": {Type: "string", MinLength: &one, MaxLength: &twoHundred},
				"expected_version":  {Type: "integer", Minimum: &one},
			},
		},
		"ConversationState": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"status", "client_request_id", "expected_version"},
			Properties: map[string]docSchema{
				"status":            {Type: "string", Enum: []string{"open", "closed"}, Description: "closed encerra o ciclo. open reabre um ciclo encerrado."},
				"reason":            {Type: "string", MaxLength: &fiveHundred, Description: "Obrigatório quando status é closed."},
				"result":            {Type: "string", Enum: []string{"resolved", "no_response", "referred_external", "spam"}, Description: "Obrigatório quando status é closed."},
				"summary":           {Type: "string", MaxLength: &fourThousand, Description: "Obrigatório quando status é closed."},
				"client_request_id": {Type: "string", MinLength: &one, MaxLength: &twoHundred},
				"expected_version":  {Type: "integer", Minimum: &one},
			},
		},
		"TemplateSend": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"template_id", "language", "client_request_id", "expected_version"},
			Properties: map[string]docSchema{
				"template_id":       {Type: "string", MinLength: &one, MaxLength: &twoFiftySix, Description: "external_id do template aprovado pelo driver."},
				"language":          {Type: "string", MinLength: &one, MaxLength: &forty},
				"purpose":           {Type: "string", MaxLength: &eighty, Description: "Finalidade do consentimento. Vazio usa atendimento."},
				"client_request_id": {Type: "string", MinLength: &one, MaxLength: &twoHundred, Description: "Idempotência estável do token e do tenant. Outro token e o painel não compartilham esta chave."},
				"expected_version":  {Type: "integer", Minimum: &one, Description: "version lida em GET da conversa."},
			},
		},
		"TemplateResult": {
			Type: "object", AdditionalProperties: &falseValue,
			Required: []string{"message_id", "attempt_id", "job_id", "replayed"},
			Properties: map[string]docSchema{
				"message_id": {Type: "string", Format: "uuid"},
				"attempt_id": {Type: "string", Format: "uuid"},
				"job_id":     {Type: "integer", Minimum: &one},
				"replayed":   {Type: "boolean"},
			},
		},
		"Agent": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"id", "role", "status"},
			Properties: map[string]docSchema{
				"id": {Type: "string", Format: "uuid"}, "role": {Type: "string"}, "status": {Type: "string"},
			},
		},
		"AgentList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"agents"},
			Properties: map[string]docSchema{"agents": {Type: "array", Items: ref("Agent")}},
		},
		"Sector": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"id", "name", "active"},
			Properties: map[string]docSchema{
				"id": {Type: "string", Format: "uuid"}, "name": {Type: "string"}, "active": {Type: "boolean"},
			},
		},
		"SectorList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"sectors"},
			Properties: map[string]docSchema{"sectors": {Type: "array", Items: ref("Sector")}},
		},
		"Inbox": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"id", "name"},
			Properties: map[string]docSchema{
				"id": {Type: "string", Format: "uuid"}, "name": {Type: "string"},
			},
		},
		"InboxList": {
			Type: "object", AdditionalProperties: &falseValue, Required: []string{"inboxes"},
			Properties: map[string]docSchema{"inboxes": {Type: "array", Items: ref("Inbox")}},
		},
	}
	doc := apiDocument{
		OpenAPI: "3.0.3",
		Info: docInfo{
			Title:       "Chat ISP API pública",
			Version:     "1.0.0",
			Description: "Leitura de conversas, contatos e cadastros, criação e atualização de contatos, envio de texto, atribuição, mudança de estado e envio individual de template. O Bearer é um token de API. O documento do painel não inclui estas rotas. A atualização de contato não grava documento nem observações. Texto, atribuição e estado reutilizam o atendimento do painel.",
		},
		Servers: []docServer{{URL: "/api/public", Description: "Prefixo montado"}},
		Tags: []docTag{
			{Name: "documento"}, {Name: "contatos"}, {Name: "conversas"}, {Name: "mensagens"}, {Name: "cadastros"},
		},
		Paths: map[string]pathItem{},
		Components: docComponents{
			SecuritySchemes: map[string]docScheme{
				"bearer": {Type: "http", Scheme: "bearer", BearerFormat: "token"},
			},
			Schemas: schemas,
		},
	}
	for _, route := range publicRoutes() {
		item := doc.Paths[route.Path]
		op := &opObject{
			OperationID: route.OperationID,
			Summary:     route.Summary,
			Tags:        []string{route.Tag},
			Responses:   map[string]docResp{},
		}
		if route.Scope != "" {
			op.RequiredScope = route.Scope
			op.Description = "Escopo exigido: " + route.Scope + "."
			op.Security = []map[string][]string{{"bearer": {}}}
			op.Responses["401"] = problemResp("Credencial ausente, inválida ou revogada")
			op.Responses["403"] = problemResp("Escopo ausente ou provedor suspenso")
			op.Responses["429"] = problemResp("Limite do token ou do provedor")
		}
		if containsRef(route.Path) {
			op.Parameters = append(op.Parameters, docParam{
				Name: "ref", In: "path", Required: true,
				Description: "UUID da conversa ou protocolo",
				Schema:      docSchema{Type: "string", MinLength: &one, MaxLength: intPtr(64)},
			})
			op.Responses["400"] = problemResp("Identificador inválido")
			op.Responses["404"] = problemResp("Conversa não encontrada")
		}
		if route.Limit {
			op.Parameters = append(op.Parameters, docParam{
				Name: "limit", In: "query", Required: false,
				Description: "Quantidade, de 1 a 100. O padrão é 50.",
				Schema:      docSchema{Type: "integer", Minimum: &one, Maximum: &hundred},
			})
			op.Responses["400"] = problemResp("Limite inválido")
		}
		switch route.OperationID {
		case "getPublicOpenAPI":
			op.Responses["200"] = docResp{Description: "Documento OpenAPI", Content: jsonContent(docSchema{Type: "object"})}
		case "listContacts":
			op.Responses["200"] = jsonResp("Contatos", "ContactList")
		case "createContact":
			op.RequestBody = bodyRef("ContactCreate")
			op.Responses["201"] = jsonResp("Contato criado", "Contact")
			op.Responses["200"] = jsonResp("Contato já existente para o external_ref", "Contact")
			op.Responses["400"] = problemResp("Contato inválido")
		case "updateContact":
			op.Parameters = append(op.Parameters, docParam{
				Name: "id", In: "path", Required: true,
				Description: "UUID do contato",
				Schema:      docSchema{Type: "string", Format: "uuid"},
			})
			op.RequestBody = bodyRef("ContactUpdate")
			op.Responses["200"] = jsonResp("Contato atualizado", "Contact")
			op.Responses["400"] = problemResp("Contato inválido")
			op.Responses["404"] = problemResp("Contato não encontrado")
			op.Responses["409"] = problemResp("Referência externa já usada ou titular excluído")
		case "listConversations":
			op.Responses["200"] = jsonResp("Conversas, cada uma com version", "ConversationList")
		case "getConversation":
			op.Responses["200"] = jsonResp("Conversa, incluindo version para o envio", "Conversation")
		case "listMessages":
			op.Responses["200"] = jsonResp("Mensagens públicas", "MessageList")
		case "getConversationContact":
			op.Responses["200"] = jsonResp("Contato da conversa", "Contact")
			op.Responses["404"] = problemResp("Conversa ou contato não encontrado")
		case "sendMessage":
			op.Description = "Escopo exigido: conversations.write. O texto segue Service.Send do atendimento, em nome do atendente atual da conversa. A chave é do token e do tenant. A janela de 24 horas e a assinatura do painel são as mesmas. O texto com assinatura não pode exceder 16000 bytes UTF-8; excesso responde 400. O corpo JSON tem limite de 128 KiB. Não envia mídia nem template."
			op.RequestBody = bodyRef("TextSend")
			op.Responses["201"] = jsonResp("Texto aceito na outbox", "TemplateResult")
			op.Responses["200"] = jsonResp("Replay da mesma chave e do mesmo texto", "TemplateResult")
			op.Responses["400"] = problemResp("Mensagem inválida")
			op.Responses["409"] = problemResp("Versão desatualizada, conversa encerrada ou chave reutilizada com outro texto")
			op.Responses["422"] = problemResp("Janela fechada, política recusada ou conversa sem atendente")
			op.Responses["503"] = problemResp("Serviço de atendimento indisponível")
		case "assignConversation":
			op.Description = "Escopo exigido: conversations.write. A atribuição segue TransferToMember do atendimento. O ator operacional é um administrador ativo do provedor, porque o token não tem sessão. O destino precisa estar no setor da conversa. A mesma chave devolve a mesma atribuição."
			op.RequestBody = bodyRef("Assignment")
			op.Responses["200"] = jsonResp("Conversa atribuída", "Conversation")
			op.Responses["400"] = problemResp("Atribuição inválida")
			op.Responses["409"] = problemResp("Versão desatualizada, conversa encerrada, capacidade esgotada ou chave reutilizada com outro destino")
			op.Responses["422"] = problemResp("Comando recusado")
			op.Responses["503"] = problemResp("Serviço de atendimento indisponível")
		case "setConversationState":
			op.Description = "Escopo exigido: conversations.write. closed segue Close e open segue Reopen, sem mensagem de despedida. O ator operacional é um administrador ativo do provedor. reason, result e summary são obrigatórios só no encerramento."
			op.RequestBody = bodyRef("ConversationState")
			op.Responses["200"] = jsonResp("Conversa no estado pedido", "Conversation")
			op.Responses["400"] = problemResp("Estado inválido")
			op.Responses["409"] = problemResp("Versão desatualizada, transição recusada ou chave reutilizada com outro conteúdo")
			op.Responses["422"] = problemResp("Comando recusado")
			op.Responses["503"] = problemResp("Serviço de atendimento indisponível")
		case "sendTemplate":
			op.Description = "Escopo exigido: templates.send. A idempotência é do token e do tenant e não se mistura com outro token nem com o painel. expected_version vem do campo version da conversa. Consentimento e template aprovado são revalidados no mesmo fluxo do atendimento."
			op.RequestBody = bodyRef("TemplateSend")
			op.Responses["201"] = jsonResp("Template aceito na outbox", "TemplateResult")
			op.Responses["200"] = jsonResp("Replay da mesma chave e do mesmo conteúdo", "TemplateResult")
			op.Responses["400"] = problemResp("Template inválido")
			op.Responses["409"] = problemResp("Versão desatualizada, conversa encerrada ou chave reutilizada com outro conteúdo")
			op.Responses["422"] = problemResp("Consentimento ausente, template não autorizado ou conversa sem atendente")
			op.Responses["503"] = problemResp("Serviço de atendimento indisponível")
		case "listAgents":
			op.Responses["200"] = jsonResp("Atendentes", "AgentList")
		case "listSectors":
			op.Responses["200"] = jsonResp("Setores", "SectorList")
		case "listInboxes":
			op.Responses["200"] = jsonResp("Caixas", "InboxList")
		}
		switch route.Method {
		case http.MethodPost:
			item.Post = op
		case http.MethodPatch:
			item.Patch = op
		default:
			item.Get = op
		}
		doc.Paths[route.Path] = item
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
	return buf.Bytes()
}

func ref(name string) *docSchema {
	return &docSchema{Ref: "#/components/schemas/" + name}
}

func bodyRef(name string) *docBody {
	return &docBody{Required: true, Content: jsonContent(*ref(name))}
}

func jsonResp(description, schema string) docResp {
	return docResp{Description: description, Content: jsonContent(*ref(schema))}
}

func problemResp(description string) docResp {
	return docResp{
		Description: description,
		Content: map[string]docContent{
			"application/problem+json": {Schema: *ref("Problem")},
		},
	}
}

func jsonContent(schema docSchema) map[string]docContent {
	return map[string]docContent{"application/json": {Schema: schema}}
}

func intPtr(n int) *int { return &n }

func containsRef(path string) bool {
	for i := 0; i+5 <= len(path); i++ {
		if path[i:i+5] == "{ref}" {
			return true
		}
	}
	return false
}
