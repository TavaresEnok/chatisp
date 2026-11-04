# M7 — painel de uso do dono do SaaS

Entrega de 09-painel-saas e 12-superadmin. O superadmin continua no fluxo já existente de provedores (`/configuracoes/provedores` e a seção em Configurações). A rota nova fica fora do OpenAPI commitado e do schema TypeScript gerado.

## O que a tela mostra

A seção "Uso da plataforma" pede início e fim do período, sem valor preenchido. A consulta envia o início do dia em America/Sao_Paulo (`-03:00`) e o início do dia seguinte ao fim escolhido, de modo que o dia final entra no intervalo. A resposta lista:

- provedores ativos e provedores suspensos
- por provedor: fuso, situação, atendentes ativos, caixas, conversas abertas (`status = open`), mensagens no período e bytes de anexo

Bytes de anexo somam `expected_bytes`. Se a tabela `attachments` não existir, o campo vem nulo e a tela diz que está indisponível. Não há corpo de mensagem, protocolo, contato, CPF nem nome de anexo.

## API

`GET /api/platform/usage?from=&to=`

`from` e `to` são RFC3339, com `to` exclusivo, intervalo não vazio e de no máximo 366 dias. Sessão de superadmin com cadastro de MFA já concluído. Admin, supervisor e atendente do provedor recebem 403. Sem sessão, 401. Período inválido, 422. Loja ausente, 503.

Cada leitura bem-sucedida grava `platform_audit_logs` na mesma transação: ação `platform.usage.read`, alvo `platform_usage`, resultado `success`. O alvo guarda só o intervalo. Papel de provedor não gera essa auditoria.

## Leitura

Migração `00035_platform_usage.sql`. O papel `chatisp_platform` ganha `SELECT` só de `inboxes.tenant_id`, `conversations.tenant_id/status`, `messages.tenant_id/ingested_at` e `attachments.tenant_id/expected_bytes`, com política quando `app.access_scope = platform`. Corpo, protocolo, resumo, contato e nome de anexo continuam sem privilégio. Provedores e vínculos já eram legíveis por esse papel; a consulta de vínculos só conta `role = agent` e `status = active`.
