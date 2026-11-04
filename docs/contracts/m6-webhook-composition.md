# M6 — composição de webhooks com mídia e ERP

A árvore parte de `6e22a2be235e257207c35d814572ee9bf2ada4f3` (mídia inbound/outbound, reparo de locks/citação e ERP) e incorpora a entrega aprovada `d20098d82ccff1690405511ff2e017180bde3a26` (`preserved/webhooks-reviewed`). O ancestral comum é `428627becf2c40a9df4eb369ab0347579b465995`. Nenhum dos dois commits foi reescrito. O índice Git desta worktree é somente leitura, então a composição está na árvore de trabalho. O merge com os dois pais fica para a gerência, sem squash e sem rebase.

## O que entrou de cada lado

Arquivos que só a entrega de webhooks alterou em relação ao ancestral comum foram copiados byte a byte desse commit. Isso inclui a migração `00044_m6_public_api.sql`, `backend/api/public-openapi.json`, o pacote `publicapi`, o pacote `outwebhook` e os ganchos `PublishWebhookSource`. A baseline de mídia e ERP permaneceu, inclusive `00041`, `00042`, `00043`, storage, painel ERP e workers de mídia.

Seis caminhos mudaram nos dois lados. A resolução junta os dois comportamentos. Não houve escolha em bloco de um lado.

| Caminho | Decisão |
| --- | --- |
| `backend/internal/platform/jobs/contract.go` | Conserva `MediaArgs`, `MediaHandler`, `KindMedia` e `QueueMedia`. Acrescenta `WebhookArgs`, `WebhookHandler`, `KindWebhook`, `QueueWebhooks` e `RetryAfterError`. |
| `backend/internal/platform/jobs/workers.go` | Conserva `mediaWorker`. Acrescenta `webhookWorker`, com timeout de um minuto e snooze em `RetryAfterError`. Metadado `{"v":1}` permanece em todos os `InsertOpts`. |
| `backend/internal/platform/jobs/client.go` | Todo cliente registra os workers de ingress, inbound, outbound, `inbound_media`, webhooks e lifecycle. A fila `webhooks` só é buscada quando o handler de entrega está ligado. A fila `inbound_media` continua em todo cliente com pool. `WithClientID` identifica o líder nos testes. O periódico `lifecycle` grava o metadado de versão. `EnqueueMedia` permanece. |
| `backend/cmd/chatisp/main.go` | O storage privado continua a ser criado antes de `attendanceSvc`, e `Files`/`AttachmentLimits` são o mesmo valor usado pela API e pelos workers. `MediaStorage`, `MediaFetcher` e `Simulation` permanecem. O handler de webhook entra no mesmo `jobs.Client` do processo. |
| `backend/internal/platform/runtime/runtime.go` | ERP, timeout, breaker e grants continuam em `composeERP`. `KeyRing` e `Env` seguem para a API. `startOutboundWebhooks` roda no modo worker antes do fetch e não abre um segundo cliente River. |
| `backend/internal/platform/httpapi/api.go` | A API pública usa `publicapi.Mount` com o mesmo `attendance` (storage e limites já preenchidos). Painel de tokens e painel de webhooks entram junto com o mount de ERP. O OpenAPI do painel não absorve o documento público. |

## Fila e liderança

Nesta versão do River, o líder resgata jobs `running` de todo o schema e descarta o tipo que não está registrado. Por isso mídia e webhook são registrados mesmo quando o handler daquele processo é nulo. Um cliente só de webhooks, ou só de mídia, cancelaria o outro tipo ao assumir a liderança e deixaria de enfileirar o lifecycle.

`TestCoexistingClientsRescueBothKinds` sobe dois clientes no mesmo schema. Cada um registra ingress, inbound, outbound, `inbound_media`, webhooks e lifecycle, com handlers que concluem o job. Jobs PostgreSQL reais nascem em `running`. O primeiro líder os conclui. Depois da troca, o segundo líder resgata um novo conjunto dos mesmos tipos e o lifecycle continua. `discarded` e `cancelled` falham o teste.

## Migração 00044

Os triggers novos são `conversations_webhook_capture` e `messages_webhook_capture`. Eles não substituem os triggers de `00041`, `00042`, `00043` nem os de mídia e ERP. Grants e políticas novas valem para `api_tokens`, `webhook_event_sources`, `api_template_requests`, `outbound_job_links` e para as funções de token e de fila. A política `tenants_owner_api_token` é `SELECT` só para `chatisp_owner` quando `app.api_token=on`. Não amplia as policies antigas de `chatisp_app`.

`webhook_enqueue` e `webhook_recover_sources` inserem `chatisp.webhook.deliver.v1` na fila `webhooks` com metadado `{"v":1}`. Se o schema River ainda não existe, a origem fica com `enqueued_at` nulo e a transação do atendimento confirma.

`chatisp_app` só enfileira quando `app.tenant_id` é o tenant da origem. `chatisp_owner` continua isento, e o superusuário de bootstrap também: os testes de ERP semeiam a conversa por esse papel antes de existir o GUC, e a recusa abortava o insert. Os demais papéis seguem recusados. `EXECUTE` não foi concedido a eles. Grants e policies antigos de mídia e ERP não mudam.

## Contratos que permanecem

- Token: hash SHA-256, segredo mostrado uma vez, revogação 401, escopo ausente 403, outro tenant invisível, limite 429.
- Template da API pública passa por `SendTemplate` e pela mesma revalidação de consentimento e janela. A chave é idempotente por token e tenant.
- Destino privado continua recusado pelo urlguard fora de `test` e `development`.
- Assinatura `X-ChatISP-Signature-256`. 503 lento ou timeout deixa a entrega `pending`. O job não é confirmado enquanto houver tentativa pendente.
- `docs/contracts/m6-public-api.md` descreve a API e os eventos. A frase do cliente River agora inclui `inbound_media`.

## Limites

Não houve rede Meta, ERP ou Evolution, nem dados de produção. O commit de merge não foi criado aqui. Checks executados nesta worktree estão no final deste arquivo e devem ser reutilizados pelo revisor quando o código e o runner forem os mesmos.

## Checks

Nesta worktree, depois da composição e do ajuste de `webhook_enqueue`:

| Comando | Resultado |
| --- | --- |
| `npm ci --prefix backend/tools/pg --no-audit --no-fund` | ok, 1 pacote |
| `go -C backend build ./...` | ok |
| `go -C backend test -count=1 ./internal/outwebhook/... ./internal/publicapi/... ./internal/platform/jobs/... ./internal/platform/runtime/... ./internal/platform/httpapi/... ./internal/erp/... ./internal/attendance/pipeline/...` | ok. A primeira execução falhou em `TestERPPostgres` com `provedor ausente` no seed superuser. Depois do ajuste, a suíte inteira passou, inclusive `TestCoexistingClientsRescueBothKinds` dentro de `outwebhook` (cerca de 64s nesse pacote). |
| `git diff --check` | ok |

Não houve chamada de rede a Meta, ERP ou Evolution. O revisor pode reutilizar esta suíte se o código e o runner forem os mesmos. O merge administrativo com os dois ancestrais não foi feito aqui.
