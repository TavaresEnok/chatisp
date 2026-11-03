# M5B — presença do atendente

Entrega independente, sem composição do runtime, OpenAPI ou schema TS. A composição posterior chama `presence/httpapi.Register(api, Dependencies{Auth, Presence: presence.New(store), TrustedOrigins})`. Não há WebSocket, worker, round-robin, desatribuição, alteração de conversa, capacidade, horário ou chamada externa.

## Persistência

Migração exclusiva `00024_m5_presence.sql`. Quatro tabelas com RLS ENABLE + FORCE e política `tenant_id = app.tenant_id` da transação, sem política para outros papéis:

| Tabela | Chave | Papel |
| --- | --- | --- |
| `presence_connections` | `(tenant_id, session_id, tab_id)` | Heartbeat da aba. `expires_at` é o instante injetado mais 30s. |
| `presence_operator` | `(tenant_id, user_id)` | Estado manual `available` ou `busy`, e `explicit_offline_at` quando a última aba é fechada. |
| `presence_settings` | `tenant_id` | Tolerância de offline em segundos, de 0 a 86400, com versão. |
| `presence_commands` | `(tenant_id, membership_id, request_id)` | Hash SHA-256 do comando e resposta histórica. |

`presence_connections` referencia a sessão. O papel `chatisp_app` não recebe `SELECT` em `users` nem em `sessions`. A função `presence_session_live(session, tenant, membership, user, instant)` devolve só boolean: confirma o escopo do tenant, resolve o usuário pela sessão, trava `users` antes de `sessions` e exige usuário `active` e sessão não revogada nem vencida no instante informado. Não devolve e-mail, segredo nem hash.

Ausência de linha em `presence_settings` significa tolerância padrão de 300 segundos e versão 0. O módulo não apaga operador, comando nem auditoria.

## Estado agregado

Há três estados: `available`, `busy` e `offline`.

- Uma aba com heartbeat ainda não vencido e sessão viva deixa o atendente conectado. Fechar outra aba não o torna offline.
- `busy` manual prevalece sobre `available` enquanto houver aba viva. Offline não vira disponível por causa do manual; ao reconectar, `busy` continua valendo até novo comando.
- Sem aba viva, o estado é `offline`. O início do offline é `explicit_offline_at` quando a última aba ainda dentro do heartbeat é fechada, ou o maior `expires_at` quando os heartbeats vencem sem close. Quem nunca conectou não tem início de offline. Close de aba já vencida grava esse maior instante entre o `expires_at` removido, os que permanecem e um `explicit_offline_at` já existente; o relógio do close não entra. Close que não remove linha não cria nem move `explicit_offline_at`.
- `offline_for_unassign` é verdadeiro só quando esse início existe e `agora >= início + tolerância`. Tolerância 0 torna o sinal imediato. O campo é apenas cálculo: nenhuma função altera conversa, responsável, vaga ou fila.
- Usuário com status diferente de `active`, matrícula inativa ou tenant suspenso nunca fica `assignable`. Tenant suspenso recusa `Effective` e `ListAvailable` com acesso negado, para a transação chamadora não tratar a leitura como sucesso. Matrícula ou usuário desativado, dentro de tenant ativo, devolve snapshot `offline` com `assignable=false`.

`ListAvailable` lista só quem está `available` no setor ativo, com matrícula ativa no setor. Não consulta capacidade. Setor ausente ou inativo devolve lista vazia na função interna; o método HTTP `List` responde 404.

Ordem de lock: advisory do tenant (exclusivo só na troca da tolerância, e antes do compartilhado), matrícula do chamador quando há sessão, `presence_settings`, `presence_operator`, `presence_session_live` (usuário antes da sessão) e por fim as linhas de conexão. `ListAvailable` trava o setor e os vínculos do setor antes das matrículas, em ordem de `user_id`. Chamar antes de locks exclusivos de conversa.

## API Go

Pacote `chatisp/internal/presence`:

```go
func New(database *db.Store) Service
func (Service) Heartbeat(context.Context, Principal, tab uuid.UUID) (Snapshot, error)
func (Service) Close(context.Context, Principal, tab uuid.UUID) (Snapshot, error)
func (Service) SetManual(context.Context, Principal, ManualCommand) (Snapshot, error)
func (Service) Current(context.Context, Principal) (Snapshot, error)
func (Service) GetSettings(context.Context, Principal) (Settings, error)
func (Service) PutTolerance(context.Context, Principal, ToleranceCommand) (Settings, error)
func (Service) List(context.Context, Principal, sector uuid.UUID) ([]Snapshot, error)
func Effective(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, now time.Time) (Snapshot, error)
func ListAvailable(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID, now time.Time) ([]Snapshot, error)
```

`Principal` é `auth.Principal`. `Service.Now`, quando preenchido, é o relógio de Heartbeat, Close, SetManual, Current, settings e List. `Effective` e `ListAvailable` usam o `now` do argumento e exigem transação cuja `app.tenant_id` seja o tenant informado. Não abrem outra transação e não autenticam sozinhas: o chamador humano passa antes por `ValidatePrincipal` na mesma transação; o round-robin futuro chama as duas funções dentro da transação que já escolhe a vaga. Nenhuma das duas desatribui.

`Snapshot` traz `user_id`, `membership_id`, `state`, `manual`, `connected`, `tab_count`, `last_heartbeat`, `connected_until`, `offline_since`, `tolerance_seconds`, `offline_for_unassign` e `assignable`.

`SetManual` e `PutTolerance` são idempotentes por `(tenant, membership, request_id)`. O mesmo corpo devolve a resposta gravada e não gera outro audit. Outro corpo com a mesma chave, ou versão de tolerância desatualizada, conflita. Um retry continua sujeito à autorização atual. O registro não expira neste bloco. Audit `presence.manual` e `presence.tolerance`, com ator real e sem conteúdo de mensagem.

## API HTTP

Cookie `__Host-session`. Mutações exigem `Origin` exatamente confiável e `X-CSRF-Token`. Tenant e matrícula vêm da sessão. Superadmin e sessão só de cadastro MFA recebem 403. Agente, supervisor e admin operam a própria presença. A lista do setor é de admin e supervisor. A tolerância é lida pelos três; só admin grava.

| Método | Caminho | Corpo | Resposta |
| --- | --- | --- | --- |
| GET | `/api/panel/presence` | — | Snapshot |
| POST | `/api/panel/presence/heartbeat` | `{tab_id}` | Snapshot |
| DELETE | `/api/panel/presence/tabs/{tab_id}` | — | Snapshot |
| PUT | `/api/panel/presence/manual` | `{request_id, state}` | Snapshot |
| GET | `/api/panel/presence/settings` | — | Settings |
| PUT | `/api/panel/presence/settings` | `{request_id, expected_version, offline_tolerance_seconds}` | Settings |
| GET | `/api/panel/sectors/{sector_id}/presence` | — | `{agents, heartbeat_ttl_seconds, tolerance_note}` |

`state` do manual é `available` ou `busy`. Close de aba já ausente é sucesso. Status: 200; 401 sessão ausente, revogada ou expirada; 403 papel, CSRF, Origin, suspensão, matrícula ou usuário inativo; 404 setor ausente ou inativo na lista HTTP; 409 versão ou request_id; 422 comando; 503 indisponibilidade. Erros não expõem SQL nem segredo.

## Provas

PostgreSQL real, relógio injetado, sem sleeps: duas abas e dois sessions, fechar uma, expirar a outra, ocupado manual, tolerância antes e depois do limite, close tardio sem reiniciar a tolerância, close de aba inexistente sem inventar offline, setor alheio, usuário desativado, matrícula desativada, tenant suspenso, RLS sem escopo e entre tenants, replay de comando. O adaptador HTTP prova sessão, MFA de admin, Origin, CSRF e papéis.

Checks deste bloco: `npm ci --prefix backend/tools/pg --no-audit --no-fund` e `go -C backend test -count=1 ./internal/presence/...`.
