# M7 — Demandas, retenção e limites

Este contrato cobre o que ainda cabia no M7 sem ligar expurgo e sem tratar a exportação como histórico do ERP. O relatório em `GET /api/panel/reports` permanece o cálculo já gravado.

As rotas abaixo ficam fora de `backend/api/openapi.json` de propósito. O processo as registra no roteador do painel.

## Exportação de demandas

`GET /api/panel/contacts/{contact_id}/demands`

`format=json` é o padrão. `format=csv` repete os mesmos campos.

Cada ciclo traz protocolo, abertura, encerramento, motivo, síntese, encaminhamento e autoria. O motivo é o do encerramento ou, se o ciclo ainda não fechou, o motivo registrado na triagem. O encaminhamento guarda a referência externa e o protocolo externo quando existem. A autoria é `pessoa` ou `bot` com o vínculo de quem encerrou.

O campo `label` e a primeira linha do CSV dizem: histórico estruturado de demandas do atendimento. Não é o histórico completo do provedor nem do ERP. O arquivo não leva corpo de mensagem, nota, anexo nem fluxo de gestão do ERP.

Administrador e supervisor veem os ciclos do contato no provedor. Atendente vê só os setores em que está ativo; ciclo de outro setor responde 403. Superadmin não usa a rota. Provedor suspenso: o administrador ainda exporta; supervisor e atendente não.

A exportação grava `demand.export` na auditoria do provedor, sem o conteúdo da conversa.

## Retenção por classe

`GET /api/panel/retention` e `PUT /api/panel/retention` são do administrador. O supervisor lê. O atendente recebe 403.

A política guarda prazos separados para conteúdo, mídia, envelope, log e job, além de um nome curto do provedor. Prazo vazio significa que ainda não foi definido. O teto de 36500 dias é limite do campo, não um prazo legal. D2-06 continua aberta: este software não declara conformidade.

`automatic_purge` só aceita falso. A restrição no banco recusa `true`. `POST /api/panel/retention/purge` grava uma tarefa `refused` por classe, responde 409 e não apaga mensagem, anexo, conversa nem ciclo. Não há agendamento. `retention_deletion_records` é a trilha por classe do provedor. A tabela `deletion_records` da migração 00040 continua sendo a que o ensaio de restore reaplica (`attachment` ou `credential_secret`). O expurgo desligado não escreve em nenhuma das duas.

## Limites do plano

`GET /api/panel/plan-limits` lê `tenants.limits`: `max_agents`, `max_numbers` e `max_storage_bytes`. Campo ausente não limita. A contagem não apaga histórico.

O limite de atendentes continua no convite e na reativação já existentes. Número novo acima de `max_numbers` é recusado na inclusão da caixa. Upload de saída acima de `max_storage_bytes` é recusado; mídia de entrada ainda cabe na reserva `attachment_reserve_bytes`, quando ela estiver gravada. O anexo ou a caixa que já existia permanece.

## Fora deste lote

Relatórios e CSV do atendimento. OpenAPI gerado, `schema.ts` e `docs/continuidade-nexoisp.md`. Expurgo automático ligado, exclusão de contato e prazo regulatório. Marca branca.
