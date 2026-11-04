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
