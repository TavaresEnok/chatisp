# Reparo da regressão de fechamento (cb19fc4)

Baseline: `main` desta worktree em `cb19fc4` (lote 1 + aceite E2E). O `go test ./...` do backend, com PostgreSQL descartável, falhava em dois pontos. O desenho aprovado de webhooks e de shutdown foi mantido. Nenhuma asserção foi afrouxada e nenhum contrato de API pública, mídia, ERP ou painel foi alterado.

## 1. Jobs de atendimento

### Causa

A migração `00044` (`webhook_capture` / `webhook_enqueue`) grava um job River `chatisp.webhook.deliver.v1` na fila `webhooks` para cada evento real de conversa ou mensagem. Isso é o desenho aprovado: uma origem, um job, chave única por tenant, tipo e source.

`countJobs` em `backend/internal/attendance/service/harness_test.go` contava todo `river.river_job` do tenant. Os testes abaixo pediam o job de outbound e passavam a ver também os webhooks:

- `TestSendIdempotencyRaceConflictAndRollback` via 5 jobs e esperava 1. São 1 `chatisp.outbound.dispatch.v1` (`outbound`) e 4 webhooks: `conversation.created`, `message.created` de entrada, `conversation.assigned` e `message.created` de saída.
- `TestCloseFarewellReopen` via 6 jobs e esperava 1. São 1 outbound da despedida e 5 webhooks: os três acima, mais `message.created` da despedida e `conversation.closed`.

A bisseção (passa em `6e22a2b`, falha em `d20098d`) coincide com a entrada desse capture. Não há job outbound duplicado na corrida nem no rollback.

### Correção

`countKindJobs` filtra `kind` e `queue`. A corrida e o rollback continuam exigindo exatamente 1 job `chatisp.outbound.dispatch.v1` / `outbound`. A despedida exige exatamente 1 job nessa mesma fila. Cada ponto afirma também a quantidade de `chatisp.webhook.deliver.v1` / `webhooks` (4 na corrida e depois do rollback; 5 na despedida). Um webhook a mais no rollback falha o teste. `countJobs` sem filtro permanece para a anotação, que só prova que a operação não enfileirou nada novo.

`TestCloseFarewellReopen` repete essas contagens (1 outbound e 5 webhooks) depois de `Reopen` e depois do replay da mesma `ReopenInput`. A migração `00044` só enfileira webhook em `conversation.created`, `message.created`, assignee novo e `conversation.closed`. Reabrir limpa o assignee e volta o status para aberto; não cria outro job de despedida nem de webhook. Um job extra na reabertura ou no replay falha o teste.

## 2. Encerramento HTTP intermitente

### Causa

`TestSmokeAllModeHealthAndLogin` sobe o modo `all` com `CHATISP_SHUTDOWN_TIMEOUT=2s` e espera `Run` sem erro. `http.Server.Shutdown` só termina quando não há conexão fora de idle. Dois comportamentos do `net/http` do Go 1.27 estouram esse prazo sob carga, sem serem falha de listener:

- Conexão aceita que ainda não terminou o primeiro header fica em `StateNew`. `closeIdleConns` só a trata como idle depois de 5s. `ReadHeaderTimeout` do servidor também é 5s. Com timeout de shutdown de 2s, qualquer socket nesse estado faz `Shutdown` devolver `context.DeadlineExceeded`.
- O processo cancelado não era o `BaseContext` do `http.Server`. Um handler preso em `r.Context()` (o `/readyz` faz um `Ping` de até 2s por pool, em sequência) continuava ativo. O cliente do smoke usa timeout de 2s e keep-alive; o corpo do POST do webhook ficava aberto até o fim do teste. O cancelamento do processo não acordava esse handler.

O erro devolvido por `Shutdown` era descartado. O log dizia só "falha ao encerrar o servidor HTTP", sem a causa. O teste isolado passava porque o prazo de 2s sobrava; com `./...` o atraso aparecia. Não é worker River preso no `Shutdown` do HTTP: o worker já observa `ctx.Done()` em paralelo. A verificação do erro de `Shutdown` foi mantida.

### Correção

- `BaseContext` do HTTP e das métricas é o contexto do processo. No cancelamento, o contexto do pedido cai e o `/readyz` (e qualquer handler que o respeite) retorna.
- Sockets em `StateNew` ou `StateIdle` só são fechados depois que `Server.Shutdown` gravou `inShutdown` e fechou os listeners (`RegisterOnShutdown`). Antes disso `closeQuiet` não fecha nada: um pedido ainda pode passar na checagem `shuttingDown` do `net/http` e entrar no handler.
- A escolha do socket e o `Close` acontecem com o mesmo mutex de `observe`. O hook de `StateActive` não retorna, e portanto o handler não começa, entre a decisão e o fechamento. Socket que já está `StateActive`, ou que publica `StateActive` antes do commit, sai da lista e não é fechado. Hijack também não é fechado.
- `Shutdown` do HTTP e das métricas registra `error` com `err.Error()`. Valor que `logging.Sensitive` classifica como segredo não entra no log. O erro retornado ao processo continua sendo a frase estável, sem o texto cru.
- O smoke lê e fecha o corpo do POST antes de seguir e chama `CloseIdleConnections` antes de cancelar o processo.

Regressão em `runtime`:

- `TestShutdownClosesConnectionWaitingForHeaders`: TCP aceito sem pedido, shutdown de 1s, `Run` retorna nil.
- `TestShutdownCancelsInFlightReadyCheck`: `/readyz` bloqueado no contexto do pedido, cancelar o processo encerra dentro do prazo.
- `TestShutdownLogsHTTPError`: handler que ignora o contexto ainda faz o shutdown falhar, e o log contém `context deadline exceeded` sem URL de banco.
- `TestQuietSelectionDoesNotCloseSocketThatBecomesActive`: com o shutdown já armado, a seleção quieta solta o mutex e o socket passa de `StateNew` ou `StateIdle` para `StateActive` antes do commit. O `Close` não ocorre.
- `TestCloseQuietBeforeShutdownArmLeavesSocketsOpen`: sem `inShutdown`, o socket em `StateNew` permanece aberto.
- `TestShutdownKeepsHandlerThatActivatedBeforeQuietClose`: o handler já entrou, a seleção quieta está no intervalo anterior ao `Close`, o cliente recebe `kept` e o shutdown de 2s ainda termina porque a conexão sem header é fechada.

## Evidências

Comandos desta worktree, PostgreSQL só pelo `backend/tools/pg`, sem rede de produção:

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`
- `go -C backend build ./...`
- `go -C backend vet ./...`
- `go -C backend test -count=10 -run TestSmokeAllModeHealthAndLogin ./cmd/chatisp/`
- `go -C backend test -count=1 ./...`
- `git diff --check`

Resultado nesta worktree, depois do reparo da corrida no `closeQuiet` e das contagens pós-reabertura:

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: saiu 0 (1 pacote).
- `go -C backend build ./...`: saiu 0.
- `go -C backend vet ./...`: saiu 0.
- `gofmt -l` nos arquivos Go alterados: sem saída.
- `go -C backend test -count=1 -timeout 120s -run 'TestShutdown|TestCloseQuiet|TestQuietSelection' ./internal/platform/runtime/`: saiu 0 em 0,6s, sem PostgreSQL.
- `go -C backend test -count=1 -timeout 180s -run 'TestCloseFarewellReopen|TestSendIdempotencyRaceConflictAndRollback' ./internal/attendance/service/`: saiu 0 em 2,7s.
- `go -C backend test -count=10 -timeout 20m -run TestSmokeAllModeHealthAndLogin ./cmd/chatisp/`: saiu 0 em 43,2s (`ok chatisp/cmd/chatisp`).
- `go -C backend test -count=1 -timeout 25m ./...`: saiu 0. Inclui `attendance/service` (19,3s), `outwebhook` (60,0s) e `runtime` (5,7s).
- `git diff --check`: saiu 0.

## Limites

Não houve deploy, push, merge, acesso a produção nem alteração de ERP, mídia ou painéis web. Contratos `m6-public-api` e o worker de `outwebhook` não foram reimplementados. O timeout configurado não foi aumentado para esconder a falha.
