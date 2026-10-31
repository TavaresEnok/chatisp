# Indicadores consultivos de conversa

As rotas exigem cookie `__Host-session` e acesso à conversa no setor. Escritas exigem também `Origin` confiável e `X-CSRF-Token`. Todas são relativas a `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision`.

* `POST /` (sem barra final): `{"tab_id":"UUID","typing":true}` renova visualização e estado de digitação da aba; responde 204. Cada aba deve manter um UUID aleatório próprio, por sessão e conversa. Não enviar texto digitado.
* `DELETE /{tab_id}` libera somente a aba da sessão autenticada; responde 204.
* `GET /` (sem barra final): retorna `{"indicators":[{"membership_id":"UUID","typing":false,"expires_at":"..."}],"ttl_seconds":30}`. Cada vínculo aparece uma vez, com digitação agregada entre sessões e abas válidas. Identidade é o ID do vínculo autorizado, sem consultar usuários globais para rótulos.

O servidor fixa TTL em 30 segundos; o painel deve renovar apenas a conversa ativa e focada e parar ao perdê-la. O estado expira mesmo sem release. Cada operação remove até 100 registros expirados do tenant, sem cron; registros restantes nunca são listados. Sessões encerradas ou expiradas e vínculos sem acesso deixam de ser listados imediatamente. Esses sinais não controlam envio, titularidade ou disponibilidade.
