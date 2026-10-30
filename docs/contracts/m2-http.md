# Contrato HTTP, Runtime e Tempo Real — Marco M2

Este documento estabelece o contrato canônico da camada HTTP e de execução global do Chat ISP no Marco M2.

O runtime global integra:
1. **API Operacional Huma v2** (montada sob o router Chi):
   - Atendimento vertical (`chatisp/internal/attendance/httpapi`).
   - Gerenciamento e inspeção de canais Meta (`chatisp/internal/channel/httpapi`).
   - Identidade e administração do painel (`chatisp/internal/platform/httpapi`).
2. **Endpoints Brutos Chi**:
   - **Realtime WebSocket**: `/api/panel/realtime` (e `/api/panel/ws`).
   - **Meta Webhook**: `/api/webhooks/meta` (e `/webhooks/meta`, `/meta/webhook`).
3. **Métricas e Lifecycle**:
   - `/healthz` e `/readyz` na porta HTTP principal (`CHATISP_HTTP_ADDR`).
   - `/metrics` isolado no listener administrativo Prometheus (`CHATISP_METRICS_ADDR`).

---

## 1. Segurança e Autenticação

- **Sessão do Painel**: Autenticação via cookie seguro `__Host-session`. O tenant e a associação (`membership_id`) são derivados exclusivamente do registro de sessão ativo no banco de dados.
- **CSRF e Origem Confiável**: Todas as operações mutadoras (`POST`, `PUT`, `DELETE`) exigem:
  - Header `Origin` pertencente estritamente a `CHATISP_PANEL_ORIGINS`.
  - Header `X-CSRF-Token` correspondente ao token de sessão ativo.
- **Segregação de Papéis**:
  - Painel de atendimento: papéis `admin`, `supervisor` e `agent`.
  - Configuração de canais: papel `admin`.
  - `superadmin`: bloqueado de rotas operacionais de atendimento e canais (retorna `403 Forbidden`).
  - Sessões em inscrição inicial de MFA (`enrollment_only`): bloqueadas (retorna `403 Forbidden`).
- **Problemas RFC 9457**:
  - Respostas de erro retornam `Content-Type: application/problem+json`.
  - Códigos padronizados:
    - `401 Unauthorized`: sessão ausente, expirada ou inválida.
    - `403 Forbidden`: permissão negada, falha de CSRF, origem não autorizada ou suspensão.
    - `404 Not Found`: recurso não localizado no escopo do tenant autenticado (sem vazar existência entre tenants).
    - `409 Conflict`: versão otimista obsoleta (`expected_version`) ou resync de eventos exigido (`ressincronização necessária`).
    - `422 Unprocessable Entity`: corpo inválido ou payload malformado.
    - `503 Service Unavailable`: dependência de banco de dados ou serviço não inicializada.

---

## 2. Rotas de Atendimento (Huma v2)

Prefixo base: `/api/panel/inboxes/{inbox_id}`

### `GET /conversations?conversation_id={UUID}`
- **Resumo**: Snapshot consistente da fila de atendimento da caixa.
- **Resposta (200)**:
  - `next_cursor`: cursor sequencial (`int64`) da caixa.
  - `conversations`: lista de conversas ativas ordenadas por prioridade/espera.
  - `messages`: lista de mensagens da conversa selecionada (se `conversation_id` fornecido).

### `GET /conversations/{conversation_id}/history`
- **Resumo**: Histórico completo de mensagens e eventos da conversa.
- **Resposta (200)**:
  - `next_cursor`: cursor sequencial (`int64`).
  - `head`: cabeçalho atual da conversa (protocolo, ciclo, status, versão, atendente).
  - `messages`: histórico de mensagens (inbound, outbound, anotações internas).
  - `timeline`: marcos do ciclo de vida da conversa.

### `GET /events?after={seq}`
- **Resumo**: Eventos ordenados ocorridos na caixa após o cursor informado.
- **Resposta (200)**:
  - `next_cursor`: cursor da faixa examinada (mesmo sem novos eventos).
  - `events`: lista de eventos autorizados do tenant.
- **Erro (409 Conflict)**: detalhe `ressincronização necessária` se a sequência do cliente for muito antiga ou inválida, instruindo o frontend a solicitar novo snapshot completo.

### `POST /conversations/{conversation_id}/assume`
- **Corpo JSON**:
  - `expected_version` (`int64`): versão esperada da conversa para controle de concorrência otimista.
- **Resposta (200)**: Conversa atualizada com atribuição ao operador atual e novo `version`.

### `POST /conversations/{conversation_id}/messages`
- **Corpo JSON**:
  - `expected_version` (`int64`): versão atual esperada da conversa.
  - `client_request_id` (`string`): chave de idempotência do cliente (escopo tenant/operador).
  - `body` (`string`): texto da mensagem a enviar ao cliente.
- **Resposta (201 Created)**:
  - `message_id` (`UUID`): identificador da mensagem persistida.
  - `attempt_id` (`UUID`): tentativa de envio gerada.
  - `idempotency_id` (`string`): identificador de deduplicação registrado.
  - `state` (`string`): estado local inicial (`queued` / `sending`).
  - `replayed` (`bool`): `true` se requisição duplicada com mesmo conteúdo foi retornada de forma idempotente.
- **Semântica de Envio**: A gravação da mensagem no banco local com transação atômica do job River **não** implica aceitação nem entrega remota pela Meta. O status progride via webhooks e receipts assíncronos.

### `POST /conversations/{conversation_id}/close`
- **Corpo JSON**:
  - `expected_version` (`int64`): versão esperada.
  - `client_request_id` (`string`): idempotência.
  - `reason` (`string`): motivo do encerramento.
  - `result` (`string`): resolução do atendimento.
  - `summary` (`string`): resumo operacional.
  - `farewell_body` (`string`, opcional): mensagem de despedida opcional.
  - `farewell_request_id` (`string`, opcional): chave de idempotência da despedida.
- **Resposta (200)**:
  - `conversation`: estado fechado (`closed`).
  - `closure`: metadados de encerramento.
  - `farewell`: dados da mensagem de despedida (se enviada).
  - `farewell_failed`: `true` se o envio da despedida falhou sem desfazer o encerramento da conversa.

### `POST /conversations/{conversation_id}/reopen`
- **Corpo JSON**:
  - `expected_version` (`int64`): versão esperada.
  - `client_request_id` (`string`): idempotência.
- **Resposta (200)**: Conversa reaberta preservando o mesmo protocolo e incrementando `cycle_number` e `version`.

---

## 3. Rotas de Configuração de Canais Meta (Huma v2)

Prefixo base: `/api/panel/channels/meta`

### `GET /`
- **Resumo**: Listar caixas e alertas operacionais Meta do tenant.
- **Resposta (200)**:
  - `boxes`: caixas configuradas com metadados públicos (sem tokens ou segredos).
  - `alerts`: alertas ativos de credencial degradada ou falha operacional.

### `POST /`
- **Resumo**: Configurar ou rotacionar credencial de caixa Meta Cloud.
- **Corpo JSON**:
  - `inbox_name` (`string`): nome da caixa.
  - `display_number` (`string`): número de exibição WhatsApp.
  - `app_id` (`string`): ID do aplicativo Meta Cloud.
  - `app_secret` (`string`): Segredo do aplicativo (somente de escrita, cifrado com envelope AES-GCM pelo KeyRing).
  - `waba_id` (`string`): WhatsApp Business Account ID.
  - `phone_number_id` (`string`): ID do número de telefone Meta.
  - `access_token` (`string`): Token de acesso de longa duração (somente de escrita, cifrado pelo KeyRing).
- **Resposta (200)**: Dados públicos da caixa e versão gerada (sem segredos em texto claro).

### `GET /inspection`
- **Resumo**: Inspecionar referências operacionais de quarentena e fila sem expor payloads sensíveis.
- **Resposta (200)**:
  - `quarantine`: identificadores em quarentena por falha repetida.
  - `items`: contadores e identificadores de itens de webhook em trânsito.
  - `discarded`: jobs River esgotados (identificadores e erros sanitizados, sem argumentos).

### `POST /alerts/{alert_id}/ack`
- **Resumo**: Reconhecer alerta de canal degradado.
- **Resposta (204 No Content)**.

---

## 4. Realtime WebSocket (`/api/panel/realtime` e `/api/panel/ws`)

Montado diretamente no Chi para suporte completo a upgrade WebSocket RFC 6455.
O OpenAPI documenta o upgrade de `/api/panel/ws` como `attendance-websocket`;
os componentes `ClientMessage`, `ServerMessage`, `RealtimeStoreHead`,
`RealtimeStoreMessage` e `RealtimeStoreEvent` são derivados dos tipos Go reais.
O registro documenta o protocolo e não instala um handler HTTP adicional.

- **Handshake e Autenticação**:
  - Valida o cookie `__Host-session` presente na requisição de upgrade.
  - Valida header `Origin` contra `CHATISP_PANEL_ORIGINS`.
  - Revalidação contínua de sessão e tenant em background.
- **Protocolo de Mensagens**:
  - JSON bidirecional; `action` seleciona a operação (`type` é alternativa, com precedência para `action`).
  - A conexão pode iniciar com `?inbox_id=UUID&since_seq=42`, equivalente à inscrição com cursor; sem cursor positivo, inicia com snapshot.
  - `{"action":"subscribe","inbox_id":"UUID"}` inscreve uma caixa e retorna snapshot; `with_snapshot:true` força snapshot. Com `after` ou `since_seq` positivo, inicia replay a partir do cursor, salvo quando `with_snapshot` está ativo.
  - `{"action":"snapshot","inbox_id":"UUID","conversation_id":"UUID"}` obtém snapshot e histórico opcional; `inbox_id` pode ser omitido após inscrição. `conversation_id` não filtra `subscribe`.
  - `{"action":"replay","inbox_id":"UUID","after":42}` retoma eventos; `inbox_id` pode ser omitido após inscrição e `since_seq` é alternativa a `after`.
  - `{"action":"ping"}` recebe `{"type":"pong","inbox_id":"00000000-0000-0000-0000-000000000000"}`. Não há operação `unsubscribe`; feche a conexão ou inscreva outra caixa.
  - Respostas usam `type`, `inbox_id`, `next_cursor` e, conforme o caso, `conversations`, `messages`, `events`, `reason`, `code` e `message`; campos vazios podem ser omitidos. Por exemplo: `{"type":"events","inbox_id":"UUID","next_cursor":42,"events":[{"Seq":42,"ID":"UUID","Type":"...","AggregateType":"...","AggregateID":"UUID","Version":1,"Payload":{}}]}`. Os objetos de conversas e mensagens também usam os nomes Go exportados (por exemplo `ID`, `ConversationID`, `LocalState`).
  - `next_cursor` representa a faixa examinada, inclusive eventos não visíveis; armazene-o para replay. `{"type":"resync","inbox_id":"UUID","reason":"retention_gap","next_cursor":42}` ou `load_limit_exceeded` requer novo snapshot; erros usam `{"type":"error","inbox_id":"00000000-0000-0000-0000-000000000000","code":"...","message":"..."}`. O UUID zero aparece quando a mensagem não associa uma caixa.
- **Sinalização Reativa**:
  - Integração com `realtime.PGNotifier` via `LISTEN/NOTIFY` no PostgreSQL (`chatisp_realtime_events`).
  - Fallback automático para `LocalNotifier` em ambientes de teste.
  - Eventos carregam `Seq`; o cursor é `next_cursor` na mensagem externa.

---

## 5. Webhook Meta Cloud (`/api/webhooks/meta`, `/webhooks/meta`, `/meta/webhook`)

Montado diretamente no Chi para acesso ao payload bruto (`raw body`) necessário para HMAC SHA-256.

- **Vínculo do App ao Endpoint**:
  - O webhook aceita o identificador do aplicativo via query parameter `?app_id={id}`, header `X-Meta-App-ID: {id}`, ou assume o App padrão configurado via `CHATISP_META_APP_ID`.
  - Se nenhum `app_id` for informado e `CHATISP_META_APP_ID` estiver vazio, a requisição é rejeitada imediatamente com `400 Bad Request` (`app_id não identificado`).
- **Resolução de Credencial e Verificação de Token**:
  - O aplicativo confiável é localizado através de `AppResolver`, consultando o segredo cifrado (`AppSecret`) associado ao `app_id`. Se o App não existir no registro, retorna `401 Unauthorized` (`aplicativo não encontrado`).
  - O token de verificação é validado via `VerifyTokenProvider` (configurado a partir de `CHATISP_META_VERIFY_TOKEN`).
  - Se o token for inválido, ausente ou idêntico ao próprio segredo do aplicativo (`AppSecret`), retorna `401 Unauthorized` (`token de verificação inválido`).
- **Verificação de Desafio (GET)**:
  - Trata `hub.mode=subscribe`, `hub.verify_token` e `hub.challenge`.
  - Valida o token contra o resolvedor configurado.
  - Responde com o valor puro de `hub.challenge` e status HTTP 200.
- **Recepção de Eventos (POST)**:
  - Trata o cabeçalho `X-Hub-Signature-256: sha256=<hex>`.
  - Valida a assinatura HMAC SHA-256 utilizando a chave do `AppSecret` resolvido antes de prosseguir com qualquer parsing de negócio. Assinatura inválida ou ausente resulta em `401 Unauthorized`.
  - Rejeita payloads que ultrapassem o limite de tamanho (`MaxBodyBytes`, padrão 1MB) com `413 Payload Too Large`.
  - Persistência atômica: insere o envelope bruto na tabela de quarentena/ingress (`ingress_deliveries`) e enfileira o job River `ingress` na mesma transação PostgreSQL sob o papel `chatisp_ingress`.
  - Responde HTTP 200 imediatamente para a Meta, garantindo desacoplamento do processamento assíncrono.
  - Permite chamadas server-to-server da Meta que não incluem cabeçalhos de navegador (`Origin` / `Sec-Fetch-Site`).

---

## 6. Distinção de Estados Locais e Recibos Remotos

O sistema mantém separação estrita entre o ciclo de vida local e as confirmações remotas, expondo os seguintes campos e estados canônicos:

### 6.1. Estados Locais da Mensagem (`messages.local_state` / DTO: `local_state`)

Representa o ciclo de vida da mensagem sob a custódia do banco de dados local do Chat ISP:

- **Mensagens de Entrada (`direction = 'inbound'`)**:
  - `received`: recebida pelo webhook, autenticada via HMAC, persistida atomicamente e vinculada à conversa e contato pelo processador River.

- **Mensagens de Saída (`direction = 'outbound'`)**:
  - `queued`: criada localmente por operador humano, bot ou encerramento; enfileirada no banco de dados aguardando execução pelo worker River.
  - `sending`: tentativa de envio (`outbound_attempts`) adquirida com lock ativo por trabalhador River, em processo de chamada ao driver Meta Cloud API (ou fake de simulação).
  - `accepted`: chamada HTTP ao provedor retornou sucesso; identificador externo da Meta (`external_message_id`) persistido localmente.
  - `failed`: falha terminal de validação, erro irreversível de envio ou rejeição fatal de credencial.
  - `retrying`: falha transitória detectada (ex.: erro 429 rate limit ou timeout reversível); tentativa reenfileirada pelo River com backoff.
  - `uncertain`: erro de rede ou interrupção de transporte após o despacho da requisição para a Meta onde o desfecho remoto não pôde ser confirmado. **Não realiza reenvio automático** para impedir duplicidade de mensagens para o destinatário; aguarda reconciliação ou recibo posterior.
  - `cancelled`: envio cancelado antes do disparo (ex.: conversa encerrada antes do lease do worker).

### 6.2. Recibos Remotos Assíncronos (`message_status_events.kind` / DTO: `kind`)

Confirmações e telemetria recebidas assincronamente da infraestrutura da Meta via webhook:

- `accepted`: confirmação remota de admissão da mensagem pela plataforma Meta.
- `delivered`: mensagem entregue com sucesso no aplicativo WhatsApp do cliente.
- `read`: mensagem aberta e lida pelo destinatário (confirmação visual).
- `failed`: falha de entrega reportada remotamente pela Meta (ex.: número bloqueado, janela de 24h expirada).
- `unknown`: recibo com classificação não documentada ou variante reservada.

### 6.3. Estados do Canal Meta (`channel_connectors.status` / DTO: `status`)

Ciclo de conectividade operacional do canal de atendimento:

- `pending`: canal cadastrado no painel, pendente de validação ou handshake inicial.
- `connected`: canal verificado, credencial ativa e apto para tráfego bidirecional.
- `degraded`: falha de credencial, expiração de token ou erros repetidos de despacho; gera alerta operacional (`alerts`) para intervenção do administrador sem interromper a recepção de webhooks já autenticados.
- `disconnected`: canal desativado manualmente pelo operador ou cancelado definitivamente.

---

## 7. Lifecycle do Processo e Shutdown Gracioso

- **Modos de Execução**:
  - `api`: escuta HTTP e métricas; inicializa pools `App`, `Platform` e `Ingress`. Exige `CHATISP_META_VERIFY_TOKEN`.
  - `worker`: escuta métricas; abre pool dedicado `chatisp_jobs` e executa trabalhadores River para as filas `ingress`, `inbound` e `outbound`. Exige `CHATISP_META_SIMULATION=true` no Marco M2.
  - `all`: executa API e Worker concorrentemente no mesmo processo.
  - `migrate`: aplica migrações Goose e River com o papel `chatisp_owner`.
  - `openapi`: exporta especificação OpenAPI canônica em JSON formatado.
- **Ordem de Encerramento (Graceful Shutdown)**:
  1. Marcação de `readyGate` como `false` (sondas `/readyz` passam a retornar 503).
  2. Encerramento gracioso do listener HTTP com timeout configurado (`ShutdownTimeout`).
  3. Parada suave do worker River (`JobsClient.Stop`), aguardando conclusão ou soft stop de tarefas ativas sem perder pendências.
  4. Encerramento do listener de métricas Prometheus.
  5. Liberação ordenada de todos os pools de conexão PostgreSQL (`db.Pools` e `workerPool`).

---

## 8. Deploy e Execução com Docker Compose

O ponto de entrada de infraestrutura do Chat ISP no Marco M2 é `deploy/compose.yml`.

- **Papéis e Concessões do Banco de Dados**:
  - O script de inicialização do PostgreSQL cria o papel `chatisp_jobs` com `NOSUPERUSER NOINHERIT NOBYPASSRLS`.
  - Concede `GRANT CONNECT, CREATE ON DATABASE chatisp TO chatisp_owner` para aplicação ordenada de migrações River (`river.river_job`) após Goose.
  - Concede `GRANT CONNECT ON DATABASE chatisp TO chatisp_jobs`.
- **Variáveis de Ambiente Obrigatórias**:
  - Senhas dos papéis: `POSTGRES_PASSWORD`, `CHATISP_OWNER_PASSWORD`, `CHATISP_APP_PASSWORD`, `CHATISP_PLATFORM_PASSWORD`, `CHATISP_INGRESS_PASSWORD`, `CHATISP_JOBS_PASSWORD`.
  - Criptografia: `CHATISP_DATA_KEYS`, `CHATISP_DATA_KEY_VERSION`.
  - Webhooks e Simulação: `CHATISP_META_SIMULATION=true`, `CHATISP_META_VERIFY_TOKEN`, `CHATISP_META_APP_ID`.
- **Comando de Deploy**:
  ```sh
  docker compose -f deploy/compose.yml --env-file deploy/.env up --build
  ```
  *(Nota: no repositório base, `README.md` referenciava `deploy/compose.yaml` da fase M1; o arquivo operacional M2 é `deploy/compose.yml`.)*
