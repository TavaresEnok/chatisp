package httpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"chatisp/internal/realtime"
)

// registerRealtimeContract documents the Chi upgrade handler without registering
// a second HTTP route. The component schemas come from the runtime wire types.
func registerRealtimeContract(api huma.API) {
	doc := api.OpenAPI()
	client := doc.Components.Schemas.Schema(reflect.TypeOf(realtime.ClientMessage{}), true, "RealtimeClientMessage")
	server := doc.Components.Schemas.Schema(reflect.TypeOf(realtime.ServerMessage{}), true, "RealtimeServerMessage")
	doc.Paths["/api/panel/ws"] = &huma.PathItem{Get: &huma.Operation{
		OperationID: "attendance-websocket",
		Summary:     "Upgrade WebSocket de atendimento",
		Description: "Requer sessão operacional ativa e Origin confiável. Após o upgrade, mensagens seguem ClientMessage e ServerMessage; o cursor de replay é next_cursor.",
		Extensions:  map[string]any{"x-websocket": map[string]any{"clientMessage": client, "serverMessage": server}},
		Responses: map[string]*huma.Response{
			"101": {Description: "Upgrade WebSocket concluído"},
			"401": {Description: "Sessão ausente ou inválida"},
			"403": {Description: "Origem ou acesso negado"},
			"503": {Description: "Serviço indisponível"},
		},
	}}
}
