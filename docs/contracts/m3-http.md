# Contratos HTTP M3A

As operações usam o cookie `__Host-session`; alterações exigem `Origin` confiável e `X-CSRF-Token`. Erros são `application/problem+json`: 401 para sessão inválida, 403 para autorização ou CSRF, 404 para recurso não visível, 409 para conflito, 422 para dados inválidos e 503 quando o serviço está indisponível. O OpenAPI gerado é a fonte dos schemas e status por operação.

## Setores

`GET /api/panel/sectors` retorna `{sectors: [...]}`. `GET /api/panel/sectors/{sector_id}` retorna arrays `membership_ids`, `inbox_ids`, `default_inbox_ids`. Setores têm `id`, `name`, `active`, `legacy_default`. `POST /api/panel/sectors` recebe `{name}` e retorna 201; `PATCH /api/panel/sectors/{sector_id}` recebe `name` e/ou `active`. `PUT` e `DELETE` em `/{sector_id}/memberships/{target_id}` e `/{sector_id}/inboxes/{target_id}` vinculam e desvinculam; `PUT /{sector_id}/inboxes/{target_id}/default` define o setor padrão da caixa. Escritas são administrativas; leitura segue a matriz operacional.

## Contatos

`GET /api/panel/contacts` aceita `q`, `order` (`name` ou `recent`), `offset`, `limit` e retorna `{items,next_offset}`. `GET /api/panel/contacts/{contact_id}` retorna o contato; `GET /api/panel/contacts/{contact_id}/history` aceita paginação e retorna `{items,next_offset}`. Cada conversa histórica tem `id`, `inbox_id`, `protocol`, `status`, `created_at`, `updated_at` e é filtrada pela autorização do setor. Contatos têm `id`, `name`, `phone`, `email`, `notes`, `blocked`, `block_reason`, `avatar_reference`, `created_at`, `updated_at`, `last_contact`. `POST /api/panel/contacts` recebe `name`, `phone`, `email`, `notes` e retorna 201; `PUT /api/panel/contacts/{contact_id}` atualiza esses campos; `PUT /api/panel/contacts/{contact_id}/block` recebe `blocked` e `reason`. Itens ingressados de contato bloqueado ficam em quarentena; tentativas humanas pendentes são finalizadas sem despacho.

## Notificações e eventos

`GET /api/panel/preferences/notifications` retorna `sound`, `browser`, `silent`; `PATCH` no mesmo caminho aceita subconjunto desses booleanos e retorna as preferências persistidas da própria associação. Sem configuração anterior o padrão é `sound: true`, `browser: false`, `silent: false`.

O realtime existente publica `conversation.opened`, `conversation.assumed`, `conversation.closed`, `conversation.waiting`, `conversation.external_protocol`, `conversation.message`, `message.attempt` e `message.status`. Não há evento específico de alteração de contato ou preferências neste contrato; após uma alteração, atualize a consulta correspondente.
