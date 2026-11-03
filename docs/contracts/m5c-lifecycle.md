# M5C — adiamento e inatividade

Entrega independente. Não compõe runtime, OpenAPI, schema TypeScript, `platform/jobs` nem o roteador central. Não edita `distribution`, `presence` nem `attendance`. Não chama Meta, ERP nem Evolution. A composição posterior chama `lifecycle/httpapi.Register` e, num worker ainda não registrado, as funções de job com o relógio da execução.

## Persistência

Migração exclusiva `00026_m5_lifecycle.sql`. RLS ENABLE + FORCE e política `tenant_id = app.tenant_id` da transação, só para `chatisp_app`. Não há DELETE. Não há leitura de `users`.

| Tabela | Chave | Papel |
| --- | --- | --- |
| `conversation_snoozes` | `(tenant_id, id)` | Adiamento. Uma linha `active` por conversa. Guarda `snoozed_until`, motivo e destino (matrícula e setor). Histórico permanece. |
| `inactivity_watches` | `(tenant_id, conversation_id)` | Âncora da resposta humana, geração, aviso e prazo de encerramento. |
| `lifecycle_warning_requests` | `(tenant_id, conversation_id, generation)` | Pedido durável do aviso. `receipt_outcome` é o resultado recebido; `state` é a decisão de armar (`confirmed`) ou não (`uncertain`). |
| `lifecycle_inactivity_settings` | `tenant_id` | Segundos até o aviso e segundos depois do aviso até o encerramento. Versão otimista. |
| `lifecycle_commands` | `(tenant_id, membership_id, operation, request_id)` | Hash SHA-256 e resposta histórica. |

Ausência de configuração significa 900 segundos e 900 segundos, versão 0. O valor gravado fica entre 60 e 604800. O evento de histórico aceita `snoozed`, `snooze_cleared` e o prefixo `snooze_return`, no mesmo formato de origem e destino já usado por transferência.

Adiar não cria estado e não encerra. A conversa continua `open` ou `waiting_customer`. O responsável atual sai de `assignee_id`, então a vaga deixa de ser contada pela capacidade já existente. A matrícula fica só como preferência. A função `assignment_track_queue` passa a não abrir intervalo de fila enquanto existir adiamento `active` daquela conversa. Sem intervalo, `distribution.Pick` e `distribution.Queue` não a escolhem. Ao sair o adiamento, um toque na conversa reabre o intervalo se ela seguir sem responsável.

## Adiamento

```go
func New(database *db.Store) Service
func (Service) Snooze(context.Context, Principal, uuid.UUID, SnoozeCommand) (Snooze, error)
func (Service) Get(context.Context, Principal, inbox, conversation uuid.UUID) (Snooze, error)
func OnCustomerMessage(ctx context.Context, tx pgx.Tx, tenant, conversation uuid.UUID, at time.Time) (Release, error)
func ReturnDue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) ([]Release, error)
```

`Principal` é `sectors.Principal`. `Service.Now`, quando preenchido, é o relógio de `Snooze`. Os jobs usam só o `now` do argumento. `OnCustomerMessage` e `ReturnDue` exigem transação cuja `app.tenant_id` é o tenant informado. Não abrem outra transação por conta própria. `Service.ReturnDue` é a drenagem: abre uma transação nova para cada conversa.

`Snooze` exige controle humano, ciclo aberto, status `open` ou `waiting_customer` e motivo. A normalização do corpo (chave, caixa, versão, motivo e instante não vazio) acontece antes da idempotência. A chave `(tenant, matrícula, operação, request_id)` é serializada com `pg_advisory_xact_lock` antes de qualquer lock da conversa. Em seguida o comando toma o advisory compartilhado do tenant e a conversa em `FOR UPDATE`, e só então `RequireConversation` compartilha essa linha. Dois `request_id` diferentes não compartilham o advisory da chave; o lock exclusivo serializa os dois. Um adiamento é aceito e o outro conflita por versão ou por adiamento já ativo. Nenhum dos dois promove `FOR SHARE` para `FOR UPDATE` e nenhum recebe deadlock `40P01`. A leitura da resposta histórica acontece nessa chave e de novo depois do `FOR UPDATE` da conversa, antes da versão. O instante precisa ser posterior ao relógio só para um comando novo, depois que a chave histórica foi consultada. Repetir a chave e o corpo já aceitos devolve a resposta gravada mesmo que o prazo tenha vencido, sem outro evento. Dois retries concorrentes do mesmo corpo devolvem essa mesma resposta; o segundo não conflita pela versão que o primeiro avançou. Outro corpo, versão diferente ou segundo adiamento ativo conflita. Um retry continua sujeito à autorização atual.

`OnCustomerMessage` é o gancho da composição, na mesma transação da mensagem atual do cliente. Mensagem anterior à criação do adiamento não conta. O adiamento sai, a espera do cliente termina e o status volta a `open` quando estava `waiting_customer`. `ReturnDue` faz o mesmo retorno quando `now` alcança `snoozed_until`, sem mensagem. Nenhum dos dois encerra.

O retorno tenta a matrícula de destino se ela segue ativa no setor atual, com a caixa ainda acessível, `presence.Effective` assignable e `attendance/store.LockAssignmentCapacity` livre. A reserva usa `ApplyAssignment` com o prefixo `snooze_return`, na mesma transação. Sem elegibilidade, ou com a vaga cheia, o responsável permanece vazio e a conversa volta à fila. `distribution.Pick` não é chamado: ele escolheria a cabeça da fila, não a preferência. A capacidade e a presença são as mesmas funções que o round-robin usa.

`ReturnDue` libera no máximo uma conversa por transação: a de menor id entre as vencidas. Lista vazia significa que não resta vencida. Um item com `Cleared` falso significa que essa candidata perdeu a corrida; a chamada seguinte segue para a próxima. O advisory de capacidade, quando a reserva acontece, dura só até o commit dessa conversa. A função não trava a conversa seguinte enquanto esse advisory está retido. Ordenar um lote dentro da mesma transação não resolve a inversão com uma mensagem ou atribuição que já travou a próxima conversa e espera a mesma capacidade.

Ordem de lock, dentro dessa única conversa: advisory compartilhado do tenant, `conversations` em `FOR UPDATE` antes de qualquer `FOR SHARE` dessa linha, linha de adiamento, e só então a matrícula candidata, `presence.Effective` e o advisory de capacidade. A tentativa do destino fica num savepoint. O retorno do adiamento e a inatividade seguem essa ordem e não promovem um share já retido da conversa.

## Inatividade

```go
func (Service) GetInactivity(context.Context, Principal) (InactivitySettings, error)
func (Service) PutInactivity(context.Context, Principal, InactivityCommand) (InactivitySettings, error)
func RunInactivity(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) ([]Effect, error)
func ConfirmWarning(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, receipt WarningReceipt, now time.Time) (Effect, error)
```

Agente, supervisor e admin leem os prazos. Só admin grava, com o advisory exclusivo do tenant antes das linhas. A mesma chave devolve a resposta histórica. Versão divergente conflita.

`RunInactivity` só arma conversa `waiting_customer`, controlador `human`, matrícula ativa, `last_eligible_human_reply_at` preenchido, sem mensagem de cliente posterior a essa âncora e sem adiamento ativo. Nota interna, bot e menu não armam. O aviso automático não reinicia a âncora.

Não há callback de entrega. Quando o prazo do aviso vence, `RunInactivity` grava `lifecycle_warning_requests` em `pending` para aquela geração, com o texto público fixo, e marca o relógio como `requested`. A mesma geração não pede de novo. `pending` não arma prazo de encerramento e não encerra. A composição entrega essa linha pela outbox do canal e depois chama `ConfirmWarning` na transação em que a entrega foi confirmada ou falhou. Este módulo não chama Meta, ERP nem Evolution.

`WarningReceipt.Outcome` é `confirmed` ou `uncertain`. `confirmed` só arma `warning_state=sent` e o prazo de encerramento quando, na mesma transação, a geração ainda é a do marcador, o estado segue `waiting_customer`, a posse é a mesma matrícula ativa, não há adiamento ativo e não há mensagem de cliente posterior à âncora. Qualquer outra situação grava `state=uncertain` e não arma o prazo. `receipt_outcome` guarda o resultado recebido, distinto dessa decisão: um recibo `confirmed` que falhou na revalidação fica `receipt_outcome=confirmed` e `state=uncertain`. Repetir o mesmo `receipt_outcome` devolve o efeito já gravado (`confirmed` se armou, `uncertain` se não) e não move o prazo. Resultado diferente para a geração já resolvida conflita. Geração ausente não é encontrada. Uma confirmação de geração antiga não altera a geração nova.

O encerramento só ocorre com pedido `confirmed` da mesma geração, aviso `sent`, prazo vencido e nova leitura, na mesma transação, de estado `waiting_customer`, posse da mesma matrícula, ausência de adiamento ativo e ausência de mensagem de cliente posterior à âncora. O resultado é `no_response`, o motivo `sem resposta` e a síntese `Encerrado sem retorno do cliente.`. Uma geração já encerrada não fecha outro ciclo. Se a mensagem nova, a troca de posse, o adiamento ou a saída de `waiting_customer` vencer o lock, o job cancela o marcador e não encerra (B8-08). Aviso `pending` ou `uncertain` não encerra.

Ordem de lock do job: advisory compartilhado do tenant, configuração em `FOR SHARE`, cada conversa em `FOR UPDATE` na ordem de id e antes de qualquer `FOR SHARE` dessa linha, adiamento, marcador. `ConfirmWarning` segue a mesma ordem para uma conversa e só então trava o pedido da geração.

## API HTTP

Cookie `__Host-session`. Mutações exigem `Origin` exatamente confiável e `X-CSRF-Token`. Tenant e matrícula vêm da sessão. Superadmin e sessão só de cadastro MFA recebem 403.

| Método | Caminho | Corpo | Resposta |
| --- | --- | --- | --- |
| PUT | `/api/panel/conversations/{conversation_id}/snooze` | `SnoozeCommand` | `Snooze` |
| GET | `/api/panel/conversations/{conversation_id}/snooze?inbox_id=` | — | `Snooze` ativo |
| GET | `/api/panel/lifecycle/inactivity` | — | `InactivitySettings` |
| PUT | `/api/panel/lifecycle/inactivity` | `InactivityCommand` | `InactivitySettings` |

Status: 200; 401 sessão ausente, revogada ou expirada; 403 papel, CSRF, Origin, suspensão, matrícula ou usuário inativo; 404 sem adiamento ativo; 409 versão, request_id ou segundo adiamento; 422 comando; 503 indisponibilidade. Erros não expõem SQL nem segredo.

## Provas

PostgreSQL real, relógio injetado, sem sleeps de negócio: adiar libera a vaga e não encerra; a fila e o `Pick` não levam a conversa adiada; mensagem atual devolve ao destino elegível; destino offline ou sem vaga volta à fila; o prazo também devolve sem encerrar; repetir o adiamento depois do prazo devolve a resposta histórica; dois adiamentos concorrentes da mesma chave esperam o advisory e devolvem a mesma resposta; dois adiamentos concorrentes de chaves diferentes esperam o `FOR UPDATE` da conversa, um é aceito e o outro conflita, sem `40P01`; corrida B8-11 não fecha; duas devoluções não seguram o advisory de capacidade enquanto a conversa seguinte está travada; inatividade grava pedido `pending` e não encerra sem confirmação; `ConfirmWarning` `confirmed` arma o prazo e `uncertain` não; confirmação com mensagem nova não marca `sent`; repetir esse mesmo recibo `confirmed` devolve `uncertain`; geração obsoleta faz o mesmo e não arma a geração nova; B8-08 espera o lock da mensagem nova e não encerra; repetir o job não abre outro ciclo; RLS sem escopo e entre tenants; outro setor não adia. O HTTP prova sessão, Origin, CSRF e papéis.

Checks deste bloco: `npm ci --prefix backend/tools/pg --no-audit --no-fund` e `go -C backend test -count=1 ./internal/lifecycle/...`.
