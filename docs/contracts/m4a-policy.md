# M4A — política de canal

Módulo `chatisp/internal/channelpolicy`, migração exclusiva `00019_m4_channel_policy.sql`.
Não altera runtime, mensagens, outbox, roteador central, OpenAPI ou código gerado.
Não envia mensagens, configura números/credenciais, cria templates, submete aprovação
nem escolhe versão da Graph API. Nenhuma chamada externa é implementada.

## Persistência

Todas as tabelas têm RLS habilitada e FORCE, contexto transacional `app.tenant_id`,
FKs compostas de tenant, sem grants para platform/ingress ou leitura global de users.

| Tabela | Contrato |
| --- | --- |
| `contact_consents` | Histórico append-only de concessão/revogação: UUID, contato, caixa (canal/número), finalidade, origem, evidência mínima, instante e revogação. `sequence` ordena alterações concorrentes; última linha do escopo decide. Não nasce consentimento ao criar contato. |
| `wa_templates` | Chave tenant + WABA local + ID externo; nome, idioma, categoria, estado remoto, componentes JSON, instante e fonte de sincronização (`driver`/`fake`). A caixa só acessa a WABA vinculada ao número ativo. |
| `channel_policy_catalog_sync` | Chave tenant + WABA; `requested_generation` reserva e ordena consultas antes do I/O; `applied_generation` identifica o snapshot aplicado. Atualizações são serializadas pela linha da WABA. RLS FORCE e grants somente ao app. |
| `channel_policy_pending` | Ponte para junção: ID igual ao ID de outbox, contato, caixa, finalidade e estado `pending`, `uncertain`, `dispatched` ou `cancelled`. Somente proativas são registradas. |

O escopo é contato + caixa + finalidade, não telefone global. Origem e evidência
devem conter referência mínima verificável (por exemplo ID do evento autorizado),
sem documentos. O módulo não registra corpos, evidências ou segredos em logs.
Concessões são eventos novos; revogar não apaga a concessão anterior. Repetir uma
revogação preserva o estado e não invalida novamente operações já canceladas.

## Assinaturas para junção

```go
type Service struct {
    DB *db.Store
    Clock func(context.Context, pgx.Tx) (time.Time, error)
    Driver CatalogDriver
}
func (Service) EvaluateDispatchTx(context.Context, pgx.Tx, sectors.Principal, Dispatch) (Decision, error)
func (Service) RecordConsentTx(context.Context, pgx.Tx, sectors.Principal, ConsentInput) ([]uuid.UUID, error)
func (Service) RegisterPendingTx(context.Context, pgx.Tx, sectors.Principal, Scope, uuid.UUID) error
func InvalidateProactiveTx(context.Context, pgx.Tx, sectors.Principal, Scope) ([]uuid.UUID, error)
func SetPendingStateTx(context.Context, pgx.Tx, sectors.Principal, Scope, uuid.UUID, string) error
```

`Scope` contém `InboxID`, `ContactID`, `Purpose`; `Dispatch` acrescenta
`ConversationID`, `Mode` (`requested`/`proactive`), `Kind` (`freeform`/`template`)
e `TemplateID`. `ConsentInput` acrescenta `Source`, `Evidence`, `Revoke`.
`Decision` retorna `Allowed`, `Reason`, `Driver`, `OfficialWindow`,
`EvaluatedAt` e `WindowEndsAt` opcional. Erros de autorização não viram permissão.

O chamador abre `DB.WithTenantTx`, usa principal real, valida sessão e mantém a
mesma transação até concluir a reserva de despacho. Revalidar imediatamente antes
da fronteira externa, inclusive em retries. A decisão é uma fotografia, sem token
de autorização reutilizável. Permanecem obrigatórios no despachante os contratos
M2 de posse, epoch, suspensão, idempotência e estado incerto. Nunca repetir um envio
incerto por esta política. `Mode` é classificação confiável do fluxo de origem;
não aceitar uma escolha arbitrária do cliente para disfarçar proativa como solicitada.

Ordem do módulo: gate compartilhado do tenant e validação do principal,
número/conector `FOR SHARE` na mesma ordem de `configuration.lockPhone` (M2),
autorização/membership/setor da caixa e caixa `FOR SHARE`, contato `FOR UPDATE`,
autorização da conversa (incluindo `FOR SHARE` da conversa), WABA e template ou
ponte de pendências. A descoberta inicial do conector é uma leitura sem lock,
escopada ao tenant; o vínculo caixa/conector é revalidado após os locks e a
autorização, sem conceder acesso pelo resultado da descoberta. Web bloqueia
somente o conector antes da caixa. Listagem e ambas as fases de sincronização
seguem a mesma ordem. Isso permite que a rotação M2 mantenha número/conector
`FOR UPDATE` e depois atualize a caixa sem ciclo de espera com a política.
Consentimento de agente também verifica e bloqueia a conversa visível somente
depois do contato. Isso acompanha o ingresso existente: contato `FOR SHARE`
antes da conversa `FOR UPDATE` em `AcceptInbound`. Não chamar o módulo com lock
prévio de caixa/configuração fora dessa ordem, nem de conversa e depois tentar
bloquear contato. Não adquirir antes locks de pendências/outbox e depois chamar
o módulo. A junção deve compor essa ordem com o lockorder M2 antes de ligar o runtime.
O lock do contato serializa concessão, revogação, registro e avaliação, mesmo sem
linha de consentimento. A suspensão e revogação de acesso usam os gates e locks M3A.

Ao enfileirar proativa, chamar `RegisterPendingTx` no mesmo commit da outbox; o ID
é idempotente e não pode mudar de escopo. Ao sair de pendente, espelhar o estado com
`SetPendingStateTx` na mesma transação. Estado incerto só pode ser reconciliado para
despachado, nunca retornar a pendente ou ser cancelado pela revogação.
`RecordConsentTx(Revoke=true)` chama `InvalidateProactiveTx` e devolve IDs para que
a junção cancele as linhas correspondentes da outbox **antes do mesmo commit**.
Se isso falhar, reverter tudo. Não registrar atendimento solicitado nessa ponte.
Os wrappers HTTP persistem e invalidam a ponte; a outbox atual só será conectada
pela tarefa de junção, que deve usar o contrato transacional, sem cancelamento
posterior assíncrono. Até essa junção o módulo isolado não governa envios atuais.

## Janela e templates

O clock padrão consulta `clock_timestamp()` do servidor PostgreSQL; testes injetam
um clock. A janela é `[última entrada, última entrada + 24h)`, por tenant +
caixa/número + conector + identidade externa (`contact_identity_id`). A identidade
é lida da conversa de destino já autorizada e bloqueada, nunca escolhida pelo
chamador. A busca inclui conversas/ciclos anteriores encerrados da mesma identidade;
outra identidade do mesmo contato na mesma caixa/conector não abre essa janela.
O escopo de consentimento permanece contato + caixa + finalidade. Somente mensagem recebida,
pública, inbound/customer, não histórica, com `trusted_at` não futuro é elegível.
Eco outbound, bot, menu, nota, registro técnico, histórico e timestamp futuro
não reabrem janela. Fechar atendimento local não modifica a janela.

Driver vem de `channel_connectors.connector_type`, nunca de input HTTP. Canal
precisa estar habilitado/conectado e número/WABA ativos. Contato bloqueado é negado.
No Cloud, texto livre fora da janela retorna `window_closed`; não existe conversão
automática. Template é outra operação: ID deve existir na mesma WABA, estar
`APPROVED` e ter fonte `driver`. Proativas de qualquer tipo exigem concessão vigente
da finalidade; atendimento solicitado não cria nem pressupõe essa concessão.
Web retorna `web_capability_requires_driver_policy` e `OfficialWindow=false`;
nenhuma garantia oficial ou capacidade de envio Web é inventada.

```go
type CatalogDriver interface {
    Templates(context.Context, string) ([]Template, error) // snapshot completo da WABA remota
    IsFake() bool
}
func (Service) SyncTemplates(context.Context, sectors.Principal, uuid.UUID) error
func (Service) ListTemplates(context.Context, sectors.Principal, uuid.UUID) ([]Template, error)
```

Antes do I/O, uma transação autoriza a caixa e reserva uma geração crescente por
tenant/WABA em `channel_policy_catalog_sync`, inclusive entre caixas da mesma WABA.
Consulta do driver ocorre fora da transação; autorização e vínculo WABA são
revalidados antes de substituir atomicamente o snapshot. A transação final bloqueia
a linha de geração `FOR UPDATE` e aceita somente a geração solicitada mais recente,
mantendo o lock até concluir a substituição e atualizar `applied_generation`.
Resposta obsoleta retorna `ErrStaleCatalog` (HTTP 409), sem alterar o catálogo ou
`synced_at`. Snapshot completo vazio remove templates antigos; falha mantém catálogo
anterior, mas a reserva persiste e continua impedindo aplicação de respostas antigas.
Uma nova consulta explícita é necessária para atualizar após falha. A geração ordena
requisições locais; não inventa uma versão remota nem detecta mudanças posteriores
no provedor sem nova sincronização.
`FakeCatalog` retorna `IsFake=true`; sua fonte aparece na listagem e não autoriza
despacho real. Adaptador oficial ainda será injetado na junção; o teste de contrato
usa um driver local sem I/O. Não há implementação Graph real nesta entrega.

## HTTP modular

`channelpolicy/httpapi.Register(huma.API, Dependencies)` recebe Auth, Policy,
TrustedOrigins. Base `/api/panel/inboxes/{inbox_id}/channel-policy`:

| Método/rota | Operação |
| --- | --- |
| `GET /templates` | Catálogo da WABA da caixa autorizada, com fonte explícita |
| `POST /templates/sync` | Admin real sincroniza pelo driver injetado |
| `POST /contacts/{contact_id}/consents` | Origem, evidência, finalidade, `revoke`; retorna IDs invalidados |

Sessão real, Origin exata e CSRF nas mutações, sem superadmin/enrollment.
Membership, tenant ativo e setor/caixa são revalidados transacionalmente. Agente
precisa de conversa visível daquele contato na caixa; supervisor lê catálogo e
registra consentimento, mas não sincroniza/configura credenciais ou números.
Erros HTTP sanitizados não expõem dados do driver ou banco.

Provas PG cobrem o interleaving de rotação M2 (locks reais de número/conector,
atualização da caixa e versão) com avaliação/listagem por admin e supervisor e
ambas as fases de sincronização; ingresso real `AcceptInbound` concorrente com avaliação; respostas
de catálogo invertidas (pausa, snapshot vazio e falha), borda exata de 24h,
isolamento entre duas identidades do mesmo contato/caixa e entrada elegível em
conversa anterior encerrada da própria identidade, tipos inelegíveis, consentimento explícito,
finalidades, rollback e serialização opt-out/despacho, pendentes versus incertos,
WABA/estado/fake/snapshot, RLS FORCE, suspensão e autorização HTTP real.
