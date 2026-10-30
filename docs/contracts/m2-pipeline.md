# Contrato M2 — pipeline de atendimento e filas River

Este contrato descreve o pacote `backend/internal/attendance/pipeline`. Ele conecta o webhook autenticado e persistido, a expansão multitenant, os workers River (`ingress`, `inbound`, `outbound`) e o despacho/reconciliação de mensagens do conector oficial Meta WhatsApp Cloud API.

Fonte de produto: `docs/especificacao.md` V4.1 B.4 e B.8, e `docs/plano.md` M2. A simulação deste pacote não comprova chamadas externas reais à Meta, à Evolution ou ao ERP.

## Arquitetura e componentes

```
                                      +------------------------+
                                      |   Meta Cloud Webhook   |
                                      +-----------+------------+
                                                  | POST (HMAC) / GET (Challenge)
                                                  v
                                      +------------------------+
                                      | pipeline.WebhookHandler|
                                      +-----------+------------+
                                                  |
                         ResolveApp()             |  StoreDelivery + EnqueueIngress (Atômico)
               +----------------------------------+----------------------------------+
               |                                                                     |
               v                                                                     v
    +----------------------+                                           +----------------------------+
    | meta_app_registry    |                                           | ingress_deliveries         |
    +----------------------+                                           | river.river_job (ingress)  |
                                                                       +--------------+-------------+
                                                                                      |
                                                                                      | river worker (ingress)
                                                                                      v
                                                                       +----------------------------+
                                                                       | pipeline.ingressHandler    |
                                                                       | (ExpandIngress)            |
                                                                       +--------------+-------------+
                                                                                      |
                                 metacloud.Parse() + ResolvePhone()                   |  PutWebhookItem + EnqueueInbound
               +----------------------------------------------------------------------+---------------------+
               |                                                                                            |
               v                                                                                            v
    +----------------------+                                                                  +----------------------------+
    | ingress_quarantine   |                                                                  | webhook_items              |
    | (unknown_phone)      |                                                                  | river.river_job (inbound)  |
    +----------------------+                                                                  +--------------+-------------+
                                                                                                             |
                                                                                                             | river worker (inbound)
                                                                                                             v
                                                                                              +----------------------------+
                                                                                              | pipeline.inboundHandler    |
                                                                                              | (ApplyInbound)             |
                                                                                              +--------------+-------------+
                                                                                                             |
                                                          AcceptInbound / ReconcileReceipt                   |
                                                               +---------------------------------------------+
                                                               |
                                                               v
                                              +----------------------------------+
                                              | conversations, contacts,         |
                                              | contact_channel_identities,      |
                                              | messages, message_status_events  |
                                              +----------------------------------+
```

```
                                              +----------------------------------+
                                              | Outbound Dispatch                |
                                              +----------------+-----------------+
                                                               |
                                                               | river worker (outbound)
                                                               v
                                              +----------------------------------+
                                              | pipeline.outboundLease           |
                                              | (AllowOutbound)                  |
                                              | - Tenant slot limit (PG max 1)   |
                                              | - Inbox send lease checking      |
                                              +----------------+-----------------+
                                                               | allowed = true
                                                               v
                                              +----------------------------------+
                                              | pipeline.outboundHandler         |
                                              | (DispatchOutbound)               |
                                              +----------------+-----------------+
                                                               |
                                      ReserveDispatch()        |
                    +------------------------------------------+
                    | - Locks conversation & lease
                    | - Validates controller epoch & author
                    | - sending state + LeaseVersion
                    v
    +-------------------------------+
    | ResolveMaterial()             |
    | (AccessToken + PhoneNumberID) |
    +---------------+---------------+
                    |
                    | Driver.SendText() (FORA DE TRANSAÇÃO)
                    v
    +-------------------------------+
    | WhatsApp Cloud API / Stand-in |
    +---------------+---------------+
                    |
                    | Resultado remoto
                    v
    +-------------------------------------------------------------------------------------------------+
    | - accepted: RecordAttemptResult(accepted, ProviderMessageID) + ConsumeReservation               |
    | - token_invalid (190): ReportInvalidCredential (degraded + alert) + RecordAttemptResult(failed) |
    | - failed: RecordAttemptResult(failed, RejectionCode) + ConsumeReservation                       |
    | - not_dispatched: RecordAttemptResult(retrying) + ReleaseReservation (River retenta com backoff)|
    | - timeout / resposta perdida: RecordAttemptResult(uncertain) + ConsumeReservation (NUNCA RETENTA)|
    +-------------------------------------------------------------------------------------------------+
```

## 1. Webhook HTTP e Persistência Atômica

`NewWebhookHandler(cfg WebhookConfig)` constrói o handler HTTP para receber webhooks da Meta.
- **Resolução de App confiável antes de HMAC**: O identificador do app (`app_id`) é resolvido via `AppResolver.ResolveApp(ctx, appID)` no escopo de ingresso (`db.WithIngressTx`). Falha de resolução ou app inativo rejeita a requisição antes de qualquer verificação criptográfica (HTTP 401/500).
- **Token GET distinto**: A validação do desafio de subscrição (`hub.challenge`) exige um token de verificação específico (`VerifyTokens.VerifyToken(ctx, appID)`) que obrigatoriamente difere do App Secret do aplicativo (`verifyToken != AppSecret`).
- **Persistência atômica**: Ao receber um POST assinado com HMAC válido (`X-Hub-Signature-256`), o callback `Persist` grava o envelope bruto em `ingress_deliveries` e enfileira o job River de ingresso (`chatisp.ingress.expand.v1`) na mesma transação `pgx.Tx` com escopo `chatisp_ingress`.
- **Condição de ACK 200**: O código HTTP 200 só é emitido se o commit da transação for bem-sucedido e o contexto da requisição HTTP permanecer ativo (`ctx.Err() == nil`). Falha de gravação ou cancelamento do contexto responde com erro (HTTP 500 ou 400) sem confirmar aceitação à Meta.
- **Replay de envelope**: Se o mesmo hash de corpo já estiver gravado (`StoreDelivery` com `Replayed: true`), o job não é reenfileirado e o ACK 200 é emitido imediatamente.

## 2. Expansão Multitenant (`ExpandIngress`)

O worker `ExpandIngress(ctx, deliveryID)` consome jobs da fila `ingress`:
- Executa com pool `chatisp_ingress` e `db.WithIngressTx`.
- Lê o envelope de `ingress_deliveries` e decodifica cada item independente com `metacloud.Parse(appID, body, now)`.
- **Roteamento estrito app-WABA-número**: resolve o número (`PhoneNumberID`) no registro integrado via `ResolvePhone(ctx, tx, appID, phoneID)` ou via `RouteResolver`. A verificação exige correspondência simultânea do App autenticado, do WABA e do PhoneNumberID ativo em todos os caminhos de resolução. Quando um `RouteResolver` (como `configuration.Server`) é informado, a validação de `waba_id` é aplicada adicionalmente contra o registro persistido no escopo de ingresso para impedir que itens com WABA incompatível sejam aceitos indevidamente.
- **Distinção entre erros de domínio e falhas operacionais**:
  - Somente ausência ou incompatibilidade comprovadas de domínio geram quarentena (`unknown_phone` ou `incompatible_binding`).
  - Falhas operacionais temporárias (como resolvedor indisponível `ErrUnavailable`, falha de conexão com o banco ou timeout) abortam `ExpandIngress` imediatamente com rollback da transação, sem gravar em `ingress_quarantine` e sem marcar o envelope como `routed` ou `quarantined`, permitindo que o River retente a entrega após a recuperação do serviço.
- **Sem vazamento de envelope**: Nenhum tenant lê `ingress_deliveries` completo. Cada item decodificado gera um registro independente em `webhook_items` com `payload` contendo apenas o objeto daquele item específico.
- **Quarentena e alertas operacionais**:
  - Números desconhecidos ou incompatibilidades de amarração (ex.: WABA pertencente a outro app ou número órfão) são gravados em `ingress_quarantine` com `reason = 'unknown_phone'` ou `'incompatible_binding'`, emitindo alerta operacional (`slog.Warn`), mantendo os itens isolados e sem expor dados a nenhum tenant.
  - Itens com número conhecido porém marcados como quarentena (ex.: JSON inválido ou motivo operacional) são gravados em `webhook_items` com `state = 'quarantined'`, `kind = 'other'` e payload preservando a razão e detalhes inspecionáveis, sem enfileirar jobs na fila `inbound`.
- **App compartilhado**: o registro global autentica o App ID; cada tenant mantém seu vínculo próprio com WABA e número. O resolvedor integrado identifica a rota por App ID e número, e a expansão confere o WABA do item antes de atribuí-lo ao tenant.
- **Chave de evento limitada (`stableEventKey`)**: `PutWebhookItem` grava com chave única `(delivery_id, external_key)` de no máximo 256 bytes. Chaves que excedam o limite ou geradas a partir de JSON longo de status são convertidas para representação estável com prefixo semântico e hash SHA-256 (`sha256:<hex>`), evitando falhas de restrição de tamanho no banco e repetição de falhas em lote.
- **Idempotência**: Se o item for replayed, o job `inbound` não é reenfileirado ("replay não duplica efeito").
- **Conclusão**: O envelope é marcado como `routed` ao final do processamento.

## 3. Worker Inbound e Reconciliação (`ApplyInbound`)

O worker `ApplyInbound(ctx, tenantID, itemID)` consome jobs da fila `inbound`:
- Executa na transação do tenant (`db.Store.WithTenantTx`).
- Controle de concorrência por tenant: adquire slot de worker em `tenant_job_reservations` para a fila `inbound` conforme o teto configurado em `tenant_job_slots` (compatibilizado com a capacidade dos workers River para até 2 workers por tenant). Se a capacidade for atingida, adia com `river.JobSnooze` sem consumir tentativas.
- Lê o item em `webhook_items`. Se já estiver `processed` ou `quarantined`, retorna imediatamente sem duplicar efeito.
- **Mensagem (`kind = 'message'`)**:
  - Resolução de contato: consulta `contact_channel_identities (tenant_id, connector_id, external_id)`. Se inexistente, cria o contato em `contacts` e a amarração em `contact_channel_identities`.
  - Processamento: chama `store.Repo.AcceptInbound`, que abre ou reutiliza a conversa ativa, gera ou mantém o protocolo único, deduplica pelo id externo da mensagem (`external_message_id`), vincula qualquer status antecipado pendente (`LinkPendingStatus`), atualiza o relógio do cliente (`ReceiveCustomerClock`) e emite o evento de tempo real.
- **Status (`kind = 'status'`)**:
  - Reconciliação por `CallbackData`: se o status trouxer o identificador oficial da tentativa no `CallbackData` (enviado via `biz_opaque_callback_data`), e houver uma tentativa em estado `uncertain` decorrente de resposta perdida ou timeout, o status reconcilia a tentativa incerta para `accepted` ou `failed` por observação oficial, sem depender de horário nem reenvio.
  - Chama `service.Service.ReconcileReceipt` / `RecordStatus`.
  - Se a mensagem referenciada ainda não existe na conversa: o status é armazenado com `pending = true` e `message_id = NULL`.
  - Quando a mensagem chegar posteriormente, `AcceptInbound` concilia automaticamente o status pelo `external_message_id`.
  - Se a mensagem já existe: o status é vinculado e atualiza os recibos de entrega/leitura sem retroceder o relógio.
- Marca `webhook_items.state = 'processed'`.

## 4. Coordenação de Envio (`AllowOutbound`)

O coordenador `outboundLease` implementa `jobs.OutboundLease`:
- Limite de concorrência por tenant: valida se o número de workers ativos em `tenant_job_reservations` para a fila `outbound` atingiu o teto configurado em `tenant_job_slots` (compatibilizado com a capacidade dos workers River para 1 slot por tenant, garantindo que um tenant não capture todos os workers disponíveis).
- Posse de envio por caixa (`inbox_send_leases`): verifica se há uma posse ativa com versão válida e expiração no futuro pertencente a outro worker. Tentativas em estado `sending` são liberadas para processamento para permitir crash recovery mesmo durante a vigência do lease anterior.
- Se a capacidade de slots estiver esgotada ou a posse ocupada: retorna `allowed = false`, fazendo o River adiar o job com `JobSnooze` sem consumir tentativa de retentativa, garantindo proteção contra vizinho ruidoso e progresso de outros tenants.

## 5. Despacho Outbound (`DispatchOutbound`)

O worker `DispatchOutbound(ctx, tenantID, attemptID)` consome jobs da fila `outbound`:
1. Reserva slot em `tenant_job_reservations`.
2. Reserva o envio chamando `service.ReserveDispatch`:
   - Bloqueia a conversa e a posse do inbox.
   - Revalida o epoch do controlador da conversa e a permissão do autor da mensagem.
   - Se o epoch for obsoleto ou o autor tiver permissão revogada: `ReserveDispatch` cancela o envio em transação commitada e o worker conclui o job sem reenviar.
   - Move o estado para `sending` e registra a versão do lease.
3. **Fronteira de despacho e chamada fora de transação**:
   - Abre as credenciais e o material de envio (`ResolveMaterial`) e o driver (`DriverFor`).
   - Se o canal estiver degradado ou a versão da configuração obsoleta, marca a tentativa como `failed` e encerra.
   - **Revalidação de fencing e renovação atômica na fronteira**: Imediatamente antes de qualquer disparo de rede, revalida e renova atomicamente em `inbox_send_leases` a versão do lease e a posse do worker (`owner_id = workerID AND lease_version = version AND expires_at > clock_timestamp()` após as esperas pelos locks), atualizando também a expiração em `send_reservations` e `tenant_job_reservations`. Se a posse expirou ou foi adquirida por outro worker durante a resolução do material, a revalidação falha com `store.ErrLeaseHeld` e o worker vencido **nunca inicia a chamada remota**.
   - Na mesma transação de fencing, a fronteira relê estado do tenant, canal e versão, autorização do autor e epoch da conversa; uma tentativa invalidada é marcada `failed` e não inicia rede. A despedida mantém a autorização vinculada à conversa e ao epoch.
   - **Contexto vinculado ao prazo efetivo**: O contexto de chamada remota (`callCtx`) é estritamente limitado ao prazo restante da reserva (`min(time.Until(expiresAt), 30s)`), garantindo que a chamada não permaneça em voo além da ocupação reservada.
   - Preenche `TextRequest.CallbackData` com o UUID oficial da tentativa (`attemptID.String()`), permitindo posterior correlação de recibos pela Meta.
   - Executa `driver.SendText(callCtx, textReq)` estritamente fora de qualquer transação de banco de dados.
4. **Reconciliação e estados com persistência durável**:
   - Todas as gravações de resultado (`RecordAttemptResult`, `ConsumeReservation`, `ReleaseReservation`, `ReportInvalidCredential`) verificam erros duráveis de banco de dados; falha de persistência propaga erro ao River para retentativa segura sem reenviar a chamada externa já disparada.
   - **Aceito**: HTTP 200 com id remoto move o estado para `accepted` (`RecordAttemptResult`) e consome a reserva (`ConsumeReservation`).
   - **Token inválido (190)**: Chama `ReportInvalidCredential`, degradando o conector de canal e gerando alerta de auditoria em `audit_logs` sem vazar o token. Marca a tentativa como `failed` e consome a reserva.
   - **Retentativa (apenas não-despachado)**: Somente evidência documentada de que a chamada remota não iniciou (`NotDispatched = true`, como cancelamento prévio ou erro de DNS/conexão recusada antes do envio dos bytes) autoriza o estado `retrying`. A reserva é liberada (`ReleaseReservation`) e o erro é repassado ao River para retentativa com backoff exponencial e jitter.
   - **Incerto (queda pós-fronteira ou resposta perdida)**: Timeout, HTTP 429, HTTP 5xx ou queda de rede após o envio dos bytes movem o estado para `uncertain` com `processo_interrompido` ou `resultado_incerto`. O job é consumido e NUNCA reenviado automaticamente.
   - **Crash recovery**: Se o processo reiniciar enquanto uma tentativa estiver em `sending`, `RecoverCrashedDispatch` transiciona a tentativa para `uncertain`. O gatilho `outbound_attempts_guard_state` no PostgreSQL impede qualquer transição de saída de `uncertain` para `sending`, `queued` ou `retrying` ("envio incerto não se repete").

## 6. Fábricas e Modo Simulação

- `NewDriverFactory(cfg DriverFactoryConfig)` aceita drivers simulados ou fakes explícitos (`whatsapp.Fake`) estritamente quando `Simulation == true`.
- Em modo oficial (`Simulation == false`), qualquer tentativa de registrar ou injetar um fake explícito é recusada com `ErrSimulationOnly`.
- `NewWorkers(cfg WorkersConfig)` expõe a fábrica de handlers River (`jobs.Handlers`) para inicialização na aplicação.

A linearização da suspensão ocorre pelo lock transacional `chatisp-tenant` compartilhado com `SetTenantStatus` e a gestão de limites/acesso.
A fronteira de despacho segue uma ordem estrita e consistente de locks para compatibilizar com a aquisição inicial de `ReserveDispatch` e eliminar quaisquer ciclos de deadlock com recuperação concorrente (`RecoverCrashedDispatch`), limites e suspensão:
1. Lock transacional de tenant (`pg_advisory_xact_lock` no namespace `chatisp-tenant`);
2. Posse do lease compartilhado do inbox (`inbox_send_leases FOR UPDATE`);
3. Conversa (`conversations FOR UPDATE`);
4. Mensagem (`messages FOR UPDATE`);
5. Conector de canal (`channel_connectors FOR UPDATE`);
6. Tentativa de outbox (`outbound_attempts FOR UPDATE`);
7. Associação de membro (`memberships FOR UPDATE`, quando aplicável para autor humano);
8. Reserva de envio da tentativa (`send_reservations FOR UPDATE`);
9. Renovação de capacidade do worker (`tenant_job_reservations`).

Se a suspensão administrativa confirmar antes da conclusão da fronteira, a fronteira observa o tenant suspenso sob o gate compartilhado, transiciona duravelmente a tentativa para `failed` com `error_code = 'despacho_invalidado'`, consome a reserva de envio e conclui sem iniciar qualquer chamada de rede ao driver externo. Essa finalização local ocorre de forma segura e durável sob a titularidade da reserva da tentativa em `send_reservations` e seu estado `sending` em `outbound_attempts`, mesmo se o lease compartilhado em `inbox_send_leases` tiver expirado ou sido transferido para outro envio via `ReserveDispatch` concorrente (preservando o envio novo e sua posse intactos) e mesmo se o slot de execução do worker em `tenant_job_reservations` tiver expirado. Por outro lado, para disparar a chamada remota, a posse em `inbox_send_leases`, a reserva em `send_reservations` e a vaga em `tenant_job_reservations` devem estar todas válidas e ser renovadas atomicamente.

Se a fronteira de despacho confirmar primeiro, o lock e a transação são liberados e a chamada remota é disparada fora de transação. A suspensão posterior não desfaz o efeito remoto já despachado ou em trânsito; a tentativa segue as regras existentes de resultado incerto/em voo. Rollback na transação também libera imediatamente o advisory lock. Falhas de preparação e revogações de credenciais são finalizadas sob o mesmo gate e fencing, consumindo a reserva sem efetuar chamada à rede.
