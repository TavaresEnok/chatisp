# Integração — produto e operação

Data: 04/11/2026. Esta branch junta as duas frentes já concluídas e fecha a escrita que faltava na API pública. Não conclui M8, V1 nem o piloto. Não houve push, deploy, banco real, credencial externa, Meta, ERP, Evolution nem expurgo. Nenhum item está em 100%.

A worktree antiga `/home/enok/srv_projetos/workspace/chat-isp-cursor-integracao` e a branch `continuation/cursor-integracao` já existiam e não foram apagadas.

## Onde está

| | |
| --- | --- |
| Worktree | `/home/enok/srv_projetos/workspace/chat-isp-cursor-integracao-2` |
| Branch | `continuation/cursor-integracao-2` |
| Base | `54febe97b69ff131d80ff3cc880eac33f832e958` em `orchestration/eec2118bdf14` |
| Repositório de leitura | `/home/enok/srv_projetos/workspace/chat-isp-paseo` permaneceu nesse commit |

O Paseo segue pausado. Filas, daemon e `/home/enok/srv_projetos/orchestration` não foram alterados.

## Commits desta integração

A branch parte de `continuation/cursor-produto` (`5668602`) e incorpora `continuation/cursor-operacao` por merge.

- `f112a258338ed3dc6c8f9dd15e6afe1f7b46fd93` — Merge branch 'continuation/cursor-operacao' into continuation/cursor-integracao-2.
- `b2e3b5b0135ebf23a50116f790d621dc8edf22cc` — Renumerar os sinais operacionais para 00047 e deixar 00046 com a privacidade.
- `498c87d966d44b51a6f90d3d7b2e87b0b8c8d794` — Expor texto, atribuição e estado da conversa na API pública pelo atendimento do painel.
- O commit que grava este arquivo vem logo depois de `498c87d`. Não houve push.

## Frente de produto

Worktree `/home/enok/srv_projetos/workspace/chat-isp-cursor-produto`, branch `continuation/cursor-produto`. O relato longo está em `docs/CONTINUACAO-PRODUTO.md`.

- `66f6676fe03ab33bd7260088ef6de378b61e1c59` — Permitir ao admin exportar e excluir os dados de um titular sem ligar o expurgo.
- `1a13ae54e1fa1c285699217bd7e73d6c379469fc` — Converter MPEG, M4A e WAV no mesmo ffmpeg limitado e recusar áudio longo antes do binário.
- `f7be14d162e8dc23df04d82999f91236b2356273` — Atualizar contato pela API pública sem gravar documento nem reabrir titular excluído.
- `5668602e0d875999b78185b908bdc5d9115cfc2a` — Registrar o fechamento da frente de produto com a validação conjunta dos pacotes tocados.

Privacidade, ffmpeg e `PATCH /v1/contacts/{id}` entraram inteiros. `automatic_purge` continua falso. O webhook de contato (V2) não foi reaberto.

## Frente de operação

Worktree `/home/enok/srv_projetos/workspace/chat-isp-cursor-operacao`, branch `continuation/cursor-operacao`. O guia de subida continua em `docs/operacao.md`.

- `55e907ebc25268f4f4b0050e938430347bfe2f1a` — Medir incerteza, reservas, webhook e storage no registry privado.
- `47789b56f7a4f0fc7e0713f19967b0bd25f9771b` — Registrar o guia de operação e o ensaio local de carga.
- `fa956b341ad0d733a4e09062475d1523f6f0ec57` — Registrar os commits da frente de operação.

Séries no registry privado, `metrics.prom` do ensaio de restore, `scripts/ops-security-check.sh`, `scripts/ops-load-local.sh` e o perfil sintético entraram inteiros. `opsignals.Collect` permanece em `backend/cmd/chatisp/main.go` e em `backend/cmd/m2demo/demo.go`. O ensaio local continua recusado como capacidade.

## Migrações

As duas frentes criaram um arquivo `00046` com nomes diferentes. O merge não conflitou no Git. A de privacidade ficou em `00046`. A de sinais operacionais foi renomeada, com o mesmo SQL, para o próximo número livre.

- `backend/db/migrations/00046_privacy_subject.sql` — privacidade do titular.
- `backend/db/migrations/00047_m8_ops_signals.sql` — sinais operacionais. Referências ao nome antigo foram atualizadas em `docs/contracts/m8-ops-signals.md` e neste arquivo.
- `backend/db/migrations/00048_public_conversation_write.sql` — só acrescenta o escopo `conversations.write` na restrição de `api_tokens` e em `api_token_issue`. Não cria tabela.

## API pública nova

Escopo `conversations.write`. O legado `read` não o cobre. O painel de integrações passa a oferecer o escopo. Contrato: `docs/contracts/m6-public-api.md`. Documento: `backend/api/public-openapi.json`.

| Método | Caminho | Atendimento reutilizado |
| --- | --- | --- |
| POST | `/api/public/v1/conversations/{ref}/messages` | `Service.Send`, em nome do atendente atual |
| POST | `/api/public/v1/conversations/{ref}/assignment` | `Service.TransferToMember` |
| POST | `/api/public/v1/conversations/{ref}/state` | `Service.Close` ou `Service.Reopen`, sem despedida |

A chave é do token e do tenant. Texto livre continua na janela de 24 horas. Atribuição e estado usam um administrador ativo do provedor como ator, porque o token não tem sessão. Teste só com PostgreSQL embutido e dados sintéticos. Não houve chamada a Meta, ERP ou Evolution.

## Evidência desta integração

Comandos na worktree, saída 0:

- `npm ci --prefix backend/tools/pg --no-audit --no-fund` — 1 pacote.
- `go test -count=1 -timeout 240s ./internal/publicapi/ ./internal/retention/ ./internal/retention/httpapi/ ./internal/media/` — ok em 2,983s, 2,551s, 2,426s e 8,258s. Inclui `TestPublicConversationTextAssignAndState`.
- `go test -count=1 -timeout 240s ./internal/platform/opsignals/ ./internal/platform/metrics/ ./internal/loadlocal/` — ok em 2,395s, 0,078s e 0,022s.
- `go test -count=1 -timeout 240s ./cmd/chatisp/ -run 'TestOps|TestAPIWiring|TestSyntheticLocalLoad|TestJobsPool|TestPercentile|TestRefuse|TestReadProcess|TestFormat'` — ok em 3,864s. O relatório do ensaio continua com `aceito_como_capacidade` falso. Isso não é capacidade.
- `go vet` desses pacotes — sem diagnóstico.
- `./scripts/ops-security-check.sh` — RLS, CSRF, webhook, SSRF e mídia ativa passaram.

A suíte inteira do repositório não rodou. O Vitest do painel e o ensaio de restore não foram repetidos aqui.

## Como subir

Na worktree integrada:

```
./scripts/dev.sh
```

Portas padrão, conferidas em `scripts/dev.sh` e `docs/operacao.md`:

- API `http://127.0.0.1:8080`
- Painel `http://127.0.0.1:5173`
- Métricas `http://127.0.0.1:9090/metrics`

O script recusa endereço que não seja loopback. O cookie de sessão continua `Secure`; em HTTP o navegador não guarda a sessão. Não fazer deploy.

## Pendências externas — instrução

Estas decisões continuam fora desta branch. Enquanto não forem respondidas, o código fica como está.

- D2-06, expurgo. Escreva o enquadramento do provedor em `docs/legal/` antes de ativar expurgo. Não grave `automatic_purge=true` e não trate o `POST /api/panel/retention/purge` como ligado. O caminho enquanto a decisão não chega está em `docs/perguntas.md`, seção D2-06.
- D2-07, capacidade. Escreva em `docs/perguntas.md`, seção D2-07, o perfil real do piloto: provedores, atendentes simultâneos, números, mensagens, mídia, retenção, p95, RPO e RTO. Só depois disso rode k6. Até lá, não publique o ensaio local nem qualquer p95 deste repositório como capacidade.
- D2-03, Meta. Homologue com app, WABA, número, permissões e credencial reais, e registre a versão da Graph API em `docs/integracoes/` quando a documentação vigente for fixada. O servidor falso não é essa homologação.
- D2-01, ERP. Documente versão, API e autenticação do ERP usado em `docs/integracoes/erp-existente.md` e substitua o fake pelo adaptador dessa interface. O fake não é homologação.
- D2-04, Evolution. Fixe a release, a licença e o aceite antes de ligar a flag. Ela permanece desligada.

## Fechamento desta continuação

O HEAD anterior era `101ab8729488a7258473aff9e3b94bfc53a99595`. Estes commits fecham lacunas que ainda eram código, teste, contrato ou guia. Nenhum item está em 100%. Não houve push, banco real, credencial, Meta, ERP, Evolution nem expurgo.

- `b6e95e88522c96c91eb28bf1639f6b00b891cbbb` — Guardar configuração, caixa e catálogo no cache por tenant.
- `413a2100e98d98bbd25a6b52909c0265527d3e8d` — Registrar a Evolution local desligada e o destino das séries.
- O commit que grava este arquivo vem logo depois de `413a2100e98d98bbd25a6b52909c0265527d3e8d`.

| Requisito | Estado |
| --- | --- |
| 14-cache | implementado e verificado |
| 01-evolution | implementado mas dependente de homologação externa |
| 01-cloud-api e o restante do driver Meta já existente | implementado mas dependente de homologação externa |
| 08-conector-erp e 14-circuit-breaker | parcial: disjuntor, tempo limite e concorrência já estão no painel e no teste local; o módulo sony/gobreaker não entrou no go.mod |
| 14-metricas-alertas | implementado e verificado no listener privado; o painel cita `GET /metrics` e não mostra os totais do banco inteiro |
| 12-retencao e 14-retencao | implementado e verificado no caminho que recusa o expurgo; `automatic_purge` continua falso |
| 14-carga | parcial: o ensaio local e o registro de que não é capacidade já existiam e não foram repetidos; falta a resposta da D2-07 |

Contratos novos: `docs/contracts/m1-tenant-cache.md`, `docs/contracts/m6-evolution.md`, `docs/integracoes/meta.md` e `docs/integracoes/erp-existente.md`.

## O que continua fora de alcance

- Ligar a Evolution. Falta a release, a licença e o aceite da D2-04, registrados em `docs/perguntas.md` e em `docs/contracts/m6-evolution.md`.
- Homologar a Meta. Falta app, WABA, número, permissão, credencial e a versão da Graph API. O lugar é `docs/integracoes/meta.md` e a D2-03 em `docs/perguntas.md`.
- Trocar o fake do ERP pelo adaptador do fornecedor. Falta versão, API e autenticação na D2-01, em `docs/integracoes/erp-existente.md`. A verificação do cliente (D2-02) também falta; a ação sensível continua oculta.
- Ativar expurgo. Falta o enquadramento em `docs/legal/`, como diz a D2-06 em `docs/perguntas.md`. `automatic_purge` permanece falso.
- Publicar capacidade. Falta o perfil do piloto na D2-07 em `docs/perguntas.md`. O ensaio local não é esse número. k6, backup de produção, RPO e RTO continuam sem esse perfil.
- Passagem de um operador no navegador autenticado não foi refeita.
- Webhook de contato criado ou alterado continua V2.
- O módulo sony/gobreaker não substitui o disjuntor já testado do pacote.

## Rodada de 04/11/2026 (noite) — HTTPS local, disjuntor, matriz e validação

Base: `d525f90`. Worktree `chat-isp-cursor-integracao-2`, branch `continuation/cursor-integracao-2`. Não houve push, deploy, banco real, Meta, ERP, Evolution nem expurgo. Paseo não foi tocado. A pasta `chat-isp` (branch `continuation/m5-runtime-panel`, `428627b`) está 52 commits atrás desta e não deve ser usada como base.

Entregas:
- `71d0cda` — `CHATISP_DEV_TLS=1 ./scripts/dev.sh`: certificado autoassinado descartável, Vite em HTTPS, origem `https://`. Login real pelo proxy retornou 200 com cookies `__Host-session` e `__Host-csrf` `Secure`. Detalhe em `README.md` e `docs/operacao.md`.
- `6b6e315` — disjuntor do ERP reabre na hora quando a sondagem meio-aberta falha (antes só contava 1 falha); `breaker_test.go` novo. Decisão sobre `sony/gobreaker` em `docs/integracoes/erp-existente.md`: manter o pacote próprio, equivalente.
- `6b6e315` — `docs/matriz-requisitos.md` com as 257 linhas. Só 9 têm estado explícito; as outras 248 estão **A conferir**. Não é conclusão.
- Commit seguinte — ESLint limpo nos specs E2E.

Evidência desta rodada (HEAD com esses commits, Go 1.27.3; Node 20 para `tsc`, Node 24.11.0 em `/tmp/chatisp-node24` para Vitest e ESLint):
- `go vet ./...` — 0.
- `go test -count=1 ./...` — 0; 63 pacotes ok, incluindo `restoredrill` e `erp`, sem falha.
- `tsc -b` — 0. `eslint .` — 0 (depois das correções). Vitest completo — 53 arquivos, 273 testes ok.
- Migrações em banco novo: aplicadas pelos testes com PostgreSQL embutido. Atualização de versão anterior sintética e E2E de navegador **não foram executados** (não há `/opt/paseo-browser`).

Pendências internas: conferir as 248 linhas "A conferir" da matriz contra código e teste; jornada autenticada no navegador (E2E m3b, m4a, m4b-m6, m7-ops com Chromium); `scripts/dev.test.sh` falha desde antes (aborta com "pid ausente" em `vite.pid` no cenário de falha); teste de upgrade de migração.
Próxima tarefa concreta: conferir a matriz por marco, começando por M1 e M2 (autenticação, RLS, isolamento), e rodar o E2E com um Chromium disponível.
Pendências externas: iguais às da seção anterior (D2-01 a D2-07).
Como retomar: `cd` na worktree, `git log --oneline -5`, abrir `docs/matriz-requisitos.md`; subir com `CHATISP_DEV_TLS=1 ./scripts/dev.sh` (portas livres via variáveis do README).

Atualização (05/11/2026): matriz de M1 a M4 classificada (`4e9a665` e o commit seguinte). Ficaram "A conferir" em M3/M4: 03-filtro-etiqueta, 04-emojis, 04-formatacao, 04-atalhos, 13-atalhos, 04-compositor, 04-drag-colar, 04-preview, 07-bloquear, 07-mesmo-numero, 07-email, 07-avatar, 07-notas, 02-receber-video, 02-figurinhas, 02-janela, 02-interativos, 02-saude-numero, 02-limites-numero, 14-uso-meta. Próxima tarefa: conferir esses 20 e depois M5 a M8.

Atualização (05/11/2026, 2): matriz de M5 a M8 classificada. Totais: 149 verificados, 35 aguardando homologação externa, 25 parciais, 48 a conferir (só 08-abrir-cadastro em M5–M8). Classificação por nomes de teste existentes e pela suíte Go/Vitest verde; os testes não foram reexecutados um a um. Parciais relevantes: 05-ativar-desativar, 05-reatribuir-desativado, 05-supervisor-filas, 06-gatilhos, 06-fora-expediente/boas-vindas/ninguem-disponivel (plano descreve envio automático como pendente; conferir de ponta a ponta), 08-aprovacao, 08-verificacao-cliente (D2-02), 09-painel-rt, 12-retencao/14-retencao (D2-06), 14-carga (D2-07). Próxima tarefa: fechar os parciais que são código (reatribuição ao desativar, gatilhos, aprovação de ação sensível, mensagens automáticas) e conferir os "A conferir".

Atualização (05/11/2026, 3): fechados parciais que eram código ou texto defasado. `backend/internal/attendance/pipeline/disabled.go` (`ReleaseDisabled`, chamado em `Automation.RunTenant`) devolve à fila, sem tolerância, as conversas abertas ou aguardando de matrícula desativada, com o evento `offline_unassigned` (sem migração nova; relatórios e interface já o tratam). `disabled_test.go` cobre ativa, desativada e repetição. Boas-vindas, fora do expediente e "ninguém disponível" já eram decididos e testados em `triage/integration_test.go`; o texto "pendente" do plano está desatualizado. `go vet ./...` e `go test -count=1 ./...` passaram depois da mudança. Totais da matriz: 155 verificados, 35 aguardando homologação, 19 parciais, 48 a conferir. Parciais restantes que são código: 06-aprovacao de ação sensível (08-aprovacao), 09-painel-rt, 05-supervisor-filas; os demais dependem de D2-xx ou de navegador. Vitest e E2E não foram repetidos nesta mudança (só backend).

Atualização (05/11/2026, 4): `05-supervisor-filas` ganhou teste (`attendance/service/queue_supervisor_test.go`); `09-painel-rt` já tinha cálculo e testes (`reports.livePicture`, `query_test.go`, `attendance-report.test.tsx`) e foi reclassificado. `08-aprovacao` ficou parcial por decisão: não há ação sensível a aprovar até D2-01/D2-02; construir junto com a primeira ação sensível. Totais: 157 verificados, 35 aguardando homologação, 17 parciais, 48 a conferir. Próxima tarefa: conferir as 48 linhas "A conferir" (as 20 de M3/M4 listadas acima mais as de M1/M2), depois E2E no navegador com um Chromium e teste de upgrade de migração.

Atualização (05/11/2026, 5): as linhas "A conferir" foram confrontadas com código e testes. `04-formatacao` e `04-emojis` existiam sem teste; `web/src/features/conversation/composer-tools.test.tsx` foi criado (Vitest, passa; eslint e tsc limpos). Descobertas: não implementados `07-avatar` (campo só no backend), `02-saude-numero`, `02-limites-numero`, `14-uso-meta`, `08-abrir-cadastro` (depende de D2-01). Parciais novos: `14-logs` (handler sem tenant_id automático), `12-auditoria` (só a desativação tem teste), `14-ordenacao` (ordena por ingested_at, não pelo horário do driver). Ficaram "A conferir": `13-pt-br`, `07-mesmo-numero`, `04-compositor`. Totais: 182 verificados, 42 aguardando homologação, 26 parciais, 6 não implementados, 3 a conferir. Próxima tarefa: implementar os pequenos não implementados que não dependem de externo (07-avatar na interface, tenant_id nos logs, ordenação pelo horário do driver), depois E2E com Chromium e teste de upgrade de migração.

Atualização (05/11/2026, 6): `internal/platform/logging` ganhou `WithTenant` e um handler que grava `tenant_id` nos registros feitos com contexto; o pacote `auth` passou a usar `InfoContext/WarnContext/ErrorContext` e a pôr o tenant no contexto nos métodos de ator (14-logs segue parcial: faltam os demais pontos de log e os de antes da resolução do tenant). `14-ordenacao` já estava implementada no cliente (`trusted_at`, com fallback) e ganhou `attendance-adapter.order.test.ts`. `07-avatar` não foi implementado de propósito (nada preenche o campo; sem fonte nem proxy seguro). `go vet ./...` e `go test -count=1 ./...` passaram; ESLint e tsc passaram nos arquivos web novos. Totais: ver linha Contagem da matriz. Próxima tarefa: converter os demais logs para a variante com contexto, depois E2E com Chromium e teste de upgrade de migração.

Atualização (05/11/2026, 7): logs com escopo de tenant convertidos para `*Context` com `logging.WithTenant` em `httpapi/panel.go`, `realtime/connection.go`, `cmd/chatisp/arrival.go` e `channel/configuration` (além do `auth`). Ficam sem `tenant_id` por natureza: runtime, migrate, panic recovery, webhook da Meta antes de resolver a rota e login antes de achar o vínculo. `go vet ./...` e `go test -count=1 ./...` passaram. 14-logs foi para "verificado" com essa ressalva (conversão por revisão e compilação, sem teste por ponto). Próxima tarefa: E2E com Chromium e teste de upgrade de migração; conferir 13-pt-br, 07-mesmo-numero e 04-compositor.

Atualização (05/11/2026, 8) — E2E no navegador. O Chromium do Playwright já estava em `~/.cache/ms-playwright/chromium-1193` (Chromium 140.0.7339.186); não foi preciso baixar nada. A porta 8080 deste host é de outro serviço, então cada jornada roda com `CHATISP_E2E_API_PORT=<porta livre>` e Node 24 em `PATH` (`/tmp/chatisp-node24/bin`). Resultados em `27d3f6d`+correções: `test:e2e:m3b` passou (3 contextos, 2 setores, cursor 2→16, replay idêntico, cache revogado); `test:e2e:m4a` passou; `test:e2e:m7-ops` passou; `test:e2e:m4b-m6` falhou e foi corrigido.

Causa da falha do m4b-m6: (1) o fixture `cmd/m2demo` montava `service.New` sem `Files` e `AttachmentLimits`, então o envio de áudio, que desde `1a13ae5` passa pelo ffmpeg no servidor, devolvia 503; agora o fixture liga os dois (`cmd/m2demo/demo.go`), como o `cmd/chatisp/main.go` já fazia. (2) o stub do MediaRecorder enviava só um cabeçalho ID3 de 10 bytes; foi trocado por um MP3 real de 0,6 s (~1 KB, base64) em `web/e2e/m4b-m6.spec.ts`, e o passo do áudio passou a falhar com o texto do compositor e os erros HTTP. Depois disso `test:e2e:m4b-m6` passou ("simulação local, sem skip"). Isto não é homologação com a Meta: ERP fake, stand-in Meta e receptor loopback.

Não repetido: m4b-m6 e demais jornadas não foram reexecutados em sequência depois do fixture novo (só o m4b-m6); Vitest completo; teste de upgrade de migração; `scripts/dev.test.sh` segue falhando.

## Fechamento da continuação (05/11/2026)

Branch `continuation/cursor-integracao-2`. Matriz completa em `docs/matriz-requisitos.md` (257 linhas). Nenhum item está declarado em 100%: restam parciais, não implementados e itens que dependem de decisão externa. Não houve push, deploy, banco real, Meta, ERP, Evolution nem expurgo (`automatic_purge` continua falso). Paseo não foi tocado.

### Validação integrada (HEAD `ca8af9b`, 05/11/2026)
Go 1.27.3; Node 20.20.2 para `tsc`; Node 24.11.0 (`/tmp/chatisp-node24/bin`) para Vitest, ESLint e E2E; Chromium 140.0.7339.186 do Playwright (`~/.cache/ms-playwright/chromium-1193`). Todos com saída 0:
- `go vet ./...`; `go test -count=1 ./...` (65 pacotes ok).
- `tsc -b`; `eslint .`; Vitest completo (56 arquivos, 278 testes).
- `bash scripts/dev.test.sh` ("ok", agora com o cenário HTTPS); `scripts/ops-security-check.sh`.
- E2E no navegador: `test:e2e:m3b`, `m4a`, `m4b-m6`, `m7-ops` (cada um com `CHATISP_E2E_API_PORT=<porta livre>`, porque a 8080 do host é de outro serviço). Stand-in da Meta, ERP fake e receptor loopback: não é homologação.
- Migrações: aplicadas em banco novo por todos os testes com PostgreSQL embutido; atualização da base `00045` (commit `54febe9`) até a versão atual com dados sintéticos em `internal/upgradetest`.

### Entregas desta continuação
Login local em HTTPS (`CHATISP_DEV_TLS=1 ./scripts/dev.sh`); disjuntor do ERP reabre em sondagem falha (decisão sobre sony/gobreaker em `docs/integracoes/erp-existente.md`); conversas de atendente desativado voltam à fila (`pipeline/disabled.go`); `tenant_id` nos logs com escopo de tenant; citação sem UUID zero; limites de CPU/memória/arquivo para o ffmpeg via prlimit; plano da busca medido (`internal/searchplan`); fixture do E2E ligado a armazenamento e limites; vários testes novos (fila do supervisor, mesmo número em dois provedores, auditoria, atalhos, ferramentas do compositor, ordem da linha do tempo).

### Como iniciar, acessar e encerrar
`CHATISP_DEV_TLS=1 ./scripts/dev.sh` (portas por variável, ver `README.md`); abrir `https://127.0.0.1:5173/entrar`, aceitar o certificado descartável; usuário `dev.local@example.com` e a senha impressa na primeira subida. Ctrl+C encerra só PostgreSQL, API e Vite do script e apaga o certificado.

### Próxima tarefa concreta
Quando houver decisão externa: D2-03 (Meta) destrava 02-saude-numero, 02-limites-numero, 14-uso-meta e a homologação de mídia/templates; D2-01/D2-02 (ERP) destravam 08-abrir-cadastro, 08-verificacao-cliente e 08-aprovacao. Localmente, o que sobra é medir a consulta composta da busca da fila (03-busca-conteudo, 14-fulltext), auditar responsividade e acessibilidade no navegador (13-responsiva, 13-a11y), exercer a Notification API real (04-notif-navegador) e decidir a fonte do avatar (07-avatar).

### Como retomar
`cd /home/enok/srv_projetos/workspace/chat-isp-cursor-integracao-2`; `git log --oneline -8`; ler `docs/matriz-requisitos.md`; a pasta `chat-isp` (branch `continuation/m5-runtime-panel`) está 52+ commits atrás e não deve ser base.

## Rodada de pendências internas (05/11/2026, depois de `454e8c3`)

Pedido: fechar o que ainda era executável localmente. Evidências em `docs/medicoes/busca-fila.md` e `docs/medicoes/a11y-notificacoes.md`.

- **Busca composta da fila**: `CHATISP_PERF=1 go test -run TestQueueComposedSearchOnSyntheticVolume -v ./internal/attendance/store/` (2 tenants × 20 000 conversas × 100 000 mensagens). Agente < 1 s, admin até 2,2 s no pior caso; o plano real varre por candidata e não usa o GIN; a troca por subconsulta não correlacionada piorou e foi descartada.
- **Navegador**: `npm --prefix web run test:e2e:ux` (Chromium 140, axe-core 4.10.2 fixado, 24 visões). Corrigidos: anel de foco invisível (`outline-none` + `focus-visible:outline-2` no Tailwind v4 exige `outline-solid`), `aria-expanded` indevido, alvos < 24 px, regiões roláveis sem teclado, estouros horizontais a 768 e 360 px. Notificações locais exercitadas (concedida, sem concessão, API ausente, pedido concedido/negado/rejeitado, construtor que lança).
- **Avatar**: iniciais determinísticas (`web/src/components/ui/contact-avatar.tsx`), porque a especificação só pede foto se o WhatsApp fornecer.
- **Uso reportado pela Meta (14-uso-meta)**: `retention.Service.MetaUsage` + `GET /api/panel/meta-usage` + painel em Relatórios; contrato do campo `pricing` conferido na documentação pública.
- **Docker**: o host tem Docker 20.10.9 **sem a ponte `docker0`**; `docker compose config` passa, o build falha (rede do build, `apt` do Debian 13 e `npm ci` do Node 24). Não se desligou seccomp nem se mexeu na rede do Docker. Consolidado `deploy/compose.yaml` (o `compose.yml` M2 virou `compose.yaml`; o antigo da M1 saiu), `.env.example` completo com teste de guarda, `ffmpeg` na imagem, e `scripts/compose-validate.sh` para um host com Docker atual.
- **Incidente de operação**: ao limpar imagens do meu build rodei `docker image prune -f --filter until=2m`, que apagou **imagens sem etiqueta** de outros projetos (cerca de 2,4 GB, versões antigas dangling; nenhuma imagem com etiqueta nem contêiner). Contêineres e imagens etiquetadas seguem intactos; as imagens sem etiqueta precisam ser reconstruídas se alguém dependia delas.
- **Meta, número (02-saude-numero, 02-limites-numero)**: contrato do webhook `phone_number_quality_update` verificado (`docs/integracoes/meta.md`). Falta decidir o roteamento por WABA + número de exibição (o webhook não traz `phone_number_id`); a nota de qualidade só vem da leitura por Graph (token + versão, D2-03).
- **Reclassificadas**: 12-retencao e 14-retencao passaram a "aguardando homologação externa" (o plano já define expurgo desligado até D2-06; recusa e classes implementadas e testadas).

Matriz: 204 verificados, 44 aguardando homologação externa, 6 parciais (12-https, 13-self-host, 13-compose por causa do Docker do host; 08-aprovacao e 08-verificacao-cliente pelo ERP; 14-carga pelo perfil D2-07), 3 não implementados (02-saude-numero, 02-limites-numero, 08-abrir-cadastro).

## Revisão geral e fechamento do código local (05/11/2026, depois de `e0ba6cd`)

Releitura de especificação, plano, matriz e documentos de entrega. Feito nesta rodada:

- **02-saude-numero e 02-limites-numero**: webhook `phone_number_quality_update` decodificado e ligado ao número por App + WABA + dígitos do número exibido, só com correspondência única (sem palpite; o resto vai para quarentena); leitura sob demanda de `quality_rating`; painel "Saúde e limites dos números" em Caixas Meta com erros essenciais de 7 dias. Migração `00049`. Detalhes em `docs/integracoes/meta.md`.
- **08-abrir-cadastro**: modelo de link HTTPS com `{cliente}` configurado pelo admin, sem credencial nem CPF/CNPJ, mostrado em cada vínculo do ERP. Migração `00050`. Detalhes em `docs/integracoes/erp-existente.md`.
- **Defeito corrigido**: o modo oficial do `DriverFactory` fixava a versão `v23.0` da Graph, contrariando a D2-03 em aberto. Agora a versão e a URL de documentação são obrigatórias e sem padrão.
- **Defeito corrigido**: dez seções de Configurações tinham a mesma chave React; agora cada uma tem a sua. A auditoria de UX passou a falhar com chave repetida.
- **Acessibilidade**: auditoria ampliada para 52 visões (MFA, convite, recuperação de senha, usuários do provedor, reflow 320×256); o seletor "Caixa" estourava 5 px a 320 px e foi corrigido.
- **Gate de qualidade da especificação** (`scripts/lint.sh`), que nunca tinha passado: 13 achados do golangci-lint corrigidos (atribuições sem efeito, funções sem uso e um erro ignorado em teste); `gofmt` em dois testes; `govulncheck` achou GO-2026-5764 (pânico no EventStream do AWS SDK, alcançável pelo armazenamento S3), corrigido subindo `service/s3` para v1.97.3 e `eventstream` para v1.7.8 (vendor refeito); modelos do sqlc desatualizados regenerados. Resta GO-2026-5932 (`x/crypto/openpgp`), não alcançável pelo código, com correção só em pseudo-versão.

Validação no tree final: `go vet` e `go test -count=1 ./...` (65 pacotes ok); `scripts/lint.sh` (golangci-lint 0, govulncheck 0 alcançável, ESLint, tsc, OpenAPI e sqlc em dia); Vitest 60 arquivos e 288 testes; E2E `m3b`, `m4a`, `m4b-m6`, `m7-ops` e `ux` (52 visões, 0 achados); `scripts/dev.test.sh`; `scripts/ops-security-check.sh`.

Matriz: 204 verificados, 47 implementados aguardando homologação externa, 6 parciais, 0 não implementados. Os 6 parciais dependem de fora: 12-https, 13-self-host e 13-compose do Docker do host (20.10.9 sem `docker0`; `scripts/compose-validate.sh` pronto), 08-aprovacao e 08-verificacao-cliente do ERP (D2-01/D2-02) e 14-carga do perfil do piloto (D2-07).

## Verificação de entrega (05/11/2026, depois de `b2b6910`)

Relatório: `docs/medicoes/verificacao-entrega-20261105.md`. Nenhum código de produção mudou; testes da rodada anterior reutilizados.

- **Docker/Compose não validado.** Duas causas reproduzidas com sonda (`docs/medicoes/docker-20261105/`): (1) o daemon roda com `--iptables=false` e a rede `bridge` aponta para `docker0`, que não existe, então todo `RUN` na rede padrão falha; (2) o seccomp do 20.10.9 nega `clone3` com EPERM, e por isso `apt` do Debian 13 e Node 24 abortam mesmo com `--network host`. O perfil upstream com ENOSYS não resolve neste daemon. O builder descartável (BuildKit rootless) exigia desligar seccomp/apparmor no contêiner e foi negado. Procedimento para host compatível no relatório.
- **Lacuna escondida:** não há caminho de produção para criar o superadmin nem o primeiro admin de um provedor. 12-superadmin e 14-provisionamento voltaram a parcial; afeta 13-self-host.
- **Incidente do `image prune`:** comando, horário e o que se pode inferir estão no relatório; os IDs removidos não foram registrados (`| tail -1`). Os 80 contêineres têm a imagem e as etiquetas que usam. Ficou presente a sobra do build anterior (contêiner `172052ca40ee`, imagem `933e4b0aa2d1`): a remoção foi negada pela política de permissões; os comandos estão no relatório.
- **Matriz:** 202 verificados, 33 aguardando homologação, 22 parciais. 13 linhas de ERP e 01-evolution saíram de "homologação" porque `erp/real.go` é stub e o Evolution é só contrato.
- **Segurança:** `docs/medicoes/govulncheck-20261105/`. GO-2026-5932 (`x/crypto/openpgp`) não está no grafo de build (`go list -deps -test`, `vendor/modules.txt`, `-scan package` = 0).

Atualização (mesmo dia): a lacuna do primeiro usuário foi implementada direto nesta sessão (o projeto não usa mais o `paseo-flow`). `chatisp bootstrap -email ... [-superadmin]` (senha pela entrada padrão, só em instalação vazia, retomável, auditado) e `POST /api/platform/tenants/{id}/admin-invitations` com "Convidar administrador" no painel. `compose-validate.sh` passou a provar bootstrap, login pelo Caddy, reinício e encerramento sem sobras, com etiquetas de imagem próprias. Go completo, `lint.sh`, Vitest (60/288) e E2E `ux`/`m7-ops` verdes. Matriz: 204 verificados, 33 aguardando homologação, 20 parciais.

Atualização (05/11/2026, 15:33): Compose validado de ponta a ponta neste host com bases musl (`CHATISP_VALIDATE_ALPINE=1 ./scripts/compose-validate.sh`, `deploy/validacao/Dockerfile.alpine`, `postgres:18.6-alpine`). A validação achou e corrigiu um defeito real: o `deploy/Caddyfile` com só `:443` falhava no TLS; agora tem `localhost:443, 127.0.0.1:443` e `default_sni` (teste `TestCaddyfileNamesTheInternalCertificate`). Também achou que `postgres:18.6-trixie` não roda em Docker 20.10.9 (`initdb`). Sobras desta tarefa removidas uma a uma; ficou só o cache de build de 05/11 (remoção por ID negada; IDs em `docs/medicoes/docker-20261105/build-cache-20261105.txt`). Matriz: 205 verificados, 33 aguardando homologação, 19 parciais.

Atualização (05/11/2026, 16:10): com autorização do usuário, `./scripts/compose-validate.sh` original (imagens Debian, `postgres:18.6-trixie`) passou num Docker 27.5.1 rootless descartável, sem mexer no Docker compartilhado; ambiente e pacotes auxiliares removidos depois. Cache de build de 05/11 removido por ID (2,1 GB → 867 kB). 13-self-host e 13-compose verificados. Matriz: 207 verificados, 33 aguardando homologação, 17 parciais; os 17 dependem de D2-01/D2-02 (ERP), D2-04 (Evolution) e D2-07 (carga).

Próxima tarefa: nenhuma local. Aguardar as decisões externas D2-01, D2-02, D2-03, D2-04, D2-06 e D2-07 (`docs/perguntas.md`).

## Retomada solicitada em 05/11/2026 — estado revisto

A frase histórica "nenhuma tarefa local" está revogada. Base desta retomada: `b43edbb`, branch e worktree `continuation/cursor-integracao-2` / `chat-isp-cursor-integracao-2`. Fontes não versionadas preservadas em snapshot privado sem credenciais antes de editar. Não se usou a pasta antiga nem se alterou Paseo, Agent Control ou outros projetos.

Correções e provas estão consolidadas em `docs/medicoes/retomada-20261105/relatorio.md`, `docs/backup.md`, `docs/implantacao.md` e `docs/dependencias-externas.md`. A tabela neste último é a única lista de dependências externas; não confundir código ausente com homologação.

Nesta rodada: espelho append-only e manifesto completo; agendamento executável; volumes por projeto; locks e checksums; recuperação efetiva do anel perdido e autenticação AES-GCM; cobertura banco/objetos; reaplicação idempotente da exclusão e anonimização; associação de objeto registrada ao excluir; redação de conteúdo estruturado/eventos; catálogo Graph ligado ao runtime com flag off; suspensão transitória limitada e quarentena; preview de retenção sem expurgo; recuperação da sessão antes de redirects; limite de storage no formulário; produção sem dependência da CA interna; regras/entrega de alertas opt-in e parametrizadas.

Migrações novas: `00051_privacy_deletion_objects.sql` e `00052_suspended_ingress.sql`. Seus rollbacks recusam descartar registros de recuperação. A base anteriormente existente continua preservada.

Validação: Go amplo (65 pacotes aprovados, fixture novo de cmd corrigido e pacote repetido integralmente com sucesso), vet, lint completo, Vitest 61/290, 16 testes Python, 8 cenários promtool de backup, 7 operacionais e entrega Alertmanager sintética local. A prova Compose nova usa Alpine no Docker 20.10.9 deste host; a execução Debian anterior continua sendo evidência histórica, não verificação do código novo em Debian.

Pendências que permanecem **implementação parcial**: adaptador ERP real e mecanismo de verificação/aprovação sensível dependem do contrato do fornecedor; conector de rede Evolution depende da release/licença/contrato; executor de expurgo depende de regras explícitas por classe e reaplicação completa das exclusões; roteiro de carga depende do perfil/metas. Todas são separadas de credencial, ambiente e autorização de homologação. Nenhum produto 100%, PITR/RPO/RTO nem produção implantada foram declarados.

Resultado final: `CHATISP_VALIDATE_ALPINE=1 CHATISP_VALIDATE_BACKUP=1 ./scripts/compose-validate.sh` passou integralmente com saída 0 no código `bdf7f5e`, depois de `7827a90` (pacote principal). Evidência sem credenciais: `docs/medicoes/retomada-20261105/compose-final.txt`. Perda real de volumes/chaves, restore, MFA, integridade do anexo mantido, ausência do excluído e anonimização preservada foram verificados. Nenhum recurso `chatisp-validate-*` restou (contêiner, volume, rede, etiqueta); caches de build ficaram preservados, sem prune global. O commit que registra esta continuidade e os documentos vem após esses dois commits de código.

Próxima ação exata: consultar `docs/dependencias-externas.md` e obter os contratos/decisões de ERP/verificação, Evolution, retenção por classe e perfil de carga antes de implementar seus executores dependentes. Homologação Meta/SMTP/storage/alertas, destino independente/cofre, domínio/ACME e passagem de operador exigem dados/autorização externos. Manter envio oficial e expurgo desligados e não fazer push/deploy/chamadas reais por conta da conclusão do laboratório.

## Missão com dez frentes — 06/11/2026

Base limpa `e58a8a3`, mesma branch e worktree. O ambiente disponibilizou principal + dez subagentes; as dez frentes usaram explicitamente `gpt-6.1-terra`, `medium`, `fork_turns=none`. Propriedade de arquivos e revisão separada foram mantidas; frentes foram reutilizadas para os achados relacionados. Não houve Paseo/Agent Control, push, produção nem serviço externo real.

Código integrado no commit local `6647f7e`, com fontes idênticas às verificadas. A última prova `CHATISP_VALIDATE_ALPINE=1 CHATISP_VALIDATE_BACKUP=1 ./scripts/compose-validate.sh` passou com saída 0 no projeto `chatisp-validate-2282618`, incluindo falha/retry da exclusão e perda/recuperação real de volumes/chaves. O commit seguinte registra os documentos e evidências, sem mudar o código testado. Inventário dos três laboratórios: zero contêineres, volumes, redes ou etiquetas restantes; cache preservado e nenhuma limpeza global.

O usuário confirmou que ainda não tem os contratos ERP/verificação, release/licença Evolution, regras de retenção por classe ou perfil/metas/RPO/RTO. Esses dados não foram inventados. A [tabela única](dependencias-externas.md) mantém as informações exatas necessárias e a próxima ação; o relatório desta missão está em [missao-20261106](medicoes/missao-20261106/relatorio.md).

Correções locais: disjuntor ERP por geração; fake Evolution sem panic no valor zero; SMTP com STARTTLS obrigatório e certificado confiável; métrica de backup somente após registro no destino e frase de chaves não vazia; interface sem confirmação/exportação de contato anterior; script de carga com relatório visível e pré-requisito explícito; runner UX com SPKI efêmero e API loopback livre.

Privacidade recebeu três migrações: `00053` (fila de limpeza física posterior ao commit), `00054` (HMAC por tenant/conector e corte original de ingresso) e `00055` (proveniência e quarentena conservadora do legado sem prova). O livro externo leva os marcadores e o restore recusa cobertura histórica ausente ou contraditória. Atualização exige API/ingresso/workers antigos parados e transações encerradas; não suporta mistura de binários nem downgrade destrutivo. O procedimento está em `docs/implantacao.md`.

Mídia recebida grava intenção de upload com tamanho/orçamento reais antes do storage; revalida propriedade sob locks limitados por timeout e usa namespace UUID/mensagem UUID. Reservas antigas identificáveis também entram na limpeza retomável antes da redação. Mídias antigas já publicadas em `in/<tenant>/<mensagem>` ou órfãos sem vínculo não recebem migração presumida: o espelho recusa esse formato e o operador precisa inventariar/tratar o legado antes do aceite. Nada foi reparado em bucket real.

Gates executados e escopos históricos estão no relatório e em `testes.txt`: Go amplo (66 pacotes), vet/lint/gerados, Vitest 61/295, Python 21, race de pipeline/retenção e demais pacotes tocados, E2E M7/M4B–M6 e UX 52 visões sem falha. Os resultados e recursos da última prova Compose/backup constam em `compose-final.txt` da missão; não atribuir a prova Debian antiga ao novo código.

A matriz permanece em **206 verificados, 32 aguardando homologação externa e 19 parciais**, total 257. Os parciais continuam contendo implementação ausente dependente de contrato/regra/perfil. Envio oficial e expurgo automático permanecem desligados; homologações externas e aceite humano não foram substituídos por laboratório. A continuação é obter as decisões da tabela única, tratar eventual legado operacional e executar somente a homologação explicitamente autorizada.

## Continuação para uso local hoje — 06/11/2026

Novo pedido humano: procurar o que mais faltava, decidir os ajustes técnicos necessários e adiar domínio/serviços indisponíveis. Base `a0103d4`; código local integrado em **`0002570`**, mesma branch. As dez frentes foram reutilizadas com Terra Medium, sem Paseo, subdelegação, push ou conexão de fornecedor real.

Corrigidos adiamentos que sobreviviam a transições manuais; revogação e emissão concorrente de sessão/senha/MFA; saturação por checkout aninhado; conversão recusada sem persistência e conversões simultâneas duplicando quota; finalização de mídia durante suspensão; JSON adicional/excedente; limites coerentes de texto/assinatura; exportação interrompida do livro e aliases de configuração do restore; encerramento dos filhos antes do cleanup; retry e troca de setor na interface. A migração **`00056_auth_verified_password.sql`** protege o snapshot de credenciais e a revogação na transação correta, com privilégios específicos.

O launcher usa HTTPS por padrão. `deploy/init-local-env.sh` gera segredos 0600 sem sobrescrita; `deploy/compose.local-alpine.yaml` permite instalação local segura no Docker 20.10.9 deste host, sem apresentá-lo como perfil de produção. README e operação foram atualizados.

**Instalação entregue e mantida ativa:** projeto `chatisp-pronto-hoje`, `https://localhost:8443/entrar` no próprio servidor, administrador de provedor `admin@chatisp.local`, sem superadmin e com MFA obrigatório no primeiro acesso. Credenciais/configuração ficam no diretório privado `/home/enok/.local/share/chatisp/pronto-hoje-20261106/`, fora do Git. Apenas a porta HTTPS em loopback está publicada; métricas, PostgreSQL e Garage são internos. Este projeto e seus volumes são intencionais e não devem ser confundidos com laboratórios descartáveis.

Gates finais: Go 66 pacotes, vet/lint/gerados, race em cinco pacotes críticos, Vitest 61/297, Python 24, jornadas M3B/M4B–M6/M7 e UX 52 visões sem falha. A prova **`chatisp-validate-3000114`** concluiu backup, exclusão durante queda do storage, retomada e perda/recuperação de volumes e chaves com saída 0. Laboratórios `2797501` e `3000114` sem contêiner, volume, rede ou etiqueta; caches preservados. O primeiro acesso real local foi conferido por HTTPS, cookies, MFA pendente e logout, sem cadastrar fator em nome do usuário.

Evidências, operação e limites: [pronto-hoje-20261106](medicoes/pronto-hoje-20261106/relatorio.md). O próximo commit grava somente documentação. A matriz continua 206/32/19: domínio, SMTP, WhatsApp/ERP/Evolution e demais contratos/aceites ausentes seguem na tabela única, adiados neste escopo local. Envio oficial e expurgo automático permanecem desligados; a entrega local não é produção homologada.

## Publicação no domínio — 06/11/2026

Equipe efetiva conferida nos registros de sessão: principal Sol 6.1 Médio e três subagentes Terra 6.1 Médio, sem descendentes e sem uso/retomada do Paseo. Base `9cbc484`; configuração integrada em `026af83`, mantendo as imagens do código `0002570`, banco, anexos, senha e MFA ainda não cadastrado do administrador original.

O Nginx `10.10.0.10` já tinha certificado público para `chatisp.tokstiloesporte.com`, mas encaminhava por HTTPS ao Apache padrão do PNETLab neste host. Foi adicionado somente vhost nomeado ChatISP → listener Caddy loopback 8480; o acesso local TLS 8443 foi mantido. O perfil `deploy/compose.behind-proxy.yaml` reaproveita os volumes e define origem pública primeiro/local em segundo; anexos assinados usam o mesmo domínio e não exigem DNS adicional. O PNETLab preservou seu comportamento nos três listeners. A rota NexoISP já retornava o fluxo PNETLab antes e depois; não foi alterada nem declarada funcional no destino próprio.

Pela resolução direta do nome para a borda interna, passaram certificado público sem bypass TLS, login, MFA da conta sintética, atendimento autenticado, snapshot WebSocket, upload/download com bytes conferidos, recusa sem assinatura e expiração real da URL. Duas fixtures foram desativadas/suspensas e seus contatos excluídos pelo fluxo de privacidade, sem apagar trilhas. PostgreSQL/Garage/Caddy/API/worker reiniciados; login original e recuperação HTTPS local passaram depois. Métricas e API administrativa Garage continuam privadas; nenhum fornecedor ou expurgo foi ativado.

Backup anterior `snapshot-20261106T154305Z-zz4P0J1c`: PostgreSQL restaurado isoladamente e Garage recuperado com listagem S3 autenticada do bucket vazio. Snapshot posterior `snapshot-20261106T155314Z-ah2EJmad`: checksums verificados e cinco serviços saudáveis após retomada. Ambos no diretório privado `/home/enok/.local/share/chatisp/backups-publicacao/`; artefatos Apache antes/depois em `/home/enok/.local/share/chatisp/publicacao-20261106/`. Uma tentativa parcial sem manifesto foi identificada e preservada. Não houve limpeza global, perda de volume nem push.

**Objetivo externo ainda pendente:** o DNS normal resolve `177.104.156.25`, mas 80/443 recusaram conexão neste servidor, e a ferramenta web externa não conseguiu abrir o endereço. É necessário identificar/corrigir o encaminhamento público ao proxy interno, com acesso administrativo ao equipamento responsável. A prova interna não comprova acesso de Internet. Logs do Nginx e proteção por IP compartilhado continuam com limites explícitos. Todas as pendências oficiais foram revisadas; contratos/regras/metas ausentes não foram inventados. Consulte o [relatório e primeiro acesso](medicoes/publicacao-20261106/relatorio.md), o [procedimento de recuperação](medicoes/publicacao-20261106/backup.md) e a [lista única de dependências](dependencias-externas.md).
