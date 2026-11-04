# M5 — gancho de bot externo

Entrega isolada. O bot é um fake em processo: não há cliente HTTP, nem Meta, ERP, Evolution ou endpoint externo. A posse continua em `conversations.controller` e `controller_epoch`. O pacote não cria outro controlador.

A fila humana, neste esquema, é `controller=menu`, `assignee_id` nulo e `status=open`. O banco exige responsável quando o controlador é `human`. Desligar o gancho, o handoff, o prazo e a recuperação incerta sobem a epoch, cancelam envios `queued` de origem `bot` ou `menu` da posse antiga e devolvem a conversa a essa fila. A tomada humana já existente continua sendo quem grava `controller=human` com matrícula.

## Persistência

Migração exclusiva `00032_m5_bot_hook.sql`. RLS ENABLE + FORCE e política `tenant_id = app.tenant_id`, só para `chatisp_app`. Não há DELETE. O token em claro não é coluna.

| Tabela | Chave | Papel |
| --- | --- | --- |
| `bot_hooks` | `(tenant_id, inbox_id)` | Um gancho por caixa. Hash do segredo, prefixo, prazo, interruptor, geração e `simulation=in-process`. |
| `bot_sessions` | `(tenant_id, id)` | Posse concedida, prazo, recibo, handoff e aviso. Uma sessão `pending` ou `received` por conversa. |
| `bot_hook_commands` | `(tenant_id, scope_id, operation, request_id)` | Hash e resposta histórica, sem o segredo. |

O token entregue uma vez tem a forma `v1.<tenant>.<segredo>`. Só o segredo entra no hash. Repetir a mesma instalação ou rotação devolve o prefixo e `token_revealed=false`.

## Comportamento

```go
func New(database *db.Store) Service
func (Service) Install(context.Context, Principal, uuid.UUID, InstallCommand) (Installation, error)
func (Service) Rotate(context.Context, Principal, uuid.UUID, RotateCommand) (Installation, error)
func (Service) SetSwitch(context.Context, Principal, uuid.UUID, SwitchCommand) (SwitchResult, error)
func (Service) Notices(context.Context, Principal, uuid.UUID) (NoticeList, error)
func (Service) Offer(context.Context, tenant, conversation uuid.UUID) (Session, error)
func (Service) Reply(context.Context, token string, conversation uuid.UUID, ReplyCommand) (ReplyResult, error)
func (Service) Handoff(context.Context, token string, conversation uuid.UUID, HandoffCommand) (HandoffResult, error)
func (Service) ExpireDue(context.Context, tenant uuid.UUID, now time.Time) (int, error)
```

`Offer` grava a posse e só então chama `Fake.Handle`. O fake padrão confirma o recebimento e não resolve a conversa. `received` é o análogo do HTTP 200: o gancho recebeu o evento. A sessão segue aberta até handoff, prazo, interruptor ou resultado incerto. Uma segunda oferta da mesma posse não chama o fake de novo.

`Reply` só enfileira texto público se o hash do token confere, o interruptor está ligado, o controlador é `bot` e a epoch é a concedida. Epoch antiga, inclusive depois de bot → fila → bot, responde conflito. A mesma chave e o mesmo corpo devolvem a mesma mensagem. Outro corpo conflita. A resposta estende o prazo; não encerra a sessão.

`Handoff` exige motivo e setor ativo ligado à caixa. A transferência é atômica. Setor sem atendente não impede a fila. Setor não autorizado não move a conversa. A retentativa da mesma chave devolve o mesmo `handoff_id`.

`SetSwitch` desligado incrementa a geração, sobe a epoch das conversas do bot na caixa, cancela o que ainda está `queued` e grava o aviso do interruptor. Ligar de novo não toma conversa sozinho.

`ExpireDue` usa o `now` do argumento. Recibo `received` não impede a devolução quando o prazo vence.

Resultado incerto, erro do fake ou handoff sem setor válido grava `state=uncertain` e o texto `Resultado do gancho incerto. A conversa voltou para a fila humana.` A consulta de avisos não remove essa linha. Gancho que não recebe o evento devolve a conversa com `unavailable`. Conversa já humana não é tomada.

A ordem de lock entre ofertas e o interruptor é o advisory `bothook-tenant`: compartilhado na oferta e na resposta, exclusivo no interruptor e no prazo. A conversa é travada com `FOR UPDATE` antes do gancho.

## API

O registro é `bothook/httpapi.Mount` no roteador chi, fora do documento OpenAPI. Cookie `__Host-session` no painel. Mutação do painel exige `Origin` confiável e `X-CSRF-Token`. A API do bot usa `Authorization: Bearer`.

| Método | Caminho | Quem |
| --- | --- | --- |
| PUT | `/api/panel/bot-hook/inboxes/{inbox_id}` | Admin instala |
| POST | `/api/panel/bot-hook/inboxes/{inbox_id}/token` | Admin gira o token |
| PUT | `/api/panel/bot-hook/inboxes/{inbox_id}/switch` | Admin liga ou desliga |
| GET | `/api/panel/bot-hook/inboxes/{inbox_id}` | Admin, supervisor ou agente com a caixa |
| GET | `/api/panel/bot-hook/notices?inbox_id=` | Idem, inclui o aviso incerto |
| POST | `/api/bot-hook/conversations/{conversation_id}/reply` | Token da caixa |
| POST | `/api/bot-hook/conversations/{conversation_id}/handoff` | Token da caixa |

Status: 200; 401 sessão ou token ausente; 403 papel, CSRF, Origin ou caixa; 404 gancho ausente; 409 epoch, interruptor ou comando; 422 corpo; 503 indisponibilidade. O corpo de erro não traz SQL nem segredo.

## Painel

`BotHookNoticeList` lista a pendência da automação. Estado `uncertain` fica em `role=alert` com o texto do aviso. O estado não é omitido nem trocado por sucesso.

## Fora deste recorte

Histórico limitado para o bot, aviso de assistente virtual, aprovação de ação sensível, botão interativo e qualquer chamada de rede. A composição com triagem e com o worker ainda chama `Offer` e `ExpireDue`; este módulo não altera esses pacotes.
