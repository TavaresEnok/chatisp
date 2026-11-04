# M6 — API pública de leitura

Bloco isolado da seção 11, somente leitura. Não emite OpenAPI, não altera `openapi.json` nem `schema.ts`, e não chama Meta, ERP ou Evolution. Webhooks, escrita e o token `api_tokens` da migração 00031 ficam fora.

A autenticação do painel continua no cookie `__Host-session` com CSRF. Esta API não aceita esse cookie como credencial. O segredo vai em `Authorization: Bearer`. O valor é opaco, aparece uma vez na emissão Go (`publicapi.Issue`) e o banco guarda só o SHA-256, o mesmo formato de imagem da sessão do painel. Tabela exclusiva: `public_read_tokens`, migração `00038_m6_public_read.sql`. Escopo gravado: `read`. Revogação: `revoked_at`. Limite simples: `request_limit`, de 1 a 6000 pedidos por minuto para aquele token.

O papel `chatisp_app` não lê a tabela. `public_read_resolve` só corre com `app.access_scope=public_read` e devolve o tenant, o escopo, a revogação, o limite e o status vivo do provedor. Tenant `suspended` responde 403 `provedor suspenso` e não devolve conversa, mensagem nem contato. Token revogado responde 401 `token revogado`. Cookie sem Bearer responde 401 `credencial ausente`.

Há também um teto de 600 pedidos por minuto por tenant, no mesmo contador em memória do processo. Estouro responde 429 com `Retry-After` e detalhe `limite do token excedido` ou `limite do provedor excedido`. O contador não é compartilhado entre processos.

## Rotas

Prefixo montado em `/api/public`. O handler em si registra `/v1/...`.

| Método | Caminho | Resposta |
| --- | --- | --- |
| GET | `/api/public/v1/conversations/{ref}` | Conversa do tenant do token. `{ref}` é o UUID ou o protocolo. |
| GET | `/api/public/v1/conversations/{ref}/messages` | Mensagens com `visibility = public`. `limit` opcional, de 1 a 100, padrão 50. |
| GET | `/api/public/v1/conversations/{ref}/contact` | Contato vinculado: id, nome, telefone, e-mail e bloqueio. |

Não há rota de escrita. A conversa não inclui resumo, nota, corpo de mensagem nem credencial de canal. A mensagem pública não inclui nota interna (`visibility = internal`). O contato não inclui observações, documento, HMAC, ciphertext nem `credential_ref`.

Erros usam `application/problem+json`, em português, sem segredo e sem stack. Respostas levam `Cache-Control: no-store`.
