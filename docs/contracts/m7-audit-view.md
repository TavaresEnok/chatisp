# M7 — Leitura da auditoria do provedor

Contrato da leitura `GET /api/panel/audit`. A tabela `audit_logs` já existe.
Esta rota não grava evento, não lê `platform_audit_logs` e não entra no
OpenAPI gerado.

## Quem vê

Somente o administrador do provedor, no tenant da sessão. Supervisor e
atendente respondem 403. Superadmin também responde 403: a auditoria de
plataforma continua em outra trilha. Sessão ausente responde 401. Sem
serviço de sessão ou de banco, 503.

## Período e página

`from` e `to` são datas civis inclusivas no fuso do provedor. O intervalo
máximo é 366 dias. Data ausente, invertida ou cursor ilegível responde 422,
sem devolver o valor enviado.

A ordem é `created_at` decrescente e, no mesmo instante, `id` decrescente.
`limit` vale de 1 a 100; o padrão é 50. `next_cursor` pede a página seguinte
a partir da última linha já vista. Um evento mais novo não desloca essa
página. O cursor é opaco e não carrega ação, alvo nem texto.

## O que a resposta mostra

Cada evento tem só `created_at`, `action`, `target_type` e `result`. Não há
corpo de mensagem, CPF, correlação, ator, identificador do alvo nem segredo.
A tela fica em Configurações, só para o administrador, e lista o que a rota
devolver. Não há linha de exemplo.
