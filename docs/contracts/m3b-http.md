# Contrato HTTP M3B

Todas as rotas usam o cookie `__Host-session`. Mutações exigem `Origin` confiável e `X-CSRF-Token`; a sessão deve ser operacional, com tenant e vínculo ativos. A autorização de caixa e setor é verificada em cada acesso. Erros seguem o formato Problem Details (RFC 9457): 401 para sessão inválida, 403 para acesso ou origem negados, 404 para registro não encontrado em operações que expõem esse resultado, 409 para conflito de versão, capacidade ou ressincronização, 422 para dados inválidos e 503 para dependência indisponível.

Sob `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}`:

- `POST /transfer-member`, `/transfer-sector` e `/unassign` recebem `expected_version`, `client_request_id` e `target_id` (este último ausente na devolução). Respondem com a conversa. `GET /assignment-candidates` retorna `candidates` com `membership_id`, `capacity` e `assigned`.
- `GET /annotations` retorna prioridade, versão, tags, notas, eventos e leitura pessoal (`watermark`, `read_version`, `unread`, `unread_count`). `POST /annotations` recebe `kind`, `subject_id`, `value`, `request_id`; os comandos incluem `note`, `tag_add`, `tag_remove` e `priority` (`normal` ou `urgent`). `POST /annotations/read` recebe `watermark`, `unread`, `read_version` e atualiza somente a leitura do usuário autenticado.
- `GET /collision` retorna `indicators` e `ttl_seconds`; `POST /collision` recebe `tab_id` e `typing`, e `DELETE /collision/{tab_id}` libera o indicador. A presença expira e não atribui atendimento nem impede respostas.

`POST /api/panel/memberships/{membership_id}/assignment-capacity` recebe `capacity`. O catálogo de tags fica em `GET` e `POST /api/panel/annotation-tags` e `PATCH` e `DELETE /api/panel/annotation-tags/{tag_id}`; criação e alteração recebem `name`. A exclusão não exige corpo.

`GET /api/panel/queue` aceita `tab` (`mine`, `unassigned`, `all`), `order` (`wait`, `lastMessage`), `status`, `sector`, `assignee`, `inbox`, `tag`, `from`, `to`, `search`, `limit` e `page_cursor`. As datas usam `YYYY-MM-DD`, com o último dia inclusivo. A resposta contém `items`, `counts`, `page_cursor` e `event_next_cursor`; cada item inclui setor, responsável, prioridade e leitura pessoal. Cursores de paginação e eventos têm finalidades distintas.

Os eventos `conversation.transferred_member`, `conversation.transferred_sector`, `conversation.unassigned` e `conversation.annotation` carregam identificadores, sem corpo de nota. Após transferência de setor, acessos e eventos pendentes voltam a ser autorizados; clientes devem respeitar ressincronização ou fechamento da conexão. Isso não apaga dados já visualizados. O contrato completo de campos e parâmetros está em `backend/api/openapi.json`.
