# M3A — junção e aceite delimitado

Fonte normativa: [especificação V4.1](especificacao.md) e [plano M3](plano.md). Este lote reúne setores, autorização operacional por setor/caixa, diretório e histórico autorizado de contatos, produtividade do compositor e preferências de notificação por usuário. Não encerra o M3. Revisão independente e gate amplo de 13 comandos pertencem ao controlador e não foram executados nesta sessão.

## Implementação e evidências

As implementações de setores, contatos, compositor, notificações e sua composição HTTP/UI já estavam no HEAD recebido. Esta entrega acrescenta provas da junção e adapta o `m2demo` para criar setor padrão, vincular o agente à caixa e fornecer os serviços M3A ao runtime. A tentativa 4 acrescenta a migração incremental 00012, a fixture explícita do TestVertical e a compatibilidade de negação 403/404 na política canônica. As migrações 1–11 permanecem intactas.

- `backend/internal/acceptance/m3a_test.go`: servidor HTTP da composição de produção, sessões reais e PostgreSQL 18.4 descartável. Dois tenants, admin/agente/supervisor, dois setores e várias caixas; configuração e cadastro por HTTP, recepção pelo serviço real. Nenhuma conexão com Meta/ERP/Evolution.
- A prova cruza revogação de membership com próxima ação, snapshot, histórico do contato compartilhado e eventos já persistidos. Também consulta o leitor limitado usado pelo socket e verifica avanço do cursor sobre eventos invisíveis. O contato comum não concede acesso a outra conversa. Supervisor conserva visão do tenant.
- Após troca explícita do padrão por HTTP, remove vínculo populado com HTTP 200. A conversa original, setor histórico, protocolo e mensagem permanecem no banco. Agente ainda associado aos dois setores e com acesso à caixa pelo setor alternativo não recupera ação, snapshot, histórico ou evento revogado. Admin e supervisor também não recuperam o histórico sem vínculo; o contato compartilhado não concede acesso. Remoção do padrão ainda retorna 409.
- `m3a_revocation_test.go`: INSERT e reatribuição sem vínculo, com setor inexistente, inativo ou de outro tenant falham; a FK histórica impede apagar setor mesmo após remoção das referências operacionais. Quatro corridas reais (INSERT/UPDATE, cada ordem de vitória contra DELETE) verificam bloqueio via `pg_blocking_pids`, resultado após commit e preservação da atribuição que venceu. DELETE direto, sem o portão exclusivo mais forte do serviço, exercita a proteção do trigger por si só.
- A migração mantém FORCE RLS e usa trigger invocador, portão tenant compartilhado e locks de setor/vínculo até commit. FKs tenant/setor e tenant/caixa separam identidade histórica da autorização mutável; mudanças efetivas de atribuição validam vínculo ativo. Atualização sem mudança de identidade continua permitida no histórico revogado.
- `TestVertical` configura explicitamente setor ativo, membership, vínculo e padrão em cada caixa nova, antes dos webhooks. Todas as assertions M2 foram preservadas. A política mantém 404 para caixa errada somente após comprovar autorização da conversa na caixa correta; conversa revogada ou alheia continua 403. O sentinel de negação usa `attendance/store.ErrNotFound`, já entendido pelos consumidores existentes, sem conceder a operação.
- Bloqueio por HTTP recusa mensagem recebida nova, preservando histórico. Para envio humano em conversa assumida, a API ainda aceita a intenção na outbox (HTTP 201, estado queued); o dispatcher real marca a tentativa failed antes de chamar o driver local. A prova verifica essa fronteira e ausência de chamada ao driver, sem alegar rejeição imediata pela API. A prova específica de quarentena já existe em `backend/internal/attendance/pipeline/m3_contact_block_test.go`; ela é uma referência anterior, não um comando reexecutado por este lote.
- `web/src/test/m3a-attendance.test.tsx`: RTL com HTTP/socket simulados, telas de setores/contatos, rascunho por conversa, emoji, troca de sessão, permissão explícita de notificação e descarte de lote anterior. A troca de sessão verifica entradas existentes no cache do escopo antigo e sua remoção posterior. Revogação pelo socket limpa sessão, conversa, histórico, cache e rascunho.
- `backend/cmd/m2demo/main_test.go`: login real, endpoints M3A disponíveis, caixa autorizada para agente, webhook assinado local, conversa no setor padrão, histórico e upgrade WebSocket pelo proxy.

As provas anteriores de ferramentas, idempotência/offline, preferências e histórico estão em `web/src/features/conversation/message-composer.test.tsx`, `web/src/features/notifications/notifications.test.tsx`, `web/src/features/contacts/contacts-panel.test.tsx`, `history-events.test.tsx` e `web/src/features/sectors/sectors-panel.test.tsx`. Referenciar essas provas não significa que a suíte inteira tenha sido executada nesta sessão.

## Checklist de aceite M3A

- [x] Prova PG/HTTP M3A, incluindo revogação de membership, eventos persistidos e bloqueio no despacho.
- [x] Provas RTL dirigidas (4 testes) e demo com login/recepção/histórico/upgrade reais.
- [x] Checks essenciais com última execução aprovada (tentativa 4 abaixo).
- [x] Prova M2 TestVertical e demo preservados, com todas as assertions existentes.
- [x] Remoção de vínculo setor/caixa com histórico revoga acesso sem apagar histórico.
- [ ] Revisão independente do controlador.
- [ ] Gate amplo final no HEAD integrado pelo controlador.

### Registro dos impedimentos anteriores (tentativas 1–3)

Naquelas tentativas, a remoção do vínculo setor/caixa com conversas era impedida pela FK `conversations_sector_inbox_fkey` de `00009_m3_sectors.sql`: mesmo alterando o setor padrão primeiro, a exclusão recebia HTTP 409. O teste caracterizava esse conflito e comprovava remoção somente de vínculo vazio. A migração incremental necessária ainda estava fora do escopo autorizado. Esse registro explica as falhas anteriores; o comportamento atual é o descrito na implementação da tentativa 4.

A rodada essencial anterior também encontrou regressão na prova M2 existente: `TestVertical` falhava em `vertical_test.go:296` com `worker não criou conversa`. Sua fixture criava caixas novas sem configurar setor padrão; o trigger `conversations_assign_sector` da migração M3 exige esse destino. O backfill da migração só alcança caixas já existentes naquele momento. A fixture original foi preservada naquelas tentativas, pois não pertencia aos arquivos autorizados. O demo já havia sido adaptado e sua prova real de recepção passava; isso, por si só, não aprovava a prova M2 existente.

## Roteiro local

Instale as dependências e execute `go -C backend run ./cmd/m2demo demo`, conforme [roteiro M2](m2-entrega.md). O demo fornece usuário/senha e comando `receive` com material efêmero de simulação. O agente já possui membership no setor padrão e caixa. Os serviços de setores, contatos e preferências estão conectados ao mesmo runtime; não é necessário acessar provedor externo. Ao encerrar, o banco descartável é removido.

RTL usa DOM simulado. Os testes Go exercitam HTTP/PG e upgrade WebSocket reais em loopback. Esta sessão não comprova interação em navegador real, Playwright nem permissões nativas do sistema operacional.

## Backlog M3 e limites

Permanecem transferências, capacidade, abas, busca completa, notas de conversa, tags, prioridade, marcadores de leitura e colisão. CPF/CNPJ autorizado com cifra e busca exata por HMAC e Playwright do fluxo M3 completo também permanecem pendentes. ERP real, Meta real, Evolution real e os demais marcos M3–M8 continuam no backlog autorizado. O checklist acima é exclusivo do recorte M3A; a matriz e o aceite integral de M3 continuam abertos.

## Checks da tentativa anterior

| Comando | Resultado final | Execuções |
| --- | --- | --- |
| `npm ci --prefix backend/tools/pg --no-audit --no-fund` | Passou; bundle PG 18.4 instalado | 1 |
| `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo` | Falhou em `TestVertical`; `TestM3AIntegrated` e pacote `cmd/m2demo` passaram na última rodada | 4 |
| `npm ci --prefix web --no-audit --no-fund` | Passou | 1 |
| `npm --prefix web test -- src/test/m3a-attendance.test.tsx` | Passou; 4 testes | 3 |

Repetições corrigiram fixtures novas: IDs de canal distintos, fila River com papel mínimo, dados de apresentação completos e timeout do cadastro durante compilação concorrente. Nenhum teste existente foi alterado para encobrir falha. Na tentativa anterior, a última rodada Go mostrou somente `TestVertical` falhando com `worker não criou conversa`; `cmd/m2demo` passou em 5,740 s. `git diff --check` passou. Gofmt aplicado aos arquivos Go editados.

## Checks da retomada

Após fortalecer a asserção RTL do cache, uma rodada essencial confirmou:

| Comando | Resultado | Execuções nesta retomada |
| --- | --- | --- |
| `npm ci --prefix backend/tools/pg --no-audit --no-fund` | Passou; bundle PG 18.4 | 1 com PATH configurado |
| `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo` | Falhou somente em `TestVertical`, linha 296; `TestM3AIntegrated` sem falha e `cmd/m2demo` passou (13,713 s) | 1 |
| `npm ci --prefix web --no-audit --no-fund` | Passou; 534 pacotes | 1 com PATH configurado |
| `npm --prefix web test -- src/test/m3a-attendance.test.tsx` | Passou; 4 testes | 1 |

Antes dessas instalações houve uma chamada a cada `npm ci` que não iniciou o npm: `/usr/bin/env: ‘node’: No such file or directory`. As chamadas seguintes configuraram explicitamente o PATH indicado no contrato. A falha Go continua sendo a regressão M2 documentada, e a prova PG/HTTP continua observando HTTP 409 na remoção do vínculo com histórico. Não foi feito contorno por fixture compartilhada, alteração de teste M2, migração ou serviço fora do escopo. `git diff --check` passou na retomada.

As execuções usaram Go 1.27.3, Node 24, vendor, GOTMPDIR na worktree e TMPDIR=/tmp. Não houve skip, deploy, push, merge, commit ou gate amplo. Resultado da implementação: **blocked**, pelas duas condições concretas acima, e não por aguardar revisão independente.

## Revalidação anterior à tentativa 4

Uma única rodada essencial no HEAD recebido `6f3883f` com as alterações locais preservadas confirmou os mesmos impedimentos, sem alteração de implementação ou testes nesta retomada:

| Comando | Resultado | Execuções nesta retomada atual |
| --- | --- | --- |
| `npm ci --prefix backend/tools/pg --no-audit --no-fund` | Passou; 1 pacote instalado | 1 |
| `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo` | Falhou em `TestVertical`, linha 296 (`worker não criou conversa`); pacote acceptance 28,549 s; `cmd/m2demo` passou em 10,100 s | 1 |
| `npm ci --prefix web --no-audit --no-fund` | Passou; 534 pacotes instalados | 1 |
| `npm --prefix web test -- src/test/m3a-attendance.test.tsx` | Passou; 4 testes, 7,79 s | 1 |

`TestM3AIntegrated` não reportou falha, mas caracteriza HTTP 409 ao remover o vínculo com histórico; esse resultado não atende ao critério de revogação por remoção de caixa. A FK em `backend/db/migrations/00009_m3_sectors.sql` referencia o vínculo operacional, e `backend/internal/sectors/service.go` tenta removê-lo por DELETE físico. A correção precisa preservar a identidade histórica da conversa e retirar a autorização operacional. Ambos os arquivos estão fora do escopo autorizado, assim como `backend/internal/acceptance/vertical_test.go`, cuja fixture não configura o setor padrão das caixas novas. Não foi usado helper global, init, alteração de migração em teste ou outro contorno para modificar o comportamento dessa prova existente. O resultado continua **blocked** por esses impedimentos técnicos. Revisão e gate final continuam com o controlador; não houve prova de navegador real.

## Tentativa 4 — implementação concluída para revisão independente

O escopo foi ampliado explicitamente para migração 00012, fixture vertical e política canônica. Os dois impedimentos anteriores foram resolvidos, com provas executáveis de remoção de vínculo populado e preservação M2. A redação do contrato de setores agora reserva 409 à remoção do padrão, sem exigir resolver ou transferir conversas históricas.

| Comando | Resultado final | Execuções nesta tentativa |
| --- | --- | --- |
| `npm ci --prefix backend/tools/pg --no-audit --no-fund` | Passou; 1 pacote, PG 18.4 | 1 |
| `go -C backend test -count=1 ./internal/acceptance ./cmd/m2demo` | Passou; acceptance 28,120 s, demo 15,497 s | 2 |
| `npm ci --prefix web --no-audit --no-fund` | Passou; 534 pacotes | 1 |
| `npm --prefix web test -- src/test/m3a-attendance.test.tsx` | Passou; 4 testes, 5,39 s | 1 |
| `go -C backend test -count=1 ./internal/sectors/... ./internal/platform/db ./internal/attendance/... ./internal/realtime` | Passou; todos os 8 pacotes | 2 |
| `go -C backend test -count=1 -run TestM3AIntegrated -v ./internal/acceptance` | Passou; 4 corridas com evidência de bloqueio PG, 2,719 s | 1 adicional dirigido |
| `git diff --check` | Passou | Verificação final |

A primeira execução Go foi iniciada antes do término do `npm ci` do PG; setores encontrou temporariamente `snowball_create.sql` ausente durante a instalação. O arquivo foi confirmado presente após o npm terminar e a repetição passou. A primeira aceitação revelou um cast ambíguo UUID/text na fixture nova e, após configurar setores, a divergência M2 entre 403 e 404 por caixa errada. O cast foi corrigido e a política autorizada preserva a negação com 404 somente após autorização independente na caixa correta; há provas positivas e negativas por HTTP. Nenhuma assertion M2 foi alterada. As repetições ocorreram por falha ou mudança material, sem nova execução das instalações ou do RTL aprovados.

A prova dirigida adicional aprovou a migração antes do ajuste final de compatibilidade 403/404; a segunda rodada obrigatória aprovou o conjunto final. Os cenários concorrentes registraram `pg_blocking_pids` nas quatro combinações, confirmando espera real em vez de inferir ordenação por tempo. As FKs históricas e FORCE RLS permanecem; a política conserva a exigência do vínculo inclusive para admin/supervisor. Não foi necessário alterar `sectors/service.go` nem nenhum arquivo fora dos caminhos autorizados.

Resultado desta fase: **completed**, pronto para revisão independente. M3 completo, revisão, gate amplo de 13 comandos e prova de navegador real permanecem pendentes. Os registros blocked acima são o histórico das tentativas anteriores. Sem commit, deploy, push, merge, produção ou API paga.
