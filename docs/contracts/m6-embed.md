# M6 — painel externo em iframe

V1 simples das seções 08 e 11. O chat embute uma página que já existe. Não há ERP, fatura, documento nem segunda interface financeira neste bloco.

A composição posterior chama `embed.Mount` no roteador do painel com `embed.New(store)`. As rotas não entram no OpenAPI gerado. Não há worker, webhook, mídia nem chamada ao ERP.

## Configuração por provedor

Migração exclusiva `00036_m6_embed.sql`. `provider_embed_settings` tem uma linha por tenant, `version`, `allowed_origin` e `panel_url`. RLS ENABLE + FORCE pela `app.tenant_id` da transação. Privilégio só de `chatisp_app`.

Admin grava e lê. Supervisor lê. Agente e superadmin não acessam esta configuração. Na conversa, admin, supervisor e agente autorizados naquela conversa podem receber o contexto de incorporação. Sessão, CSRF e Origin do navegador seguem o serviço de autenticação existente. Tenant e membership vêm da sessão.

A URL e a origem passam por `urlguard` com escopo vazio: só destino público em HTTP/80 ou HTTPS/443, sem DNS privado, loopback, metadata, link-local, credencial, consulta ou fragmento. A origem da URL tem de ser exatamente a origem configurada. Origem com caminho, ou URL de outro host, é recusada e a página não é embutida. A gravação valida antes de abrir a transação de escrita.

Ausência de linha significa painel desligado. Enviar as duas strings vazias remove a linha. Não há corpo de mensagem, CPF nem segredo na tabela.

| Método | Caminho | Quem | Corpo |
| --- | --- | --- | --- |
| GET | `/api/panel/embed` | admin, supervisor | — |
| PUT | `/api/panel/embed` | admin | `{allowed_origin, panel_url}` |
| GET | `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/embed` | quem pode ver a conversa | — |

PUT exige Origin do navegador na lista confiável do painel e CSRF. Campos desconhecidos, inclusive `body` e `cpf`, são recusados. 401 sessão ausente; 403 papel, CSRF ou Origin do navegador; 404 conversa fora do alcance; 422 origem, URL ou comando; 503 serviço sem banco. O erro não devolve a URL nem o host.

`Settings = {configured, version, allowed_origin, panel_url, embeddable}`. `embeddable` é falso quando a linha deixaria de passar no urlguard.

## postMessage

O iframe só é renderizado quando `embed` é verdadeiro, a URL é a mesma devolvida pelo servidor e a origem dela coincide com `origin`. Sandbox mínimo: `allow-scripts allow-forms`. Sem `allow-same-origin`, sem credencial na URL e com `referrerPolicy=no-referrer`. Se a página recusar incorporação, o atendente pode abri-la fora; o chat não contorna essa proteção.

O pai envia uma única mensagem, com `targetOrigin` igual à origem configurada. Mensagem vinda de outra origem ou de outra janela é ignorada. O iframe não recebe comando de volta nesta V1.

```json
{
  "type": "chatisp.embed.context",
  "v": 1,
  "conversation_id": "uuid",
  "protocol": "ATD-2026-1",
  "sector_id": "uuid",
  "inbox_id": "uuid"
}
```

Não há texto de mensagem, nota, CPF/CNPJ, telefone, e-mail, token, cookie ou segredo de ERP/WhatsApp. Protocolo vazio é permitido; protocolo com outro caractere não gera contexto e o iframe fica oculto. Identificador ausente também oculta o iframe.

## Provas

`go test ./internal/embed/` cobre origem divergente, URL interna/SSRF e payload sem corpo. A tela cobre a mesma regra no Vitest: origem errada não embute e o objeto publicado não carrega corpo.
