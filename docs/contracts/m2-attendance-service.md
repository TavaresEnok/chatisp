# Contrato M2 — serviço de atendimento

Pacote `backend/internal/attendance/service`. Ele orquestra o painel e os workers sobre `attendance/store`, o modelo puro `conversation` e `jobs.InsertTx`. Não abre rota HTTP, não escolhe driver remoto e não chama Meta, Evolution, ERP ou qualquer outra rede. A transação não contém I/O de rede.

Fonte de produto: `docs/especificacao.md` V4.1 B.4 e B.8, e `docs/plano.md` M2. A simulação deste pacote não comprova a Cloud API, a Evolution nem o ERP.

Até o M3 não existe membership de setor. Caixa permitida é uma caixa do tenant ativo. Enviar e reservar despacho exigem ainda o conector `meta_cloud` habilitado, `connected`.

## Quem chama

| Operação | Quem | Transação |
| --- | --- | --- |
| `List`, `Snapshot`, `History`, `Events` | painel, com `Principal` | uma `db.WithTenantTx` |
| `ReceiveText`, `Assume`, `Send`, `Close`, `Reopen` | painel, com `Principal` | a mesma transação grava domínio, evento e, no envio, o job |
| `ReserveDispatch`, `RecordAttemptResult`, `RecoverCrashedDispatch` | worker de saída | uma `db.WithTenantTx`; sem SQL próprio do worker |
| `RecordReceipt`, `ReconcileReceipt` | worker de status | igual |
| `ReserveWorker`, `ReleaseWorker`, `ConsumeReservation`, `ReleaseReservation` | worker | repassa o store na transação do tenant |

`New(database, queuer)` recebe `*db.Store` e um `Queuer`. `*jobs.Client` implementa `EnqueueOutbound` com `InsertTx` e não dá commit. `JobID` zero ou erro de fila no envio principal devolve `ErrJobRequired` e desfaz a transação. Ausência de job não vira sucesso.

## Autorização

`Principal` carrega usuário, membership e tenant. `IsSuperadmin` não abre acesso. Sem membership ativa, com usuário diferente ou com papel fora de `admin`, `supervisor` e `agent`, o resultado é `ErrForbidden`. Tenant ausente ou diferente de `active` é `ErrTenantInactive`. Caixa de outro tenant ou inexistente é `ErrInboxForbidden`.

Cada chamada, inclusive o replay da mesma chave, relê membership e tenant. Um replay sem acesso devolve o erro e não apaga a linha já gravada.

## Idempotência

Chave: tenant, membership do principal, operação e `client_request_id`. O hash tem 32 bytes.

| Operação | Constante | Hash canônico |
| --- | --- | --- |
| texto humano ou despedida | `send_text` | o store cobre conversa, conector, corpo e o JSON `v`, `conversation_id`, `connector_id`, `body`, `visibility=public` |
| encerrar | `close_attendance` | conversa, versão esperada, motivo, resultado, síntese, texto e id da despedida |
| reabrir | `reopen_attendance` | conversa e versão esperada |

A mesma intenção devolve a mesma mensagem ou o mesmo ciclo. Outro payload na mesma chave é `store.ErrIdempotencyConflict`. Corrida de insert grava uma mensagem, uma outbox e um job. O perdedor recebe `Replayed` e não insere outro job. O replay de envio não devolve o id River original (`JobID` 0). A versão da conversa só é exigida na primeira tentativa de envio; o replay não a reavalia porque o envio não incrementa versão. Encerrar e reabrir incluem a versão no hash: repetir a mesma chamada casa; mudar a versão com a mesma chave conflita.

O replay não segue o ciclo vivo. A chave de encerrar fica ligada ao ciclo que ela fechou; a de reabrir, ao ciclo que ela abriu. A ligação é outra linha de idempotência, no mesmo principal, com operação `close_attendance_cycle` ou `reopen_attendance_cycle`. O hash de 32 bytes guarda a conversa, o ciclo, a versão e o epoch daquele comando. Não é DTO de painel. Se a conversa já reabriu e encerrou de novo, repetir a primeira chave devolve o fechamento e o ciclo originais, e repetir a primeira reabertura devolve o ciclo que aquela chave criou.

## Comandos do painel

`ReceiveText` deduplica pelo id externo do conector antes de qualquer reabertura. Duas primeiras mensagens concorrentes, com ids diferentes, compartilham uma conversa e um protocolo. `WithinReopenWindow` é decisão de quem chama; o schema não guarda a duração. Se a janela vale e não há outra conversa ativa da identidade, a reabertura mantém o protocolo e abre o ciclo seguinte. Se já existe ativa, a mensagem entra nela. `Historical` grava na conversa mais recente, sem mover relógio e sem abrir ciclo. Sem conversa, `store.ErrNotFound`.

`Assume` entrega a versão corrente ao humano. Epoch só muda quando o controlador deixa de ser humano. O perdedor da versão recebe `store.ErrStaleVersion`. O ator da timeline é o `user_id` da membership.

`Send` exige a conversa aberta, controlador humano e assignee igual à membership. Mensagem, idempotência, outbox, evento e job entram na mesma `pgx.Tx`.

`Close` exige motivo, resultado (`resolved`, `no_response`, `referred_external`, `spam`) e síntese. `no_response` permanece `no_response`. A despedida, se houver, é enfileirada antes do fechamento, com origem humana, dentro do savepoint `svc_farewell`. No mesmo savepoint grava-se `close_farewell`, com o id da tentativa como `client_request_id`, autorizando o despacho daquele goodbye depois que o epoch mudar. Falha de job, canal ou SQL desfaz só a despedida (`FarewellErr`), inclusive essa autorização, e o fechamento segue. O fechamento incrementa o epoch, grava ator e timeline e cancela automáticos `bot`/`menu` ainda `queued` do epoch anterior. Humano que não é essa despedida e tentativa já despachada ficam na outbox, mas o worker só reserva a despedida autorizada quando o epoch da tentativa não é mais o da conversa ou a conversa está fechada. Automático obsoleto continua cancelado.

`Reopen` usa as tabelas e as queries já geradas. Não há método novo no store nem migração. O protocolo não muda. O ciclo incrementa, o controlador volta a `menu`, o assignee sai e o epoch incrementa. Outra conversa ativa da mesma identidade é `ErrActiveConversation`. A unique `conversations_one_active_identity` também mapeia para esse erro, dentro de savepoint, para não abortar a transação.

## Workers

`ReserveDispatch` exige tenant ativo e canal pronto. Canal indisponível devolve `ErrChannelUnavailable` antes do lease, e a tentativa continua `queued`. A ordem de lock é a da migração: lease da caixa, depois a conversa. `LeaseVersion` 0 adquire lease livre ou expirado. Versão positiva só renova o mesmo dono.

Antes de gravar a reserva, a origem humana relê `messages.author_membership_id` e trava essa membership. O papel vigente precisa ser `admin`, `supervisor` ou `agent`, com status `active`. Sem esse ingresso — inclusive a despedida já autorizada por `close_farewell` — a tentativa `queued` vai a `cancelled` e a `retrying` vai a `failed`, com código `permissao_revogada`. O commit acontece e o retorno é `ErrForbidden`. A revogação não muda `controller_epoch`. `sending` e `uncertain` não são reclassificados. `bot` e `menu` não têm autor humano e continuam só sob a cerca de epoch.

Se o autor segue vigente, a transação grava `owner_id`, `lease_version` e `expires_at` na reserva e passa a tentativa a `sending`.

A despedida com linha `close_farewell` cujo hash casa com a conversa e o epoch da tentativa segue para `sending`, mesmo com a conversa fechada ou o epoch da conversa já avançado. Isso não vale para `bot`, `menu` nem para texto humano sem essa linha.

Fora essa despedida, se o epoch da tentativa não é o da conversa, ou a conversa está fechada:

| Estado | Efeito confirmado | Retorno |
| --- | --- | --- |
| `queued` | `cancelled` | `ErrStaleEpoch` |
| `retrying` | `failed`, código `epoch_obsoleta` | `ErrStaleEpoch` |
| `sending` | permanece em voo | `ErrStaleEpoch` |
| `uncertain` | nada | `store.ErrUncertain` |

O cancelamento ou a falha obsoleta dá commit. O erro volta depois. Outro dono com lease vivo recebe `store.ErrLeaseHeld`.

Uma tentativa `retrying` já tem reserva. O insert único cai no savepoint `svc_reserve` e a mesma linha passa a ter o `owner_id`, a `lease_version` e o `expires_at` do lease da caixa recém-tomado, com estado `held`. Não nasce segunda reserva.

`RecordAttemptResult` grava `accepted`, `failed`, `retrying` ou `uncertain` na mesma tentativa. Sem `Observation`, é comando do worker que reservou. `LeaseVersion` menor que 1 ou `OwnerID` vazio é `store.ErrInvalid`. A transação trava a mensagem, a tentativa e a reserva e exige o mesmo dono e a mesma `lease_version` persistidos. Divergência é `store.ErrLeaseHeld` e não grava estado nem substitui a lease. Um resultado atrasado do lease 1 não altera um `sending` do lease 2. `accepted` exige evidência documentada, instante e id externo. Quando o id externo é gravado, a mesma transação chama `LinkPendingStatus` e aplica o efeito elegível de cada aceite que estava pendente para esse id. A espera do cliente só começa ou avança se a mensagem é humana, pública, do epoch corrente, a conversa está aberta (ou já aguardando cliente) e o cliente não escreveu depois dessa aceitação. Duas respostas públicas humanas elegíveis aceitas sucessivamente no mesmo ciclo/epoch avançam `waiting_since` e `last_eligible_human_reply_at` até a aceitação mais recente, incrementam a versão, publicam o evento em tempo real de conversa e preservam um único evento de timeline `wait_started`. Aceitação repetida ou fora de ordem devolve `IgnoredLate` e não retrocede relógios nem duplica efeitos. Epoch ou ciclo antigo devolve `IgnoredLate` e não altera `waiting_since` nem `last_eligible_human_reply_at`. `store.ErrClosed` nessa nota não desfaz a aceitação da outbox. `store.ErrStaleVersion` desfaz, para o worker repetir.

`Observation` verdadeira é recibo remoto, não comando do worker. Exige lease 0, owner vazio e destino `accepted` ou `failed`. Não grava `lease_version`. `RecordReceipt` também é observação: não usa a cerca e não troca o estado da tentativa.

`RecoverCrashedDispatch` leva `sending` a `uncertain` com código `processo_interrompido` só se a cerca esperada ainda é a persistida. Cerca ausente é `store.ErrInvalid`. Cerca antiga é `store.ErrLeaseHeld` e o envio vigente continua. O schema não permite `sending` → `queued`. Queda não reenvia. Um recibo posterior ainda pode aceitar ou falhar a mesma tentativa.

`RecordReceipt` e `ReconcileReceipt` são o mesmo caminho. Os dois chamam `LinkPendingStatus` antes de reler a `dedup_key`. Status sem mensagem fica `pending`. Quando a mensagem já tem o id externo, o vínculo tira `pending` e a mesma chave, repetida, devolve essa linha ligada, sem criar outra. Aceite ligado e elegível dispara a espera; respostas sucessivas aceitas avançam o relógio de espera sem duplicar a timeline de início de espera; repetir a reconciliação não move relógio para trás nem abre outra espera. Entrega, leitura e falha só deixam de estar pendentes. Aceite de epoch antiga não move relógio.

`ReserveWorker` e `ReleaseWorker` são as vagas do tenant. `ConsumeReservation` e `ReleaseReservation` encerram a cerca de envio. Nada disso abre rede.

## Erros tipados

Os erros do store continuam comparáveis com `errors.Is`: `ErrNotFound`, `ErrStaleVersion`, `ErrIdempotencyConflict`, `ErrInvalid`, `ErrClosed`, `ErrLeaseHeld`, `ErrCapacity`, `ErrResync`, `ErrUncertain`.

Este pacote acrescenta:

| Erro | Quando |
| --- | --- |
| `ErrForbidden` | sem ingresso operacional vigente, inclusive superadmin sem membership; no despacho, o cancelamento ou a falha por `permissao_revogada` já teve commit |
| `ErrTenantInactive` | tenant suspenso ou ausente |
| `ErrInboxForbidden` | caixa fora do tenant |
| `ErrChannelUnavailable` | conector não está pronto para despacho |
| `ErrJobRequired` | envio principal sem job durável |
| `ErrStaleEpoch` | cerca de epoch; o cancelamento de `queued` já teve commit |
| `ErrActiveConversation` | reabertura com outra conversa ativa da identidade |

Não há DTO de painel. `History` e `TimelineEvent` são fatos persistidos, não contrato HTTP.

## Prova

`go -C backend test -count=1 ./internal/attendance/service` usa PostgreSQL embutido. Cobre autorização no replay, corrida e conflito de idempotência, rollback quando o job falha, duas primeiras mensagens, assumir concorrente, despedida que falha sem desfazer o fechamento, reserva da despedida depois do fechamento e da reabertura, revogação da membership entre o envio e a reserva (texto e despedida), reabertura com o mesmo protocolo, replay do ciclo original, aceitação atrasada, status de saída anterior ao id externo com reconciliação repetida, cerca de epoch com estado incerto e cerca de lease que não deixa resultado antigo substituir a reserva nova. Não prova Meta, Evolution nem ERP.
