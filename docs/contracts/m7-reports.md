# M7 — Relatórios do atendimento

Contrato da leitura `GET /api/panel/reports`. A tela e o CSV saem do mesmo
cálculo. Não há construtor de indicadores e a exportação não é o histórico
do ERP.

## Quem vê

Administrador e supervisor veem todos os setores. Atendente vê só os setores
em que está ativo. Pedir setor ou caixa fora desse conjunto responde 403.
Superadmin não usa esta rota.

## Período

`from` e `to` são datas civis inclusivas, interpretadas no fuso do provedor.
O intervalo máximo é 366 dias. Horários gravados em UTC. O agrupamento
`day`, `week` (semana começando na segunda) ou `month` usa esse fuso.

## O que entra na conta

- Ciclo: pelo início. Conversa: ciclos distintos iniciados no período.
  Mensagem: pública, de cliente, pessoa, bot ou menu, sem nota e sem
  histórico importado, pelo horário da mensagem.
- Primeira resposta humana: da primeira mensagem atual do cliente no ciclo
  até o envio público humano com estado aceito. Nota, falha, menu e bot
  ficam de fora. Mensagem da empresa antes do cliente não abre o relógio.
  Sem humano, o valor é nulo. A média e a mediana ignoram esses ciclos.
- Encerramento: do início ao fechamento local, só ciclos fechados no
  período. O operacional desconta uma vez a união de aguardando o cliente
  (`wait_started`/`wait_ended`) e adiado. Relógio corrido, inclusive fora
  do expediente.
- Espera na fila: intervalo de fila encerrado no período, duração integral.
- Setor e atendente: intervalos de responsabilidade e de fila, não o último
  nome da conversa. Mensagem pelo autor. Encerramento pelo ator.
- Motivo principal: motivo de encerramento ou, se não houver, motivo da
  triagem. Uma vez por ciclo. Etiquetas adicionais se sobrepõem.
- Encerrado pelo bot fica separado de solução confirmada por pessoa.
- O agora (fila, atendimento, aguardando, adiadas, sem responsável,
  atendentes com conexão viva, maior espera e alertas) não depende do
  período. O alerta reutiliza o limite já gravado da distribuição; prioridade
  urgente não dispara. Adiada não é um estado novo.

O CSV repete essas definições, inclusive a frase de que não é o histórico
completo do provedor.
