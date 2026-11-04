// Package opsalert grava alertas operacionais do provedor a partir do estado
// já persistido: idade de job River, conector desconectado e webhook pendente.
// O destino é uma linha local lida pelo admin no painel. Não há e-mail, webhook
// externo, teste de carga nem restauração.
package opsalert
