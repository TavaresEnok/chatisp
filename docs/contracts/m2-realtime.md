# Contrato M2 — serviço de tempo real e WebSocket

Pacote `backend/internal/realtime`. Ele implementa o servidor de tempo real via WebSocket para o painel de atendimento sobre `attendance/service`, `attendance/store` e o subsistema de autenticação. É um pacote independente com construtor `NewHandler(Options) *Handler` que implementa `http.Handler`. O wiring HTTP global nas rotas do painel fica para `m2-http-runtime`. Dependência vendorizada direta: `github.com/coder/websocket` `v1.8.15`.

Fonte de produto: `docs/especificacao.md` V4.1 B.4.5, B.4.10 e B.8; `docs/plano.md` M2 (11-websocket, 14-realtime, 14-notify, 14-autorizacao, 03-tempo-real). Não há chamada a Meta, Evolution ou ERP.

## Quem chama

| Operação | Quem | Mecanismo |
| --- | --- | --- |
| Handshake e Upgrade WebSocket | Navegador / Painel | `GET` HTTP com upgrade WebSocket e cookie `__Host-session` |
| `subscribe`, `snapshot`, `replay`, `ping` | Cliente WebSocket | Mensagens JSON no socket aberto |
| Emissão de sinal NOTIFY | Workers / Banco / Testes | `SELECT pg_notify('chatisp_realtime_events', tenant_id)` |

## Autenticação, Origin e Contrato de Sessão

O handshake HTTP valida estritamente a política de segurança do painel antes do upgrade:

1. **Origin permitido**: O cabeçalho `Origin` é obrigatório. Requisições sem `Origin` ou com origem fora de `TrustedOrigins` são rejeitadas imediatamente com `HTTP 403 Forbidden`.
2. **Sem Bearer**: O cabeçalho `Authorization` é expressamente proibido para o WebSocket do painel. Qualquer tentativa com `Authorization` presente é rejeitada com `HTTP 401 Unauthorized`.
3. **Contrato de Cookies**: Apenas os cookies do painel (`__Host-session` e opcionalmente `__Host-csrf`) são aceitos. A presença de cookies alheios ao contrato resulta em `HTTP 403 Forbidden`.
4. **Sessão existente e íntegra**: O cookie `__Host-session` é autenticado contra o banco via `Authenticator.AuthenticateSession`. A sessão deve ser ativa e não expirada; o usuário, a membership e o provedor (tenant) devem estar `active`. Papéis permitidos são `admin`, `supervisor` e `agent`. Sessões em fase de cadastro (`EnrollmentOnly`) são rejeitadas com `HTTP 403 Forbidden`.

## Revalidação Contínua, Revogação e Interrupção Imediata

O socket não assume que a autorização inicial seja eterna e protege contra entregas a sessões revogadas:

- **Revalidação Periódica e em Operações**: A cada intervalo configurável (`RevalidateInterval`, padrão 5s) e a cada mensagem recebida (`subscribe`, `snapshot`, `replay`), o servidor revalida a sessão, usuário, vínculo e tenant no banco.
- **Revalidação no Consumo da Fila de Saída**: O `writePump` revalida a autorização imediatamente antes de entregar payloads de negócio (`snapshot` ou `events`). Se a autorização foi revogada ou suspensa enquanto havia mensagens enfileiradas na outbox, a fila pendente é descartada e a conexão é encerrada imediatamente com código 1008 (`StatusPolicyViolation`, `authorization_revoked`). Nenhuma mensagem subsequente vaza para o cliente revogado.
- **Suspensão de Usuário, Vínculo ou Tenant**: Qualquer alteração para status não-ativo (`disabled`, `suspended`, `blocked`) corta imediatamente a conexão com código 1008.

## Consistência de Snapshot, next_cursor e Replay Concorrente

- **Serialização de Operações e Fronteira Ordenada**: Todas as operações de leitura de snapshot, troca de subscrição, replay, alteração de cursor e consulta/enfileiramento de eventos são estritamente serializadas por mutex de operação (`opMu`). Um snapshot nunca é intercalado ou ultrapassado por consultas concorrentes de eventos, garantindo consistência total do cursor.
- **Snapshot Inicial Determinístico**: O snapshot inicial é executado e enfileirado antes de ativar o stream de eventos, evitando corrida entre o snapshot e notificações prematuras.
- **Deduplicação Delimitada por Subscrição e Destino**: A deduplicação por chave `(id, version)` só registra eventos efetivamente filtrados e destinados à caixa ativa (`isEventForInbox == true`). Eventos pertencentes a outras caixas não poluem a deduplicação. Ao trocar de caixa ou executar replay, o estado de deduplicação é resetado, permitindo recuperação completa e sem perdas ao alternar entre caixas ou reconsultar intervalos anteriores.
- **Resolução de Caixa Resiliente a Erros Transitórios**: A verificação de caixa de uma conversa distingue negação definitiva (`ErrInboxForbidden`, `ErrNotFound` -> invisível) de falhas operacionais do banco de dados (erros de conexão/timeout). Em caso de erro operacional transitório, a fronteira do cursor é preservada antes do evento problemático e o evento não é descartado nem registrado no dedup, permitindo reexecução segura no próximo ciclo de polling/notificação. Na ausência de `InboxResolver` configurado ou quando a caixa de uma conversa não puder ser determinada, a entrega do evento é expressamente negada para a subscrição (`isEventForInbox == false`), assegurando isolamento estrito entre caixas do mesmo tenant sem vazamentos.
- **Gap de Retenção e Resync Explícito**: Se o cursor requisitado for mais antigo que o piso de expurgo (`retain_after`), o serviço retorna `store.ErrResync`. O servidor envia uma mensagem explícita `{type: "resync", reason: "retention_gap"}`. Replay parcial nunca é disfarçado de sucesso.

## Memória Limitada e Proteção contra Cargas Excessivas

- **Cache de Conversas Limitado**: O mapeamento de conversa para caixa (`convInbox`) utiliza estrutura limitada (`boundedCache`, padrão 512 itens) com política FIFO/evicção, impedindo crescimento descontrolado de memória em conexões de longa duração.
- **Teto de Carga e Fronteira Paginada com o Serviço Integrado**: Consultas ao banco na fronteira de atendimento (`BoundedAttendance`) aplicam limites na origem SQL antes da materialização em memória. Lotes de eventos são limitados a no máximo 500 itens por consulta (`LIMIT 500`). Se a faixa pesquisada contiver mais de 500 eventos, o servidor examina e entrega a página limitada com `next_cursor` correspondente ao último item examinado da página, distinguindo a página examinada do restante pendente e permitindo que o cliente/stream continue a recuperação de forma paginada sem sobrecarga de memória. Consultas de snapshot aplicam `LIMIT 501` em conversas e `LIMIT 1001` em mensagens; caso o teto seja excedido, a consulta aborta antes de materializar o histórico completo e emite uma mensagem explícita `{type: "resync", reason: "load_limit_exceeded"}` com o cursor coerente.
- **Backpressure e Cliente Lento**: A fila de saída de cada conexão é estritamente limitada (`BufferSize`, padrão 64). Clientes que não consomem suas mensagens provocam encerramento imediato com código 1008 (`slow_client`) e drenagem de memória pendente sem vazamento de goroutines.

## NOTIFY Mínimo e Polling Seguro

- **Listener Dedicado**: `PGNotifier` mantém uma conexão dedicada no pool executando `LISTEN chatisp_realtime_events`.
- **Sinal Mínimo**: O NOTIFY carrega apenas o identificador mínimo do tenant (`tenant_id`), sem corpo nem dados de eventos.
- **Consulta Durável**: O sinal apenas acorda os ouvintes registrados daquele tenant. A recuperação de eventos sempre consulta o armazenamento durável (`realtime_events` via `AttendanceService.Events`).
- **Polling de Segurança**: Além do NOTIFY, cada conexão possui um temporizador independente (`PollInterval`, padrão 500ms). Se o sinal do PostgreSQL for perdido ou descartado por sobrecarga de fila, o polling seguro garante a recuperação de todos os eventos confirmados.

## Protocolo de Mensagens JSON

### Cliente → Servidor

```json
{"action": "subscribe", "inbox_id": "uuid", "since_seq": 10, "with_snapshot": true}
{"action": "snapshot", "inbox_id": "uuid", "conversation_id": "uuid"}
{"action": "replay", "inbox_id": "uuid", "after": 10}
{"action": "ping"}
```

### Servidor → Cliente

```json
{"type": "snapshot", "inbox_id": "uuid", "next_cursor": 15, "conversations": [...], "messages": [...]}
{"type": "events", "inbox_id": "uuid", "next_cursor": 18, "events": [...]}
{"type": "resync", "inbox_id": "uuid", "reason": "retention_gap", "next_cursor": 18}
{"type": "resync", "inbox_id": "uuid", "reason": "load_limit_exceeded", "next_cursor": 18}
{"type": "error", "code": "inbox_forbidden", "message": "caixa não permitida"}
{"type": "pong"}
```
