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
