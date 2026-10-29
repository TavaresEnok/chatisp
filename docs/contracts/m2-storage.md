# Contrato de persistência M2

Este contrato descreve o que a migração `00006_attendance.sql` e o pacote `internal/attendance/store` gravam de fato. Os serviços seguintes consomem esta fronteira. Não há chamada externa, ERP nem Evolution aqui, e o pacote não importa River.

## Transação

`db.Store.WithTenantTx(ctx, tenantID, fn)` abre uma transação no pool `chatisp_app`, recusa outro `current_user` e fixa `app.tenant_id` com `set_config(..., true)`. O callback recebe o `context` já no escopo de `WithTenant` e o mesmo `pgx.Tx`. Um job River futuro entra com `InsertTx` nessa transação. Não chame `WithTenant` de novo dentro do callback e não faça rede com a transação aberta. `tenantID` nulo devolve `db.ErrTenantContextRequired`. O pool privilegiado não é exposto e o tenant não sai de corpo de requisição.

`db.WithIngressTx(ctx, ingressPool, fn)` exige `current_user = chatisp_ingress`, fixa `app.access_scope=ingress` e não define tenant. O pool da aplicação é recusado.

`store.Repo` é sem estado. Cada método exige o tenant do argumento igual a `current_setting('app.tenant_id')` ou, no ingresso, o papel e o escopo acima. Autorização HTTP fica fora deste pacote.

## Ordem de locks

Quem escreve trava nesta ordem e não volta atrás. O cursor é o último. Um snapshot trava o cursor com `FOR SHARE` e não faz `FOR UPDATE` de domínio depois. Isolamento é `READ COMMITTED`. Unicidade concorrente usa savepoint (`store_open`, `store_msg`) porque `23505` aborta o comando. `TransitionAttempt` lê a tentativa sem travar, trava a mensagem e só então trava a tentativa. O cancelamento automático trava as mensagens por `id` e depois as tentativas por `id`. A publicação do cursor vem depois das duas.

1. `tenant_job_slots (tenant_id, queue_name)`
2. `inbox_send_leases (tenant_id, inbox_id)`
3. `protocol_counters (tenant_id, period)`
4. `conversations (tenant_id, id)`
5. `idempotency_records (tenant_id, principal_id, operation, client_request_id)`
6. `messages (tenant_id, id)`
7. `outbound_attempts (tenant_id, id)`
8. `realtime_tenant_cursors (tenant_id)`

## Conversas e ciclos

A linha antiga `(id, tenant_id, summary, created_at)` continua válida. As colunas novas aceitam nulo ou default. O `CHECK` de forma separa placeholder (`inbox_id`, contato, identidade e protocolo nulos, `cycle_number = 0`) de atendimento operacional (todos preenchidos, ciclo ≥ 1). Placeholder não entra no índice de conversa ativa.

Há no máximo uma conversa com `status <> 'closed'` por `(tenant_id, inbox_id, contact_identity_id)`. Protocolo local e período são imutáveis pelo gatilho `conversations_guard_protocol` (`23514`, `protocolo imutável`). O formato gravado é `AAAAMM-` mais seis dígitos, ou o inteiro completo se passar de 999999. O contador é `INSERT ... ON CONFLICT DO UPDATE last_value + 1 RETURNING`. O perdedor da abertura desfaz o savepoint, então o número não é consumido, e relê o vencedor.

`Open` devolve a conversa existente ou a nova (`Created`). `Assume` passa o controle a `human` com o membership informado, sobe `version` e, se o controle não era humano, sobe `controller_epoch`. Nesse caso a mesma transação cancela envios `queued` de origem `bot` ou `menu` da época anterior: grava `outbound_attempts.state` e `messages.local_state` como `cancelled` e publica um `message.attempt` por tentativa. O payload leva conversa, mensagem, tentativa e estado, sem o corpo. Envio `human`, ou que já saiu de `queued`, permanece. A mesma atribuição humana fora de triagem não muda a versão nem cancela nada. Versão divergente devolve `ErrStaleVersion`.

`Close` grava `status=closed`, limpa `waiting_since`, copia o resumo para a conversa e para o ciclo, e preserva `last_customer_message_at`. O ciclo fica com motivo, resultado (`resolved`, `no_response`, `referred_external`, `spam`), síntese, autor, membership, espécie (`human` ou `bot`) e instante. `no_response` é armazenado como tal. Na mesma transação, `Close` cancela os envios `queued` de origem `bot` ou `menu` da época que está encerrando, com o mesmo efeito em `messages.local_state` e o mesmo `message.attempt` por tentativa. O gatilho `conversation_cycles_guard_author` exige que `close_author_id` seja o `user_id` do membership (`23514`). `AttachExternalProtocol` grava uma vez e publica `conversation.external_protocol` na mesma transação. O mesmo valor não gera outro evento. Outro valor é `ErrInvalid`.

A identidade e a caixa precisam ser do mesmo conector, e `contact_id` precisa ser o contato dessa identidade. O gatilho `conversations_guard_binding` recusa o contrário com `amarração incompatível` (`23514`); `Open` devolve `ErrInvalid`. `inboxes` tem a chave `UNIQUE (tenant_id, id, connector_id)`. `meta_phone_numbers` e `webhook_items` referenciam esse par. `messages_guard_binding` recusa mensagem cujo conector não é o da caixa da conversa (`conector não pertence à caixa`). `message_status_guard_binding` exige o mesmo conector quando `message_id` está preenchido. Conversa de outro tenant continua no `23503` da chave composta, porque a linha não é visível para o gatilho.

`NoteAcceptedReply` só move `open` para `waiting_customer` quando `accepted_at` não é anterior à última mensagem do cliente. Fora disso devolve a conversa sem erro. Uma mensagem de cliente mais nova chama `ReceiveCustomerClock`, volta o status a `open`, limpa `waiting_since` e grava `wait_ended` com o controlador e o atendente atuais. Mensagem histórica ou com `trusted_at` anterior não move o relógio e ainda é gravada.

## Mensagens, status e outbox

Identidade externa de mensagem é única por `(tenant_id, connector_id, external_message_id)`. Idempotência é única por `(tenant_id, principal_id, operation, client_request_id)`. O `request_hash` é SHA-256 de quatro campos com prefixo de tamanho: conversa, conector, corpo visível e payload canônico. A mesma tupla devolve a mesma mensagem e a mesma tentativa (`Replayed`), inclusive depois do fechamento. Tupla diferente devolve `ErrIdempotencyConflict`. Conversa fechada sem essa chave devolve `ErrClosed`. Conector diferente do da caixa devolve `ErrInvalid` antes de gravar a chave. A mensagem e a tentativa apontam para `idempotency_id`; a linha de idempotência não é atualizada.

Status é a tabela `message_status_events`, não uma coluna da mensagem. A chave única é `(tenant_id, connector_id, dedup_key)`. `StatusDedupKey` usa `event:` + id externo quando ele existe. Sem id, a chave é `status:` + id da mensagem + espécie + unix nano UTC + categoria + `billable` (`unknown` se vazio). Status de mensagem ainda ausente fica `pending` com `message_id` nulo e é ligado quando a mensagem chega. Cada inserção nova publica `message.status` na mesma transação. Com mensagem conhecida o agregado é a conversa; sem mensagem o agregado é `status` e a versão é 1. Repetição da chave não publica de novo.

Estados de envio: `queued`, `sending`, `accepted`, `failed`, `retrying`, `uncertain`, `cancelled`. O gatilho `outbound_attempts_guard_state` recusa transição ilegal (`ErrInvalid`) e recusa sair de `uncertain` para `sending`, `queued` ou `retrying` com a mensagem `envio incerto não se repete` (`ErrUncertain`). Um recibo tardio ainda pode `accepted` ou `failed`. Reenvio humano é outra linha. `TransitionAttempt` trava a mensagem antes da tentativa, grava o estado nos dois e publica `message.attempt` no mesmo commit. O cancelamento de `Assume` e `Close` faz o mesmo para cada tentativa `queued` de origem `bot` ou `menu`. O payload leva conversa, mensagem, tentativa e estado, sem o corpo.

## Cursor

`AdvanceRealtimeCursor` faz `UPDATE ... SET seq = seq + 1 RETURNING seq` e o evento entra na mesma transação. Publicam assim: `conversation.opened`, `conversation.assumed`, `conversation.closed`, `conversation.waiting`, `conversation.external_protocol`, `conversation.message`, `message.attempt` e `message.status`. `Snapshot` e `ReadEvents` travam o cursor e usam esse `seq` como `NextCursor`, mesmo sem evento novo. `ReadEvents(after)` lê `after < seq <= cursor`. Se `after < retain_after`, devolve `ErrResync` em vez de uma página furada. `Compact` só avança `retain_after` até o `seq` já commitado e apaga eventos com `seq <= retain_after`. O payload do evento leva identificadores, não o corpo da mensagem.

## Coordenação

Filas e tetos gravados na primeira reserva: `ingress` 2, `inbound` 8, `outbound` 4. `AcquireJob` trava o slot, apaga reservas vencidas, conta as vivas e recusa com `ErrCapacity` quando a contagem atinge o teto. O dono é um UUID sem chave estrangeira. `ReleaseJob` trava o mesmo slot e apaga a reserva daquele dono e daquela fila. Sem a linha, `ErrNotFound`. O slot volta a poder ser adquirido na transação seguinte.

`AcquireLease` insere a versão 1 ou assume a linha se o dono é o mesmo ou `expires_at <= now()`. Caso contrário, `ErrLeaseHeld`. `RenewLease` e `ReserveSend` exigem dono, versão e prazo ainda válidos. `ReserveSend` grava `state=held`. `ConsumeSend` passa a `consumed` e `ReleaseSend` passa a `released`. Repetir o mesmo estado não faz nada. Trocar `consumed` por `released`, ou o contrário, é `ErrInvalid`. A chave única continua sendo uma reserva por tentativa.

## Ingresso e segredos

`ingress_deliveries` e `ingress_quarantine` não têm `tenant_id` nem RLS. A unicidade é `(source, app_id, body_sha256)` com `source = meta_cloud`. `StoreDelivery` calcula o hash. `reason` vazio grava `stored`; `unknown_phone` ou `incompatible_binding` grava `quarantined` e uma linha de quarentena. O mesmo corpo devolve o id original (`Replayed`).

`meta_apps`, `meta_wabas` e `meta_phone_numbers` são do tenant, com unicidade global de `app_id`, `waba_id` e `phone_number_id`. O segredo do app e `credential_secrets` guardam só `key_version`, `ciphertext` e `aad`. Entidade de credencial: `channel_connector`.

`PutWebhookItem` insere `webhook_items` se existir telefone ativo com o mesmo tenant, inbox, conector e `phone_number_id`. App e plataforma não leem envelopes. O ingresso não tem `GRANT` em mensagens, usuários, conversas nem `credential_secrets`. Em `webhook_items` o ingresso só seleciona `id`, `tenant_id`, `delivery_id` e `external_key`, e só insere. A aplicação não insere itens. A aplicação não é dona de tabela e não tem `BYPASSRLS`. Políticas de tenant usam `ENABLE` e `FORCE ROW LEVEL SECURITY`.

## Erros do repositório

`ErrTenantContextRequired`, `ErrIngressContextRequired`, `ErrNotFound`, `ErrStaleVersion`, `ErrIdempotencyConflict`, `ErrInvalid`, `ErrClosed`, `ErrLeaseHeld`, `ErrCapacity`, `ErrResync`, `ErrUncertain`.

## Métodos

`Open`, `Assume`, `Close`, `AttachExternalProtocol`, `NoteAcceptedReply`, `AcceptInbound`, `EnqueueText`, `TransitionAttempt`, `OutboundState`, `StatusDedupKey`, `RecordStatus`, `Snapshot`, `ReadEvents`, `Compact`, `AcquireJob`, `ReleaseJob`, `AcquireLease`, `RenewLease`, `ReserveSend`, `ConsumeSend`, `ReleaseSend`, `PutCredential`, `GetCredential`, `RegisterMeta`, `ResolvePhone`, `ReadAppSecret`, `StoreDelivery`, `PutWebhookItem`, `ListWebhookItems`, `MarkDelivery`.
