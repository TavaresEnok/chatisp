# M4A runtime — política no aceite e no despacho

Este contrato é a composição central do marco. As rotas de respostas prontas, política de canal, anexos e horários passam a existir no processo. A geração de OpenAPI, sqlc e TypeScript desta worktree acompanha esses módulos.

## Rotas

O processo registra, mesmo com dependências vazias no documento OpenAPI:

- `/api/panel/canned-replies`
- `/api/panel/inboxes/{inbox_id}/channel-policy`
- `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments`
- `/api/panel/inboxes/{inbox_id}/business-hours`
- `POST /api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/template-messages`

Sem autenticação ou sem serviço, a operação responde 503. O processo não instala `channelpolicy.FakeCatalog`. Sem driver de catálogo configurado, `SyncTemplates` devolve `ErrUnsupported` antes de reservar geração ou alterar `wa_templates`; um catálogo já gravado permanece. O valor zero do fake também é recusado antes de qualquer mutação. Simulação de catálogo só ocorre quando o double é injetado de propósito (`Explicit` ou um snapshot deliberado). `IsFake` verdadeiro não autoriza despacho. Um template só entra na fila quando a linha está `APPROVED` e `sync_source=driver`. Não há chamada Graph para sincronizar ou enviar.

`template-messages` usa a operação idempotente `send_template`. Texto livre continua em `send_text`.

## Storage

Variáveis lidas só no processo, sem valor padrão e sem credencial no código:

- `CHATISP_ATTACHMENTS_S3_ENDPOINT`
- `CHATISP_ATTACHMENTS_S3_REGION`
- `CHATISP_ATTACHMENTS_S3_BUCKET`
- `CHATISP_ATTACHMENTS_S3_ACCESS_KEY`
- `CHATISP_ATTACHMENTS_S3_SECRET_KEY`
- `CHATISP_ATTACHMENTS_S3_SESSION_TOKEN` e `CHATISP_ATTACHMENTS_S3_PATH_STYLE` são opcionais

Todas vazias: as rotas sobem. `attachments.UnavailableStorage` responde `attachments.ErrStorage` (503) na entrada de cada operação do serviço, antes de reservar orçamento ou gravar anexo. Limites positivos do tenant não autorizam a reserva. Configuração parcial recusa a subida e cita só o nome das variáveis ausentes. `NewS3` não usa credencial ambiente. Plano e teto por arquivo saem de `tenants.limits` (`attachment_plan_bytes`, `attachment_reserve_bytes`, `attachment_file_bytes`). Zero ou ausente falha fechado com o mesmo erro de storage.

Este lote não liga mídia ao driver, ao áudio, ao gravador, ao vídeo, ao sticker nem à reação. O módulo entrega reserva, upload e leitura privados.

## Horários

A configuração de horário comercial fica disponível na API. Nenhum worker consulta essa decisão para fila, transferência ou mensagem automática.

## Aceite

Texto público humano passa por `cannedreplies.AcceptPublicHumanTx` uma vez, na aceitação, com o e-mail da sessão. Despacho e retry enviam o corpo já guardado. Nota e bot não chamam esse aceite. A assinatura padrão permanece desligada. Replay de uma chave aceita antes deste aceite — texto ou despedida, sem `attendant_signature_receipts` — valida o payload original e devolve o resultado persistido. A configuração atual da assinatura não entra nesse replay. Quando o recibo já existe, o replay reutiliza o texto aceito. A despedida continua num savepoint: validação ou expansão da assinatura que estoura o limite vira `FarewellErr`, o encerramento local segue e nenhuma tentativa entra na fila. Falha de autorização do encerramento continua erro da operação.

No aceite, a política oficial corre antes do bloqueio de escrita da conversa. A ordem comum ao aceite, à revogação e à cerca final é conector, contato e conversa, cada um num comando próprio. A consulta conjunta não é a primeira aquisição dessas linhas. No despacho, a lease da caixa continua antes do conector, e a posse continua na consulta já ordenada de `memberships`, depois da conversa. A janela e o consentimento são relidos depois dessa posse e antes da rede. A releitura não bloqueia o contato de novo. A transação não atravessa a rede.

Cada aceite novo grava `dispatch_policy_decisions` na fase `enqueue`. O recheck grava outra linha na fase `dispatch`, com `lease_version`. Linha nenhuma é token reutilizável.

## Despacho

`ReserveDispatch` reavalia intenção `queued` ou `retrying` antes de reservar a rede. Janela fechada, consentimento ausente ou template não autorizado gravam a decisão da fase `dispatch` com `lease_version`, cancelam `queued` ou falham `retrying`, e devolvem `ErrPolicyDenied` junto com `ErrForbidden`. A cerca em `internal/attendance/pipeline/dispatch.go` trata `ErrForbidden` como recusa local e volta sem chamar o driver. Não há HTTP remoto nem nova tentativa de rede. Posse revogada e contato bloqueado continuam somente `ErrForbidden`. `uncertain`, `accepted`, `failed` e `cancelled` não são reenviados.

Depois da preparação do material, a mesma cerca revalida janela, consentimento e aprovação do template com a tentativa já em `sending` e com o lease da reserva. A leitura ocorre depois da posse e sem novo bloqueio de contato. A decisão nova da fase `dispatch` leva esse `lease_version`. Se a política nega, a tentativa `sending` vai para `failed` com o código da política, a reserva é consumida e o driver não é chamado. A transação termina antes de qualquer chamada externa. Revogação de consentimento continua sem alterar `sending` ou `uncertain` no gatilho; a cerca é que impede a rede quando a revogação já commitou durante a preparação. Uma revogação que chega depois do aceite da cerca não segura a transação durante a rede.

O idioma da intenção nova tem de ser um token válido e igual a `wa_templates.language` da linha `APPROVED` com `sync_source=driver`. Idioma vazio ou inválido recusa a entrada. Idioma divergente ou catálogo alterado na preparação recusa na cerca, sem chamar o driver e sem marcar `uncertain`. O replay da mesma chave e o conflito de idioma ou finalidade permanecem. `content_kind=template` chama `TemplateSender.SendTemplate` com o id e o idioma gravados. Texto livre continua em `SendText`. O fake identificado implementa o contrato, simula, marca `Simulation` e guarda id e idioma separados do corpo. O driver oficial devolve `template_not_authorized` antes de qualquer request. `Capabilities.Templates` continua falso. Mídia, áudio, gravador, vídeo, sticker e reação continuam desligados.

O hash de `send_template` inclui conversa, conector, id, finalidade e idioma. A mesma chave com finalidade ou idioma diferente conflita. A requisição idêntica continua replay.

## Consentimento

A revogação marca pendências proativas. Antes de alterar `channel_policy_pending`, `channel_policy_lock_outbox` bloqueia as conversas do contato na caixa, depois as mensagens e as tentativas `queued` ou `retrying`, cada grupo em ordem de id. O gatilho `channel_policy_cancel_outbox` cancela só tentativa `queued` e falha `retrying`, atualiza `messages.local_state` e publica `message.attempt` na mesma transação, com o cursor da conversa. `sending` e `uncertain` permanecem e não geram esse evento. Uma proativa nova depois da revogação não entra na fila; a admissão é repetida depois do bloqueio da conversa. Resposta pedida pelo cliente não está na ponte proativa e não é cancelada.

## Mensagem

A migração `00022` acrescenta `content_kind` (`text` por omissão) e `content_context` (`{}`). Inserções antigas e o FIFO, o epoch e a idempotência de texto permanecem. Template grava `content_kind=template` e o contexto semântico. O corpo visível continua sendo o id do template.
