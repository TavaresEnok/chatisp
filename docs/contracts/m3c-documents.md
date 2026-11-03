# M3C — CPF/CNPJ opcional do contato

Módulo `contactdocs` para composição posterior. Migração exclusiva `00023_m3_contact_documents.sql`. Não altera runtime, roteador central, OpenAPI, dbgen nem o esquema TypeScript. O documento não é obrigatório para conversar e não autentica ninguém. Não há chamada a ERP, Meta ou Evolution.

## Persistência

Três tabelas, todas com `ENABLE` e `FORCE ROW LEVEL SECURITY` e política `tenant_isolation` só para `chatisp_app`. O dono da tabela também fica sujeito ao FORCE e não tem política de leitura. Não existe coluna de documento em claro.

| Tabela | Conteúdo |
| --- | --- |
| `contact_document_peppers` | Pepper aleatório de 32 bytes, cifrado com a chave mestra versionada. Um por tenant. AAD: entidade `contact_document_pepper`, campo `pepper`. |
| `contact_documents` | `kind` (`cpf` ou `cnpj`), `key_version`, `ciphertext` AES-GCM, `document_hmac` de 32 bytes, `version` monotônica. PK `(tenant_id, contact_id)`. HMAC único por tenant. FK composta para `contacts` e `memberships`. Ausência posterior conserva a linha: `kind`, `key_version`, `ciphertext` e `document_hmac` ficam nulos juntos e `version` continua. |
| `contact_document_commands` | Idempotência por tenant, membership e `request_id`. Guarda HMAC do pedido (com pepper) e o JSON mascarado. O JSON não pode ter a chave `document`. |

O HMAC é SHA-256 com a pepper do tenant e a mensagem inclui o tenant e os dígitos normalizados. Não é hash simples. A cifra usa `crypto.Ring`: nonce aleatório, versão da chave gravada junto do ciphertext, AAD `contact_document` / id do contato / `document`. Trocar tenant ou contato invalida a abertura. A chave mestra não entra no banco, em log nem em erro.

`Clear` apaga ciphertext e HMAC e incrementa `version`. A linha permanece, então a versão não volta a zero e não é reutilizada. A trilha fica em `audit_logs` (`contact_document.set`, `contact_document.clear`, `contact_document.reveal`) com resultado `success` ou `denied`, sem o valor, sem correlação do documento e sem evento realtime.

## Autorização

Toda operação corre em `WithTenantTx`, revalida membership ativa e tenant ativo (`sectors.ValidatePrincipal`, incluindo o gate compartilhado de suspensão) e aplica a mesma visibilidade de contato do diretório: contato manual sem conversa só para administrador ou autor; com conversa, exige acesso ao setor e à caixa. Quem não vê o contato recebe ausência, inclusive na busca. Escritas tomam o lock exclusivo do tenant antes da pepper, e a pepper antes do contato, para ordenar com a suspensão.

Papel | Máscara e busca exata | Gravar, apagar e revelar
--- | --- | ---
Administrador | Contatos visíveis | Sim, com auditoria
Supervisor | Contatos visíveis | Sim, com auditoria
Atendente | Contatos visíveis | Não. Revelação e escrita visíveis geram auditoria `denied`

Superadmin e sessão só de cadastro MFA são recusados. Contato que nunca teve documento devolve versão 0 e `present=false`. Depois de `Clear`, a leitura mascarada devolve `present=false` com a versão monotônica da ausência. Revelar ausência é 404. A gravação seguinte exige essa versão. Uma operação atrasada com a versão anterior ao `Clear` conflita, inclusive no ciclo set/clear/set.

## API Go

`New(*db.Store, *crypto.Ring) Service`

- `Set(ctx, principal, contactID, Command) (View, error)`
- `Clear(ctx, principal, contactID, Command) (View, error)`
- `Masked(ctx, principal, contactID) (View, error)`
- `Reveal(ctx, principal, contactID) (Revealed, error)`
- `FindByDocumentHMAC(ctx, principal, document string) (Match, error)`

`Command` traz `request_id`, `expected_version` e, no set, `document`. `expected_version` 0 só corresponde à ausência inicial, sem linha. Dígitos de CPF (11) ou CNPJ (14) são validados; pontuação `.`, `-`, `/` e espaços são removidos. CPF/CNPJ com dígitos repetidos ou verificadores errados é inválido. `View` e `Match` expõem `kind` e a máscara `***.***.***-**` ou `**.***.***/****-**`, nunca os dígitos. `Revealed.Document` são só os dígitos canônicos e só sai por `Reveal`. Repetir o mesmo `request_id` e o mesmo pedido devolve a `View` armazenada. Pedido diferente com a mesma chave, versão obsoleta ou HMAC já usado por outro contato do tenant geram conflito. `Clear` de documento presente grava a ausência e soma um à versão. Repetir `Clear` sobre ausência já gravada, com a versão atual, não avança de novo. Erros sentinela: `ErrForbidden`, `ErrNotFound`, `ErrInvalid`, `ErrConflict`, `ErrUnavailable`. Nenhum deles inclui o documento.

## HTTP

`httpapi.Register(api, Dependencies{Auth, Documents, TrustedOrigins})` publica, sem montar no roteador do processo:

| Método | Caminho | Efeito |
| --- | --- | --- |
| GET | `/api/panel/contacts/{contact_id}/document` | `View` mascarada. Sessão. |
| PUT | mesmo caminho | `Command` com documento. Sessão, Origin confiável e CSRF. |
| DELETE | mesmo caminho | `Command` sem documento. Sessão, Origin e CSRF. |
| POST | `.../document/reveal` | `Revealed`. Sessão, Origin e CSRF. Auditoria. |
| POST | `/api/panel/contacts/document-search` | JSON `document`. Busca exata. Sessão, Origin e CSRF. |

Respostas levam `Cache-Control: no-store`. Erros: 401 sessão, 403 origem/CSRF/papel, 404 invisível ou sem documento na revelação/busca, 409 versão, idempotência divergente ou documento já ligado a outro contato, 422 documento inválido, 503 dependência. O corpo de erro é genérico e não ecoa o valor.

A validação do corpo ocorre antes do handler. Um JSON ilegível responde 400. Um `document` de tipo errado, ou um campo obrigatório ausente com CPF/CNPJ no mesmo objeto, responde 422. Nessas respostas o campo `value` é removido e nenhum dígito do documento permanece, mesmo sem sessão. Sucesso de revelação continua devolvendo os dígitos só ao papel autorizado.
