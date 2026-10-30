# Atendimento HTTP M2

O módulo `internal/attendance/httpapi` exporta `Register(api huma.API, deps Dependencies)`, com `Dependencies{Auth *auth.Service, Attendance service.Service, TrustedOrigins []string}`. O runtime global registra as operações e gera os artefatos OpenAPI/TypeScript. Sem banco ou autenticação, as operações continuam registradas e respondem 503.

A sessão é autenticada pelo cookie `__Host-session`; mutações exigem `Origin` em `TrustedOrigins` e `X-CSRF-Token`. A identidade e o tenant são derivados exclusivamente da sessão. Superadmin e sessões pendentes de inscrição MFA não acessam o atendimento. A autorização operacional é revalidada pelo serviço.

`GET /api/panel/inboxes` lista caixas do tenant acessíveis ao atendimento, inclusive canais desconectados, sem credenciais. O usuário e a sessão são autenticados em cada pedido; o vínculo operacional e o tenant são revalidados antes da listagem.

Prefixo `/api/panel/inboxes/{inbox_id}`:

- `GET /conversations?conversation_id=UUID`: snapshot consistente com `next_cursor`, `conversations` e `messages` da seleção opcional.
- `GET /conversations/{conversation_id}/history`: `next_cursor`, `head`, `messages` e `timeline`.
- `GET /events?after=N`: `next_cursor` e `events` autorizados da caixa; o cursor é o da faixa examinada, mesmo sem eventos visíveis. `409` com detalhe `ressincronização necessária` exige novo snapshot.
- `POST /conversations/{conversation_id}/assume`: `{expected_version}`; retorna conversa.
- `POST /conversations/{conversation_id}/messages`: `{expected_version, client_request_id, body}`; retorna 201 com `message_id`, `attempt_id`, `idempotency_id`, `state` e `replayed`. A fila local não implica aceitação ou entrega remota.
- `POST /conversations/{conversation_id}/close`: `{expected_version, client_request_id, reason, result, summary, farewell_body?, farewell_request_id?}`; retorna `conversation`, `closure`, `farewell` e `farewell_failed`. Falha da despedida não desfaz o encerramento.
- `POST /conversations/{conversation_id}/reopen`: `{expected_version, client_request_id}`; retorna conversa com protocolo preservado e novo ciclo.

Identificadores são UUIDs e datas são timestamps RFC3339. Os campos das estruturas e erros são descritos no OpenAPI gerado a partir dos DTOs Go. Repetição de chave com mesma intenção retorna o recurso; conflito de intenção ou versão obsoleta retorna 409. Erros sanitizados usam RFC 9457: 401 sessão, 403 autorização/CSRF/origem, 404 recurso, 409 conflito ou resync, 422 comando inválido e 503 indisponibilidade.
