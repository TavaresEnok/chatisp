# Continuidade — frente de operação

Data: 04/11/2026. Esta frente não conclui M8, V1 nem o piloto. Não houve deploy, banco real, credencial externa, k6 nem expurgo.

## Partida

| | |
| --- | --- |
| Repositório de leitura | `/home/enok/srv_projetos/workspace/chat-isp-paseo` |
| Branch conferida e não alterada | `orchestration/eec2118bdf14` |
| Commit de partida | `54febe97b69ff131d80ff3cc880eac33f832e958` |
| Worktree desta frente | `/home/enok/srv_projetos/workspace/chat-isp-cursor-operacao` |
| Branch desta frente | `continuation/cursor-operacao` |

Os commits desta branch estão na seção final. A branch de orquestração não recebeu commit nem push.

## Já concluído antes desta frente

Evidência no próprio repositório, em `docs/m7-m8-lote2-entrega.md` e nos contratos citados. Não foi reimplementado aqui.

| Entrega | Commit de origem | Evidência |
| --- | --- | --- |
| Filtros, grade e mapa de calor | `eacd1af` (merge `f7c0bee`) | `docs/contracts/m7-report-panel.md` e o aceite integrado do lote 2 |
| Nome e logo do provedor | `f6183fc` (merge `76802c6`) | `docs/contracts/m7-provider-identity.md` |
| Séries de pool, WebSocket e River | `51888f4` (merge `e0327d3`) | `docs/contracts/m8-runtime-telemetry.md` |
| Ensaio de restore com conteúdo e chaves | `ccabd76` (merge `2407824`) | `docs/contracts/m8-restore-drill.md`; `go test` do pacote `restoredrill` registrado naquele documento |
| Aceite integrado local do lote 2 | `6afc370` (merge `54febe9`) | Chromium `140.0.7339.186`, `npm --prefix web run test:e2e:m7-ops`, frase `M7-OPS E2E passou (simulação local, sem skip)` |

O ensaio de restore e o E2E do lote 2 não foram repetidos como prova nova, salvo o teste `TestDrillRestoresDumpAndReapplies`, reexecutado nesta frente porque o ensaio passou a gravar `metrics.prom`.

## O que esta frente implementou

- Séries operacionais no registry privado: incerteza de envio, reservas de envio e de trabalho, idade e contagem de webhook, soma de `expected_bytes` dos anexos validados e contagem de conectores. Contrato: `docs/contracts/m8-ops-signals.md`. Migração `00047_m8_ops_signals.sql` (na integração, depois da `00046_privacy_subject.sql`). O modo `api` não publica essas séries.
- `metrics.prom` no diretório temporário do ensaio de restore, com a duração daquele exercício.
- Guia com os comandos e as URLs conferidos no repositório: `docs/operacao.md`.
- Revisão operacional reexecutável: `./scripts/ops-security-check.sh` e `docs/contracts/m8-seguranca-operacional.md`.
- Ensaio de carga só local: `./scripts/ops-load-local.sh` e `load/perfil-sintetico-local.md`. O relatório recusa capacidade.

## Lacunas, em ordem de dependência

Funcionalidade ainda ausente, que não espera credencial:

1. O painel de alertas não mostra as séries novas. O destino delas é `GET /metrics`. Os três alertas antigos continuam em `/configuracoes`.
2. Cache com Ristretto e invalidação de permissão. Não há uso no módulo.
3. Circuit breaker do ERP (`sony/gobreaker` não é usado no código do produto).
4. Carga multi-tenant com k6, p95 de aceite, CPU, RAM e E/S do banco. O ensaio desta frente é um processo, duas rotas de saúde e um login sintético.
5. Passagem de um operador no navegador autenticado. Em HTTP o cookie `Secure` não fica gravado; o Compose em `https://localhost:8443` não foi executado neste host, que não tem Docker.
6. Backup de produção, agenda mensal e RPO/RTO. O ensaio descartável não substitui isso.

Integração já encaminhada que só espera credencial ou homologação:

- Meta real, número e app. O modo simulado existe; a D2-03 continua aberta.
- ERP real. O fake não é homologação. A D2-01 continua aberta.
- Evolution. A flag permanece desligada. A D2-04 continua aberta.
- Perfil de carga e metas do piloto. A D2-07 continua aberta. Instrução na seção de pendências.
- Retenção e expurgo. A D2-06 continua aberta. O expurgo automático permanece desligado.

Outra frente, não implementada aqui. O detalhe deve ir para `docs/CONTINUACAO-PRODUTO.md`, arquivo que esta frente não cria:

- Privacidade, exportação e exclusão de contato, além do que já existe sob a política com expurgo desligado.
- Mídia, ffmpeg e limites de conversão.
- Lacunas de API pública e integrações de produto.

## Próxima entrega funcional

Mostrar no painel do admin, só leitura, os totais que `GET /metrics` já publica (incertos, reservas, webhook pendente, bytes esperados e conectores), sem inventar limite e sem tratar o número como capacidade. Isso não depende de credencial externa. k6 fica para depois da D2-07.

## Dependências externas e limites

- Não reativar o Paseo, não alterar fila, daemon nem `/home/enok/srv_projetos/orchestration`.
- Não fazer deploy, não usar credencial externa, não abrir banco real, não executar expurgo.
- PostgreSQL de teste instalado nesta worktree: `npm ci --prefix backend/tools/pg --no-audit --no-fund` (1 pacote, `@embedded-postgres/linux-x64` `18.4.0-beta.17`). O diretório `node_modules` não entra no Git.

## Matriz conferida

Só entram requisitos lidos no plano, na especificação, no código ou na evidência desta frente. Não é a lista das 257 linhas. Nenhum item está em 100%. Simulação não conta como homologação de Meta, ERP ou Evolution.

| ID | Estado |
| --- | --- |
| 14-rls | implementado e verificado. `TestTenantIsolationAndCompositeForeignKey` e `TestPoliciesFailClosedWithoutTenantSetting` passaram nesta frente. |
| 14-csrf | implementado e verificado. `TestPanelRejectsBadOriginCSRFAndRevokedSession` passou nesta frente. |
| 14-logs | parcial com a lacuna específica: há `slog` JSON e redação de segredo em `backend/internal/platform/logging`; esta frente não provou que corpo de mensagem e CPF ficam de fora em todos os caminhos. |
| 14-migracoes | implementado e verificado. Goose aplicou as migrações nos testes de sinais, de carga e de restore desta frente. Na árvore integrada o arquivo de sinais é `00047_m8_ops_signals.sql`; `00046` fica com a privacidade. |
| 14-modos | parcial com a lacuna específica: `api`, `worker`, `migrate` e `all` existem; o ensaio subiu só `all`. |
| 14-cache | não implementado. Não há Ristretto no módulo. |
| 14-isolamento | parcial com a lacuna específica: RLS entre tenants foi reexecutado; cache por tenant não existe; anexo e fila não foram reexecutados aqui. |
| 14-webhook-duravel | parcial com a lacuna específica: `TestWebhook_VerificationAndPersist` passou; a prova não cobre produção. |
| 14-assinatura-meta | implementado mas dependente de homologação externa. A verificação local passou no teste de webhook; o segredo real da Meta não foi usado. |
| 14-outbox | parcial com a lacuna específica: o estado `uncertain` agora tem gauge; o teste de processo caído depois da chamada remota não foi reexecutado. |
| 14-s3 | parcial com a lacuna específica: o plano registra objeto local simulado no recorte M4A; não há S3 de operação. |
| 14-upload | parcial com a lacuna específica: `TestTypeAndNameValidation` recusou HTML e SVG nesta frente; duração de mídia não foi medida. |
| 14-ffmpeg | não implementado. Permanece para a frente de produto. |
| 14-uso-meta | implementado mas dependente de homologação externa. O agregado local não vê uso reportado por uma WABA real. |
| 14-circuit-breaker | não implementado. `sony/gobreaker` não aparece no código do produto. |
| 14-observabilidade | parcial com a lacuna específica: desconexão entra no alerta local; número real, Evolution e credencial segregada em produção não foram homologados. |
| 14-retencao | parcial com a lacuna específica: classes e recusa de expurgo existem; a D2-06 segue aberta e o expurgo permanece desligado. |
| 14-backup | parcial com a lacuna específica: o ensaio descartável restaura dump, bytes, chave e reaplica exclusão, e agora grava a duração; não há backup de produção nem RPO/RTO. |
| 14-carga | parcial com a lacuna específica: 200 pedidos locais passaram e o relatório marca capacidade como falsa; não houve k6 nem carga multi-tenant. |
| 14-metricas-alertas | parcial com a lacuna específica: idade de job, erro de worker, pool, WebSocket, incerteza, reserva, webhook, storage e duração do ensaio existem; o painel só alerta job, conector desconectado e webhook incompleto; não há meta de latência. |
| M8-guia | implementado e verificado. `docs/operacao.md` repete os comandos e as portas de `scripts/dev.sh` e do Compose. O Compose não foi executado. |
| M8-revisao-seguranca | parcial com a lacuna específica: RLS, CSRF, webhook, SSRF e mídia ativa passaram no script local; cache, ffmpeg e homologação externa ficam de fora. |
| M8-piloto | não implementado. Nenhum operador percorreu o fluxo autenticado no navegador nesta frente. |

## Evidência desta frente

Comandos executados na worktree, com código 0:

- `npm ci --prefix backend/tools/pg --no-audit --no-fund` — 1 pacote.
- `go test -count=1 -timeout 60s ./internal/platform/metrics/ ./internal/loadlocal/ ./cmd/chatisp/ -run 'TestOps|TestAPIWiring|TestJobsPool|TestPercentile|TestRefuse|TestReadProcess|TestFormat'`
- `go test -count=1 -timeout 180s ./internal/platform/opsignals/` — `ok` em 2,005s.
- `go test -count=1 -timeout 240s -run 'TestDrillRestoresDumpAndReapplies$' ./internal/restoredrill/` — `ok` em 3,692s.
- `go test -count=1 -timeout 360s -run 'TestSyntheticLocalLoad$' -v ./cmd/chatisp/` — 200 pedidos, 0 falhas, p95 1,648 ms, RSS 97345536 bytes, E/S de leitura e escrita 0 na janela, 12 ticks de CPU. `aceito_como_capacidade` falso. Isso não é capacidade.
- `go vet` nos pacotes alterados — sem diagnóstico.
- `./scripts/ops-security-check.sh` — RLS, CSRF, webhook, SSRF e mídia ativa passaram.

Não rodei a suíte ampla do plano.

## Pendência externa — instrução

Para autorizar k6 e um número de capacidade, escreva a resposta da D2-07 em `docs/perguntas.md` com provedores, atendentes simultâneos, números, mensagens, mídia, retenção, p95, RPO e RTO do piloto real. Até lá, não publique o p95 1,648 ms nem qualquer outro número deste ensaio como capacidade. O expurgo só liga depois da resposta da D2-06 em `docs/legal/`, com o perfil do provedor. Meta, ERP e Evolution só saem de simulação com a credencial e o aceite correspondentes (D2-03, D2-01 e D2-04).

## Arquivos que a frente de produto não deve sobrescrever

- `docs/CONTINUACAO-CURSOR.md`
- `docs/operacao.md`
- `docs/contracts/m8-ops-signals.md`
- `docs/contracts/m8-seguranca-operacional.md`
- `docs/contracts/m8-ops-alerts.md`
- `docs/contracts/m8-runtime-telemetry.md`
- `docs/contracts/m8-restore-drill.md`
- `load/perfil-sintetico-local.md`
- `scripts/ops-security-check.sh`
- `scripts/ops-load-local.sh`
- `backend/db/migrations/00047_m8_ops_signals.sql`
- `backend/internal/platform/opsignals/`
- `backend/internal/platform/metrics/ops.go`
- `backend/internal/platform/metrics/ops_test.go`
- `backend/internal/loadlocal/`
- `backend/internal/restoredrill/metric.go`
- `backend/cmd/chatisp/loadlocal_test.go`

Se a outra frente editar `backend/cmd/chatisp/main.go` ou `backend/cmd/m2demo/demo.go`, precisa conservar a chamada de `opsignals.Collect` no processo que já coleta o River.

## Commits desta frente

Branch `continuation/cursor-operacao`, a partir de `54febe97b69ff131d80ff3cc880eac33f832e958`.

- `55e907ebc25268f4f4b0050e938430347bfe2f1a` — Medir incerteza, reservas, webhook e storage no registry privado.
- `47789b56f7a4f0fc7e0713f19967b0bd25f9771b` — Registrar o guia de operação e o ensaio local de carga.
- O commit imediatamente posterior a `47789b5` grava esta lista. Não houve push.
