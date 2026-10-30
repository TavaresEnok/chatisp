# M3A — setores e autorização operacional

O módulo é independente do router: registrar `httpapi.Register(api huma.API, httpapi.Dependencies{Auth: authService, Sectors: sectors.New(store), TrustedOrigins: origins})`. Não há endpoints Bearer. O cookie `__Host-session` autentica; mutações exigem `Origin` incluída exatamente em `TrustedOrigins` e `X-CSRF-Token` válido. O domínio revalida tenant ativo, usuário, membership ativo e papel persistido na transação. Superadmin não é operador. Apenas admin altera configuração; supervisor consulta todos setores e caixas; agente consulta setores associados e caixas na interseção.

## Painel

* `GET /api/panel/sectors` retorna `{ "sectors": [{"id", "name", "active", "legacy_default"}] }`.
* `GET /api/panel/sectors/{sector_id}` retorna `membership_ids`, `inbox_ids` e `default_inbox_ids`; agentes veem apenas seu próprio membership.
* `POST /api/panel/sectors` recebe `{ "name": "..." }` e retorna o setor criado (201).
* `PATCH /api/panel/sectors/{sector_id}` recebe `name` e/ou `active`; desativar setor padrão de alguma caixa gera conflito.
* `PUT` e `DELETE /api/panel/sectors/{sector_id}/memberships/{target_id}` incluem e removem membership; inclusão exige membership ativo no mesmo tenant.
* `PUT` e `DELETE /api/panel/sectors/{sector_id}/inboxes/{target_id}` incluem e removem caixa do mesmo tenant.
* `PUT /api/panel/sectors/{sector_id}/inboxes/{target_id}/default` seleciona setor ativo já vinculado como padrão da caixa.

Mutações gravam auditoria na mesma transação. Respostas: 401 sessão ausente, 403 autorização/origem/CSRF, 404 recurso ausente ou alheio, 409 conflito de integridade, 422 entrada inválida, 503 falha interna sem detalhes do banco. Inclusão de vínculo existente e remoção de vínculo ainda necessário pelo padrão geram 409. Após troca explícita para outro setor ativo/vinculado, remover vínculo populado retorna 200 e revoga autorização operacional, preservando conversas, setor histórico, protocolos e mensagens. A troca do padrão não modifica conversas existentes.

## Integração transacional

`sectors.Principal{TenantID, UserID, MembershipID uuid.UUID, Role string, IsSuperadmin bool}` é independente de attendance. Dentro de `store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error { ... })`, usar `sectors.ValidatePrincipal(ctx, tx, p) error`, `sectors.AuthorizedInboxIDs(ctx, tx, p) ([]uuid.UUID, error)`, `sectors.AuthorizedConversationIDs(ctx, tx, p, inboxID) ([]uuid.UUID, error)`, `sectors.CanAccessInbox(ctx, tx, p, inboxID) (bool, error)` e `sectors.CanAccessConversation(ctx, tx, p, inboxID, conversationID) (bool, error)`. Essas funções consultam relações persistidas e validam o principal em cada chamada. Não confiar em atribuição, cache de permissão ou somente tenant. Filtrar listas, histórico, pesquisa e eventos por IDs autorizados na mesma transação da leitura; para evento de conversa verificar sua caixa e conversa antes de divulgar payload. A aplicação dessas funções às rotas e eventos existentes pertence a m3-operational-access; esta entrega não afirma que M2 já aplica essa regra.

## Compatibilidade e ingresso

A migração 00009 cria um setor `Padrão` por tenant existente, associa todas as caixas existentes e memberships ativos, escolhe esse setor como padrão das caixas e atribui conversas existentes com caixa ao setor. Isso preserva os direitos M2, que permitiam acesso a todas as caixas do tenant, e não altera protocolos, mensagens ou cifra. Memberships convidados/inativos e novos vínculos não recebem acesso automático; novas caixas precisam ser vinculadas e receber padrão explicitamente. No ingresso, o trigger usa o padrão ativo da caixa quando `sector_id` não é informado e rejeita ingresso sem padrão. Conversas placeholder sem caixa permanecem sem setor.

A migração incremental 00012 separa identidade histórica e autorização: FKs compostas para setor e caixa preservam integridade tenantizada; o trigger exige setor ativo e vínculo presente em INSERT ou mudança efetiva de tenant/caixa/setor. Atualizações sem mudança dessa identidade preservam histórico já revogado. A validação toma o portão compartilhado do tenant antes de locks `FOR SHARE` no setor e no vínculo, mantidos até commit. DELETE concorrente espera a atribuição ou vence e faz a atribuição falhar. O serviço de configuração mantém o portão exclusivo antes das linhas. O trigger usa direitos do invocador, sem SECURITY DEFINER, e mantém FORCE RLS. O padrão continua protegido por sua FK ao vínculo. Rollback é recusado para evitar perda de dados. Autorização por setor é adicional e cabe aos consumidores descritos acima: até admin/supervisor precisam do vínculo para acessar a conversa histórica operacionalmente.

## Protocolo de autorização atômica

`CanAccessInbox`, `CanAccessConversation`, `AuthorizedInboxIDs` e `AuthorizedConversationIDs` leem membership, setor, `sector_memberships`, `sector_inboxes` e a linha da conversa com `FOR SHARE`, mantido até o fim da transação do consumidor. Revogação, remoção de caixa, desativação de setor, troca de papel e transferência de setor bloqueiam até o consumidor confirmar; leituras e ações na mesma `pgx.Tx` após a chamada não perdem a autorização. O portão compartilhado do tenant (`pg_advisory_xact_lock_shared`) coordena com `SetTenantStatus`. Novas funções: `RequireInbox(ctx, tx, p, inboxID) error`, `RequireConversation(ctx, tx, p, inboxID, conversationID) error` (ErrForbidden se negado) e `Service.WithConversation(ctx, p, inboxID, conversationID, fn func(context.Context, pgx.Tx) error) error`. Consumidores devem manter política e leitura/ação na mesma transação.

Mutações de configuração adquirem o portão exclusivo (`db.Store.LockTenant`) antes de validar o principal e o mantêm até o commit; tenant suspenso recusa com 403.

Compatibilidade M2: `RequireConversation` também pode retornar o sentinel `attendance/store.ErrNotFound` quando a caixa solicitada diverge e a conversa é autorizada, com locks, na caixa correta. Essa resposta preserva HTTP 404 sem conceder a operação. Conversa ausente, alheia ou revogada continua com 403, inclusive quando consultada por uma caixa diferente.

## Auditoria dos vínculos

`audit_logs.action` inclui o setor: `sector.membership.add|remove sector=<id>` (target_type `membership`, target_id = membership), `sector.inbox.add|remove sector=<id>` (target_type `inbox`), `sector.default sector=<novo> previous=<anterior|none>` (target_type `inbox`). Criação/edição usam target_type `sector`.
