# M5B — distribuição round-robin

Núcleo de distribuição automática. Não compõe runtime, OpenAPI nem schema TypeScript. Não abre HTTP, não lê a tabela global `users` e não chama Meta, ERP nem Evolution. A presença efetiva vem de `presence.Effective`. A vaga vem de `attendance/store.LockAssignmentCapacity` e o histórico de `attendance/store.ApplyAssignment`, sem alteração desses pacotes.

## Persistência

Migração exclusiva `00025_m5_distribution.sql`. RLS ENABLE + FORCE e política `tenant_id = app.tenant_id` da transação, só para `chatisp_app`:

| Tabela | Chave | Papel |
| --- | --- | --- |
| `distribution_cursors` | `(tenant_id, sector_id)` | Última matrícula que recebeu conversa no setor. Ausência de `last_membership_id` significa que o anel ainda não girou. |
| `distribution_settings` | `tenant_id` | Limiar, em segundos, do alerta de espera normal. Versão otimista. |
| `distribution_sector_auto` | `(tenant_id, sector_id)` | Liga ou desliga a atribuição automática daquele setor. Ausência de linha significa ligada e versão 0. |

`chatisp_app` recebe SELECT, INSERT e UPDATE. Não há DELETE. Ausência de linha em `distribution_settings` significa 900 segundos e versão 0. O máximo gravado é 604800 segundos. A migração também amplia `conversation_events_kind_chk` para o prefixo `distributed`, no mesmo formato de origem e destino já usado por transferência.

## Escolha

```go
func Pick(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (Result, error)
func Queue(ctx context.Context, tx pgx.Tx, tenant, sector, inbox uuid.UUID, now time.Time) (QueueView, error)
func PutWaitAlert(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, expectedVersion int64, seconds int) (Settings, error)
func PutSectorAuto(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID, expectedVersion int64, enabled bool) (SectorAuto, error)
```

As três exigem a transação cuja `app.tenant_id` é o tenant informado. Não abrem outra transação e não autenticam sessão: o chamador humano continua passando por sessão, CSRF, Origin e papel antes; o worker automático entra na transação que já fixou o tenant. Tenant suspenso ou escopo trocado devolve acesso negado.

`Pick` considera a interseção de duas condições: matrícula ativa no setor pedido, com papel admin, supervisor ou agente, e a caixa presente na união das caixas dos setores ativos dessa matrícula. Em seguida chama `presence.Effective`. Só entra no anel quem está `assignable`. Ocupado, offline, matrícula inativa e usuário inativo ficam de fora.

O anel ordena a matrícula por UUID e recomeça depois de `last_membership_id`. A primeira pessoa do anel que ainda tem vaga recebe a conversa. A vaga é a contagem já definida de conversas `open` e `waiting_customer`; o padrão continua 20 quando não há linha em `assignment_capacities`. `LockAssignmentCapacity` e `ApplyAssignment` correm na mesma transação, depois do lock exclusivo da conversa. O cursor só avança para quem foi de fato reservado.

A conversa escolhida é a primeira da fila do setor e da caixa: `urgent` antes de `normal`; dentro da prioridade, `assignment_queue_intervals.started_at` e depois o id. Triagem, encerrada e conversa já atribuída não entram. Sem fila, sem setor ativo, sem vínculo da caixa, com a atribuição automática desligada, sem ninguém assignable ou com todos no limite, `Pick` devolve `Status=queued` e um motivo explícito (`empty`, `sector_inactive`, `inbox_unlinked`, `auto_disabled`, `nobody_available`, `capacity_full`). O conjunto permanece sem responsável e o cursor não anda.

`PutSectorAuto` grava o interruptor do setor com versão otimista: versão 0 insere; versão diferente conflita; setor ausente é comando inválido. Com a automática desligada, `Pick` devolve `auto_disabled` antes de criar cursor ou reservar vaga. O worker consegue percorrer os setores e preservar os desligados. `Queue` continua listando a espera e marca `AutoAssign=false`; a fila segue visível. Setor sem linha permanece ligado.

`Result.Status=assigned` traz a conversa, a matrícula e o usuário. O evento de histórico é `distributed:<setor origem>:<setor destino>:<responsável anterior>:<matrícula nova>`. O ator do evento é o usuário escolhido. O controlador passa a `human`. `waiting_customer` permanece `waiting_customer`.

`Queue` devolve a mesma ordenação e marca `Alert` só na prioridade normal quando a espera é maior ou igual ao limiar. Urgente não gera esse alerta. `PutWaitAlert` grava o limiar com versão otimista: versão 0 insere; versão diferente conflita. Não substitui autorização HTTP.

## Ordem de lock

1. Advisory compartilhado do tenant (`chatisp-tenant`), o mesmo das outras transações operacionais. `PutWaitAlert` e `PutSectorAuto` usam o exclusivo antes de qualquer linha.
2. Advisory do interruptor do setor (`chatisp-distribution-auto`, chave tenant e setor). `Pick` e `Queue` usam o compartilhado; `PutSectorAuto` usa o exclusivo e só então compartilha o setor.
3. Compartilhamento de `distribution_sector_auto`, quando a linha existe. `Queue` em seguida compartilha `distribution_settings`. `Pick` toma o cursor do setor em `FOR UPDATE`.
4. Cada conversa ainda na fila, em ordem de id, com `FOR UPDATE` só na linha de `conversations`. Essa escolha não lê prioridade.
5. Novo comando, já com esses locks, lê `conversation_annotations.priority` e o início da fila e confirma que a cabeça continua sendo a primeira. Uma alteração de prioridade segura `FOR SHARE` na conversa até o commit; `Pick` espera esse lock e só então ordena. Se surgir uma conversa de id menor do que alguma já travada, `Pick` devolve conflito para a transação chamadora repetir, em vez de travar fora de ordem.
6. Compartilhamento do setor, do vínculo da caixa e de uma matrícula candidata, e só então `presence.Effective`.
7. Advisory de capacidade e, na mesma transação, `ApplyAssignment`, que grava estado, evento e cursor de realtime.

As etapas 6 e 7 de cada candidato ficam num savepoint. Quem não está disponível ou não tem vaga sofre rollback desse savepoint, para soltar matrícula e advisory antes do próximo do anel. Os locks das conversas permanecem fora do savepoint e antes do compartilhamento da matrícula, na ordem já usada pela transferência manual.

## Provas

PostgreSQL real, relógio injetado: anel entre dois disponíveis, ocupado e lotado fora da escolha, membro de outro setor excluído, fila urgente/FIFO com alerta no limiar padrão e depois da troca, ninguém disponível, caixa sem vínculo, setor com automática desligada preservado enquanto o setor vizinho recebe conversa, religamento por versão, B8-10 com duas goroutines em setores diferentes e o mesmo atendente na última vaga. Os cursores não se encontram. O vencedor só commita depois que a outra sessão aparece esperando o advisory de capacidade. Uma atribuição é aceita e a outra conversa permanece na fila. Prioridade alterada sob `FOR SHARE` da conversa enquanto `Pick` espera esse lock (promoção da mais nova e rebaixamento da urgente), RLS FORCE sem escopo e entre tenants, tenant suspenso.

Checks deste bloco: `npm ci --prefix backend/tools/pg --no-audit --no-fund` e `go -C backend test -count=1 ./internal/distribution/...`.
