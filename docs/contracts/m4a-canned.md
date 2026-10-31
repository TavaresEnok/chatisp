# M4A — respostas prontas e assinatura

Migração exclusiva `00018_m4_canned_replies.sql`. Nenhuma ligação ao runtime,
Send/dispatch, OpenAPI central ou schemaTS nesta entrega. A composição chama
`cannedreplies.Register(api, cannedreplies.Dependencies{Auth: authService,
CannedReplies: cannedreplies.New(store), TrustedOrigins: origins})`.

Todos os endpoints exigem sessão real `__Host-session`. PUT/POST/DELETE exigem
Origin da lista configurada e X-CSRF-Token. Superadmin e sessão de enrollment
não operam. Tenant ativo e membership/role são revalidados e fixados na mesma
transação da operação. Admin/supervisor escrevem; agente consulta e renderiza.
O catálogo é compartilhado pelo tenant, independente do setor; a renderização
exige acesso setorial atual à conversa e à caixa usando M3A.

| Método e caminho | Entrada / saída |
|---|---|
| GET /api/panel/canned-replies | search literal (até 120 caracteres), limit 1–100 (25), offset 0–100000; `{replies:[],has_more:false}` |
| POST /api/panel/canned-replies | `{shortcode,title,content}`; 201 com `{id,shortcode,title,content,version:1}` |
| PUT /api/panel/canned-replies/{reply_id} | `{shortcode,title,content,version}`; registro com versão incrementada |
| DELETE /api/panel/canned-replies/{reply_id}?version=1 | objeto vazio; versão obrigatória |
| POST /api/panel/canned-replies/{reply_id}/render | `{inbox_id,conversation_id}`; `{text,missing:[]}` |
| GET /api/panel/attendant-signature | `{enabled,prefix,version}` |
| PUT /api/panel/attendant-signature | `{enabled,prefix,version}`; versão incrementada |

Erros: 401 sessão inválida, 403 política/CSRF/Origin, 404 registro ausente,
409 shortcode duplicado/versão obsoleta, 422 dados/variáveis inválidos, 503
dependência indisponível. Não são expostos detalhes SQL. Busca literal sem
wildcards, ordenação shortcode/id, paginação offset (mudanças concorrentes
podem mover páginas). Shortcode minúsculo único por tenant, regex
`^[a-z0-9][a-z0-9_-]{0,63}$`; título 1–120 e conteúdo 1–10000 caracteres.
Não há anexos (V2). CRUD e configuração geram audit_logs transacional com
ator, alvo e versão, sem copiar o conteúdo para auditoria.

Variáveis exatas: `{{nome_cliente}}` = contacts.name; `{{nome_atendente}}` =
auth.Principal.Email; `{{protocolo}}` = conversations.protocol. A identidade
atual não tem campo nome: usa-se o e-mail autenticado completo, sem inventar
nome ou consultar users globalmente. Valores ausentes preservam o marcador e
aparecem uma vez em `missing`, em ordem de ocorrência. Só essas variáveis,
sem expressões, propriedades, HTML, acesso remoto ou expansão recursiva.
Chaves avulsas e `<`/`>` no template são rejeitados. Valores são texto simples:
o cliente deve inseri-los via valor de textarea/textContent, nunca innerHTML.

Exemplo: criar `{shortcode:"reiniciar",title:"Reiniciar equipamento",
content:"Olá {{nome_cliente}}, protocolo {{protocolo}}. Sou {{nome_atendente}}."}`.
Render retorna texto para o cliente inserir no rascunho. Não cria mensagem,
não altera rascunho persistente, não assina e não envia; o envio segue o fluxo
de texto existente e exige ação explícita do atendente.

Assinatura padrão desligada, prefixo `{{nome_atendente}}: `, versão virtual 0
até a primeira gravação. Prefixo 1–240 caracteres, aceita apenas
`{{nome_atendente}}` ou texto literal. PUT compara versão (inclusive 0) e
audita. Exemplo `{enabled:true,prefix:"{{nome_atendente}}: ",version:0}`.

Contrato para junção posterior: `AcceptPublicHumanTx(ctx, tx pgx.Tx,
p auth.Principal, acceptanceID uuid.UUID, originalText string) (string,error)`.
Somente a aceitação de mensagem humana pública chama esse helper, depois da
autorização M3A e antes da inserção, na MESMA transação tenant da mensagem.
`p` vem de Authenticate e nunca do corpo HTTP. Notas e bot não chamam o
helper; dispatch/retry não reaplicam assinatura. O acceptanceID deve ser o
identificador estável da aceitação/mensagem, com originalText original.
Persistir a mensagem usando o retorno. Rollback desfaz mensagem e receipt.
O receipt armazena original, ator e resultado; replay retorna exatamente o
resultado anterior mesmo com configuração alterada. Mesmo ID com texto/ator
diferente conflita. Nunca deduzir aplicação por startsWith: o texto original
pode legitimamente começar pelo prefixo. Há serialização por tenant+ID para
primeira aceitação concorrente e comparação de payload. Limite final 16000
caracteres; identidade ausente com variável exigida gera erro, sem inventar.

Tabelas reais: canned_replies (PK tenant_id/id, unique tenant_id/shortcode),
attendant_signature_settings (PK tenant_id), attendant_signature_receipts
(PK tenant_id/acceptance_id, FK tenant/membership). Todas usam FORCE RLS,
tenant local transacional, sem permissão global de users. Receipts são
imutáveis para app; não devem ser removidos enquanto replay for possível.
