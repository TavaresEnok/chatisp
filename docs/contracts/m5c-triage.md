# M5C — triagem na chegada

Decisão de menu por caixa, separada do envio. Não compõe runtime, OpenAPI nem schema TypeScript. Não altera driver, atendimento, presença nem distribuição. Não chama Meta, ERP nem Evolution. A composição posterior registra `triage/httpapi.Register` e chama `Decide` na transação que já fixou o tenant.

## Persistência

Migração exclusiva `00027_m5_triage.sql`. RLS ENABLE + FORCE e política `tenant_id = app.tenant_id` da transação, só para `chatisp_app`. Não há DELETE. Não há leitura da tabela global `users`.

| Tabela | Chave | Papel |
| --- | --- | --- |
| `triage_menus` | `(tenant_id, inbox_id)` | Menu versionado. `configured=false` é tombstone. `max_attempts` de 1 a 5. `options` é um array JSON. |
| `triage_states` | `(tenant_id, conversation_id)` | Tentativas, boas-vindas já emitidas, fase, versão do menu, `cycle_number` e `controller_epoch` da posse resolvida. |
| `triage_commands` | `(tenant_id, membership_id, request_id)` | Idempotência do CRUD. Hash SHA-256 do comando e resposta gravada. |
| `triage_decisions` | `(tenant_id, conversation_id, input_id)` | Reexecução do mesmo texto sem nova tentativa. |
| `triage_events` | `(tenant_id, id)` | Histórico da decisão. Não usa `conversation_events`. |

`chatisp_app` recebe SELECT, INSERT e UPDATE em menu e estado. Comandos, decisões e eventos recebem SELECT e INSERT.

## Menu

Uma opção leva a um setor ativo vinculado à caixa, com motivo opcional de até 40 caracteres, ou a humano. Humano não tem setor nem motivo. O menu gravado tem de 1 a 12 opções, chaves numéricas únicas de 1 a 4 dígitos, rótulos únicos de até 80 caracteres e ao menos uma opção humana. Chave alfabética é rejeitada no CRUD. Sem a opção humana, o PUT responde 422. O rótulo, comparado em minúsculas, não pode repetir a chave de outra opção nem uma resposta aceita de outra opção, inclusive a linha `1 - Suporte`. O rótulo de setor não pode ser `humano`, `atendente`, `pessoa`, `falar com humano` nem `falar com atendente`. `Decide`, se encontrar um menu legado sem humano, acrescenta a saída "Falar com atendente" com uma chave numérica livre. Na decisão, a opção humana e essas palavras vencem um rótulo ou uma chave de setor.

O texto do menu é numerado (`1 - Suporte`) e usa a mesma chave gravada. A resposta aceita é o texto normalizado igual à chave, igual ao rótulo, ou igual à chave seguida de um separador (` - `, espaço, `-`, `.`, `)` ou `:`) e exatamente esse rótulo. `1 não sei` conta como entrada imprevista. `0 não quero atendente` não é pedido de humano. Isso é o fallback declarativo. Este módulo não envia botão interativo e não chama o driver.

## API

Base: `/api/panel/inboxes/{inbox_id}/triage-menu`.

| Método | Quem | Corpo | Resposta |
| --- | --- | --- | --- |
| GET | admin, supervisor | — | Menu |
| PUT | admin | Command | Menu |
| DELETE | admin | `{request_id, expected_version}` | Menu sem configuração ativa |

Agente e superadmin não acessam. Sessão real, enrollment, CSRF na escrita e Origin exato confiável usam o autenticador existente. Tenant e membership vêm da sessão. O serviço revalida papel, matrícula, suspensão e caixa na mesma transação, por `sectors.RequireInbox`.

Escrita: trava exclusiva do tenant, depois autorização, depois os setores das opções em ordem de UUID com `FOR SHARE`. Leitura: trava compartilhada. Versão 0 cria a primeira linha. Versão divergente responde 409. O mesmo `request_id` e o mesmo corpo devolvem a resposta histórica sem novo audit; outro corpo ou outra operação conflita. Audit `triage_menu.put` e `triage_menu.delete`, alvo inbox, sem texto de cliente.

`Command = {request_id, expected_version >= 0, max_attempts, options[]}`. Ausência de linha: `version=0`, `configured=false`, `max_attempts=3`, `options=[]`. DELETE incrementa a versão e esvazia as opções.

Status: 200, 401, 403, 404, 409, 422, 503. O erro não expõe SQL nem segredo.

## Decide

```go
func Decide(ctx context.Context, tx pgx.Tx, in Input) (Outcome, error)
func ReadDistributionQueue(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Snapshot, error)
type Availability struct{}
func (Availability) Inspect(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Snapshot, error)
```

`Input` traz tenant, caixa, conversa, texto, instante, `input_id` e um `Port`. A transação já tem `app.tenant_id` igual ao tenant. `Decide` não autentica sessão: o chamador humano passa por sessão, CSRF, Origin e papel; o ingresso automático entra na transação que já fixou o tenant. `Availability` é a porta operacional. `FixedPort` só existe para teste e não lê presença nem vaga.

Ordem de lock dentro de `Decide`:

1. Advisory compartilhado do tenant (`chatisp-tenant`).
2. `FOR UPDATE` da conversa, a mesma linha usada pelo atendimento, antes de qualquer share de caixa ou setor. A leitura captura `controller`, `status`, `controller_epoch` e `version`. Uma tomada humana ou um fechamento espera esse lock; não confirma no meio da decisão.
3. `businesshours.EvaluateTx`, que repete o advisory e segura caixa, horário e setores de destino até o commit.
4. `FOR SHARE` do menu.
5. `FOR UPDATE` do estado da conversa em `triage_states`.
6. `Port.Inspect`, somente quando já existe setor de destino.

`Port.Inspect` não pode chamar `distribution.Pick` nem pegar o advisory `chatisp-distribution-auto`: o horário já segurou os setores. `Availability.Inspect` compartilha o setor e o vínculo, lê `distribution_sector_auto` sem o advisory desse interruptor e, num savepoint, trava uma matrícula por vez, chama `presence.Effective` e toma o advisory `chatisp-assignment-capacity` já usado por `LockAssignmentCapacity`. O savepoint é desfeito antes do retorno, então a matrícula e a vaga não ficam presas até o commit. Sem matrícula ativa na caixa, ou sem ninguém `assignable`, o motivo é `nobody_available`. Quem está assignable e sem vaga produz `capacity_full`. Há vaga livre produz `ready`. `AutoAssign` espelha o interruptor do setor e não esconde a presença.

`ReadDistributionQueue` adapta `distribution.Queue`, não atribui, não reserva vaga e não avança cursor. Ele segue a ordem da fila e deve rodar numa transação que ainda não segurou setor. A fila sozinha responde `ready`, `sector_inactive` ou `inbox_unlinked`. Não implementa `Inspect` e não devolve `nobody_available`. `capacity_full` não dispara o texto de ninguém disponível.

`Decide` não grava mensagem, tentativa de saída, responsável nem controlador. A conversa fechada, em espera ou com controlador diferente de `menu` devolve fase `skipped`, nenhuma ação, e a época e a versão lidas sob o lock. Estado resolvido vale para o `cycle_number` e o `controller_epoch` gravados em `triage_states`. A reabertura manual mantém a conversa, incrementa os dois e devolve `controller=menu`. Se o par lido sob o lock divergir, `Decide` zera tentativas, fase e `resolved`, preserva `welcome_sent` e decide de novo para essa posse. `triage_decisions` e `triage_events` permanecem. O mesmo `input_id` devolve o resultado gravado, com a época daquela decisão. Dentro da mesma posse, estado já resolvido não repete ações. `Outcome` inclui `controller_epoch` e `version` para o compositor invalidar as ações se a posse mudar antes do envio.

Fases e ações:

| Situação | Fase | Ações |
| --- | --- | --- |
| Primeira chegada com menu, expediente aberto ou horário não configurado, texto que não é humano nem opção válida | `menu` | Boas-vindas, se ainda não emitidas, e menu numerado. Não consome tentativa |
| Expediente aberto, ou horário não configurado, com menu já apresentado e texto imprevisto abaixo do limite | `menu` | Menu numerado com a opção humana |
| Opção de setor ainda vinculada | `routed` | Motivo, se houver; rota; encaminhamento humano; ninguém disponível se o port disser isso |
| Texto de humano, opção humana, ou as palavras humano, atendente, pessoa, falar com humano, falar com atendente | `routed` | Rota ao setor padrão e encaminhamento humano, sem consumir tentativa |
| Imprevisto no limite | `exhausted` | Rota ao setor padrão e encaminhamento humano |
| Sem menu | `routed` | Rota ao destino do horário (setor padrão quando aberto ou sem configuração) e encaminhamento humano |
| Fora do expediente sem plantão autorizado | `outside_hours` | Texto de fora do expediente, rota ao padrão e encaminhamento humano |
| Fora do expediente com plantão autorizado | `on_call` | Texto de fora do expediente, rota, plantão e encaminhamento humano |

Boas-vindas usam o texto da caixa em `businesshours`, uma vez por conversa, trocando só `{protocolo}` pelo protocolo já gravado. Texto vazio não gera ação. Fora do expediente não entra no contador do menu. Toda saída terminal inclui `forward_human`. Sem setor autorizado, o encaminhamento humano segue sem setor, para o compositor não deixar o cliente preso.

`Outcome = {actions, attempts, welcome_sent, phase, controller_epoch, version}`.

## Provas

Limites do menu sem banco: tentativas 0 e 6, menu sem humano, setor ausente, motivo no humano, chave duplicada, rótulo longo ou nulo, 13 opções, rótulo de setor igual à chave humana, rótulo de setor igual a palavra reservada, rótulo humano igual à chave de setor, chaves alfabéticas e rótulo que copia a linha ou a resposta de outra opção (`1 - Suporte`, `1-Suporte`). `Decide` sem transação é inválido. Com o menu já apresentado, `1`, `Suporte` e `1 - Suporte` escolhem o setor; `1 não sei` consome tentativa e, no limite, sai para o humano; `0 não quero atendente` não é pedido humano. A chave `1` não captura `10`. O texto exibido é `1 - Suporte`.

PostgreSQL real: CRUD, replay, versão, setor não vinculado, audit, papéis admin/supervisor/agente, sessão, CSRF, Origin, suspensão, superadmin, RLS sem escopo e entre tenants. Ramos: menu na primeira chegada sem consumir tentativa, saudação com `max_attempts=1` ainda mostra o menu, fallback humano, repetição idempotente, limite, humano direto e depois de uma tentativa, motivo financeiro, opção com setor revogado, boas-vindas uma vez com protocolo distinto por conversa, fora do expediente, plantão, ninguém disponível e capacidade cheia lidos de presença e vaga reais, capacidade cheia sem esse texto, sem menu, conversa fechada e controlador bot. A tomada humana e o fechamento concorrentes esperam o `FOR UPDATE` da conversa; a decisão seguinte vê a época ou a versão nova e não grava ações do controlador antigo. A reabertura existente, depois de um estado resolvido, volta a apresentar o menu e aceita o pedido humano; o `input_id` antigo continua idempotente e os eventos anteriores ficam. Um menu legado com rótulo de setor `0` ou `humano` não desvia a saída humana para esse setor. Nenhuma mensagem, tentativa de saída, cursor de distribuição ou responsável é gravado por `Decide` ou por `Availability`.

Checks deste bloco: `npm ci --prefix backend/tools/pg --no-audit --no-fund` e `go -C backend test -count=1 ./internal/triage/...`.
