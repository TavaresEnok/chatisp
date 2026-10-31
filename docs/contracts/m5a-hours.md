# M5A — horários por caixa

Entrega independente, sem composição do runtime, OpenAPI ou schema TS. A composição posterior chama `businesshours/httpapi.Register(api, Dependencies{Auth, BusinessHours: businesshours.New(store), TrustedOrigins})`. Não há worker, envio externo, alteração de conversa, atribuição, cálculo de capacidade ou mudança dos relógios de métricas. A saída humana permanece disponível no fluxo existente.

## Persistência e autorização

Migração exclusiva `00021_m5_business_hours.sql`. `inbox_business_hours` tem chave `(tenant_id,inbox_id)`, FK composta para `inboxes`, `version bigint`, `configured boolean` e `config jsonb`. `business_hours_commands` tem chave `(tenant_id,membership_id,request_id)`, hash SHA256 do comando completo e resposta persistida. Ambas usam RLS ENABLE + FORCE, política pelo `app.tenant_id` local da transação e privilégios exclusivos de aplicação. Não há leitura de `users` no módulo.

Admin lê/configura/exclui; supervisor lê/consulta decisão; agent e superadmin não acessam esta configuração. Sessão real, MFA/enrollment, CSRF nas escritas e Origin exato confiável usam o serviço de autenticação existente. Tenant e membership vêm da sessão, nunca do corpo. O serviço revalida membership/role/status, suspensão e acesso à caixa na mesma transação, pelo contrato `sectors.RequireInbox`.

Escritas adquirem primeiro o gate exclusivo do tenant via `Store.LockTenant`, depois autorização e configuração; usam a mesma ordem de mutações administrativas de setores. Leituras/decisões usam gate compartilhado e locks `FOR SHARE` mantidos até commit. Vínculos e setores usados como destino são consultados e travados novamente: revogar vínculo, desativar setor ou suspender tenant não pode invalidar uma decisão dentro da transação consumidora. O consumidor não deve reutilizar uma decisão depois do commit para executar uma ação futura sem reavaliar.

## API modular

Base: `/api/panel/inboxes/{inbox_id}/business-hours`.

| Método | Caminho | Corpo/consulta | Resposta |
| --- | --- | --- | --- |
| GET | base | — | Configuration |
| PUT | base | Command | Configuration |
| DELETE | base | `{request_id,expected_version}` | Configuration sem configuração ativa |
| GET | base + `/evaluation` | `instant` RFC3339 obrigatório | Decision |

`Command = {request_id: UUID não nulo, expected_version: inteiro >= 0, config: Config}`. PUT cria ou substitui integralmente. Ausência inicial retorna `version=0, configured=false`. DELETE conserva tombstone e incrementa versão; recriar exige a versão atual. Não há remoção de histórico. Mesmo request_id e conteúdo pelo mesmo membership retorna exatamente a resposta histórica sem novo audit; reutilização com outro conteúdo/caixa/operação conflita. Um retry continua sujeito à autorização atual. O registro de idempotência não expira neste bloco. Versão desatualizada retorna 409, inclusive duas escritas concorrentes com a mesma versão: uma vence. Audit transacional `business_hours.put`/`business_hours.delete`, alvo inbox e ator real, sem textos de mensagens no log.

Status: 200 sucesso; 401 sessão ausente/revogada/expirada; 403 papel, enrollment, CSRF, Origin, suspensão ou caixa inacessível; 404 caixa inexistente no contrato interno EvaluateTx; 409 versão/request_id; 422 intervalos/fuso/comando; 503 indisponibilidade. Os erros não expõem SQL ou segredos.

`Configuration = {version,configured,timezone,config}`. `Config`:

```json
{
  "intervals": [
    {"day": 1, "start": 540, "end": 720},
    {"day": 1, "start": 780, "end": 1080}
  ],
  "welcome": "Olá! Protocolo {protocolo}",
  "outside_hours": "Estamos fora do expediente.",
  "nobody_available": "Aguarde na fila.",
  "on_call_sector_id": null
}
```

Dia 0=domingo até 6=sábado. Minutos locais, início inclusivo e fim exclusivo; início 0..1439 e fim 1..1440, sempre início < fim. `1440` significa meia-noite ao terminar aquele dia. Intervalos vazios, invertidos, sobrepostos ou duplicados são inválidos; adjacentes são válidos. Para atravessar meia-noite, dividir em dois dias (por exemplo segunda 1380..1440 e terça 0..60). Lista vazia ou dia ausente significa fechado, nunca 24 horas implícitas. Até 112 intervalos e 4000 caracteres por texto, sem NUL. Textos vazios desabilitam aquele texto. Placeholders são conteúdo armazenado, sem interpolação ou envio neste módulo.

O fuso vem somente de `tenants.timezone`, validado com IANA tzdata embutido em Go (UTC permitido, vazio/Local/inválido recusado). Não há fuso por caixa nem mudança de fuso por este endpoint. Alteração do fuso do tenant é refletida na leitura e cálculo seguintes. Plantão deve existir, estar ativo e vinculado à caixa no tenant ao salvar. Referência antiga é preservada na configuração para edição/audit, mas somente o campo de destino reautorizado de Decision pode ser usado para decisões.

## Contrato Go para composição

Pacote `chatisp/internal/businesshours`:

```go
func New(database *db.Store) Service
func (Service) Get(context.Context, Principal, uuid.UUID) (Configuration, error)
func (Service) Put(context.Context, Principal, uuid.UUID, Command) (Configuration, error)
func (Service) Delete(context.Context, Principal, uuid.UUID, Command) (Configuration, error)
func (Service) Evaluate(context.Context, Principal, uuid.UUID, time.Time) (Decision, error)
func EvaluateTx(ctx context.Context, tx pgx.Tx, instant time.Time, tenant, inbox uuid.UUID) (Decision, error)
func Evaluate(instant time.Time, configuration Configuration) (Decision, error)
func Validate(config Config) error
```

`Principal` é alias de `sectors.Principal`. `EvaluateTx` exige tx com `app.tenant_id` igual ao argumento; valida caixa, tenant ativo e fuso, prende gate/configuração/destinos até commit. Não autentica um usuário: consumidores humanos devem chamar `sectors.RequireInbox` na mesma tx antes; consumidores internos precisam da própria autorização de entrada. Entrar pelo gate de tenant antes de adquirir locks de entidades, conforme contrato de setores. Não usar uma transação de owner/platform para ampliar visibilidade.

`Decision` contém Configuration, `instant` normalizado UTC, `open`, `next_opening` UTC ou null, `default_sector_id`, `on_call_sector_id` reautorizados e `destination_sector_id`. Em expediente usa setor padrão ativo e vinculado; fora do expediente usa plantão válido, senão padrão válido; sem destino autorizado retorna null. Não consulta disponibilidade, não promete vaga e não reserva atribuição. Configuração ausente/excluída retorna `configured=false`, `open=false`, próxima abertura null e destino padrão válido; consumidor distingue ausência de configuração de fechamento explícito por `configured`.

`Evaluate` puro calcula só horários, sem acesso ao banco e sem destinos autorizados. A decisão avalia minutos de parede no fuso do tenant a partir do instante absoluto: no retorno do horário de verão, ambas as ocorrências do minuto são válidas; no avanço, minutos inexistentes não abrem. Próxima abertura percorre minutos reais UTC até nove dias, cobrindo semana e transições de fuso; em expediente ou semana sem intervalos retorna null. Instantes não dependem do relógio do servidor; não há sleeps. Não altera contagem de tempo operacional (que continua corrido, inclusive fora do expediente).

## Provas focadas

`schedule_test.go`: intervalos inválidos/adjacentes, meia-noite, almoço, semana vazia, bordas exatas, fuso São Paulo, gap e repetição DST em Nova York. `integration_test.go`: PostgreSQL 18.4 real com todas as migrações, CRUD/tombstone, replay/hash/versão, concorrência, audit, RLS sem escopo e entre tenants, vínculo removido/lock transacional/setor inativo, suspensão e membership revogada; adaptador HTTP com sessão real, MFA, Origin, CSRF e papéis admin/supervisor/agent/superadmin.

Checks deste bloco: `npm ci --prefix backend/tools/pg --no-audit --no-fund` e `go -C backend test -count=1 ./internal/businesshours/...`. Sem serviços externos, suíte global, geração central ou deploy.
