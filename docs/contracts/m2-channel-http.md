# API HTTP de canais Meta

O pacote `channel/httpapi` registra operações Huma com `Register(api, Dependencies{Auth: authService, Channels: channelService, TrustedOrigins: origins})`. O router existente deve importar esse pacote e chamá-lo após criar a API Huma; o pacote não importa `platform/httpapi` e não cria router ou worker. Dependências nulas preservam operações no OpenAPI e respondem 503.

Todas as rotas exigem cookie `__Host-session`, sessão ativa de administrador do tenant e MFA completo. Mutações exigem `X-CSRF-Token` e `Origin` igual a uma origem confiável configurada. Superadministradores não têm acesso operacional. Não se aceita tenant no corpo.

- `GET /api/panel/channels/meta`: `boxes` e `alerts` do tenant.
- `POST /api/panel/channels/meta`: corpo `inbox_name`, `display_number`, `app_id`, `app_secret`, `waba_id`, `phone_number_id`, `access_token`; retorna a caixa pública com 200. Segredos são somente de escrita.
- `GET /api/panel/channels/meta/inspection`: referências `quarantine`, `items` e `discarded`, limitadas pelo serviço; sem envelopes ou argumentos de jobs.
- `POST /api/panel/channels/meta/alerts/{alert_id}/ack`: reconhece alerta aberto e retorna 204.

Caixas incluem IDs, estado, versão e versões públicas de chave, sem ciphertext ou credenciais. Falhas são problemas RFC 9457 sanitizados. Huma gera o contrato OpenAPI e os tipos de cliente na integração global.
