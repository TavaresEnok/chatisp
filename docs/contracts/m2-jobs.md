# Contrato M2 — filas River

River open-source `v0.50.0` (`github.com/riverqueue/river`) com driver `github.com/riverqueue/river/riverdriver/riverpgxv5` `v0.50.0`. O driver exige `github.com/jackc/pgx/v5` `v5.11.0`, a mesma versão do módulo. A linha de migração `main` usada é a versão 8. O socket futuro usa `github.com/coder/websocket` `v1.8.15` como dependência direta; os contratos HTTP não mudam.

Não há chamada a Meta, Evolution ou ERP. Não há River Pro: sem DLQ licenciada e sem concorrência Pro.

## Onde a fila vive

As tabelas oficiais ficam no schema `river`, criado e migrado só por `jobs.Migrate` com o pool `chatisp_owner`. Esse papel não é superusuário e não tem `BYPASSRLS`. O pool de migração precisa de pelo menos duas conexões. `chatisp_owner` precisa de `CREATE` no banco para criar o schema; o bootstrap atual não concede isso, então o caminho de migração recebe o grant antes de `Migrate`.

`chatisp_jobs` é criado fora deste pacote, porque o dono não tem `CREATEROLE`. Precisa existir antes da migração, sem superusuário e sem `BYPASSRLS`. `Migrate` não abre worker e pode repetir.

## Quem enfileira e quem consome

| Papel | Uso |
| --- | --- |
| `chatisp_owner` | Migração e dono do schema. Fora do processo de API e de worker. |
| `chatisp_app` | `InsertTx` na transação de domínio. |
| `chatisp_ingress` | `InsertTx` na transação de ingresso. |
| `chatisp_jobs` | Busca, conclusão, retentativa, manutenção e leitura de jobs esgotados. |
| `chatisp_platform` | Sem acesso ao schema. |

O `INSERT` oficial do River devolve a linha e tem `ON CONFLICT DO UPDATE`. Por isso app e ingresso têm `SELECT`, `INSERT` e `UPDATE` em `river.river_job`, mais uso da sequence e execução de `river_job_state_in_bitmask`. Não têm `DELETE` nem acesso a `river_leader`, `river_queue`, `river_notification` ou `river_migration`.

`river.chatisp_job_update_guard` é `BEFORE UPDATE`, com `search_path` fixo em `pg_catalog`. Só `chatisp_jobs` passa. App e ingresso não executam a função, então não conseguem alterar uma linha já gravada. O worker consegue concluir, interromper e reagendar.

## Inserção atômica

`EnqueueIngress`, `EnqueueInbound` e `EnqueueOutbound` chamam `InsertTx` na `pgx.Tx` recebida e não dão commit. Rollback desfaz o job. Commit o publica. Um cliente sem pool insere; outro processo, com o pool `chatisp_jobs`, trabalha o que ficou gravado.

## Filas e argumentos

| Fila | Kind | Prioridade River | Argumentos |
| --- | --- | --- | --- |
| `ingress` | `chatisp.ingress.expand.v1` | 2 | `v`, `delivery_id` |
| `inbound` | `chatisp.inbound.apply.v1` | 1 | `v`, `tenant_id`, `item_id` |
| `outbound` | `chatisp.outbound.dispatch.v1` | 3 | `v`, `tenant_id`, `attempt_id` |

`v` é 1. Metadado gravado: `{"v":1}`. Ingresso não tem tenant. Domínio sem tenant é rejeitado antes do banco. Não há texto, telefone, token ou corpo de mensagem no argumento. Log do cliente omite erro, args, metadata e payload.

Os handlers são interfaces injetadas. Pacote nenhum de atendimento ou ingresso é importado aqui. Handler ausente falha; não há sucesso mudo. Não existe cliente global iniciado por `init`.

## Retentativa, espera e esgotamento

Backoff dobra a partir de 1s, com jitter de ±20% e teto de 2min. `MaxAttempts` é 8. Esgotar deixa `state = discarded` em `river.river_job`, com o erro curto e sem stack na inspeção `Exhausted`. A retenção local é 30 dias. Isso substitui a DLQ paga.

`MaxWorkers` (2, 4 e 2) limita o processo. Não é limite de tenant nem de número. O worker de `outbound` chama `OutboundLease` antes do handler. Recusa gera `JobSnooze` de 5s e não consome tentativa. Sem a reserva, o job falha fechado.

## Shutdown

`Start` só existe com pool de worker. `Stop` respeita o contexto e o soft stop de 5s. Job em execução que observa o cancelamento volta para a fila. Processo novo trabalha o que já tinha commit.

## Prova

`go -C backend test -count=1 ./internal/platform/jobs` usa PostgreSQL 18. Cobre rollback, commit, restart, shutdown, reserva, descarte inspecionável e papéis sem superusuário.
