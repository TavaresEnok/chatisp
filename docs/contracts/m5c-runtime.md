# M5C — composição do runtime

Base preservada: `d7178d5`. Serviços `lifecycle.New(store)` e `triage.New(store)`
percorrem `runtimeServices → runtime.Options → httpapi.Dependencies`. As rotas
publicadas são as dos contratos M5C existentes. O gerador central usa prefixos
`Lifecycle` e `Triage`, evitando colisões entre tipos de módulos.

## Chegada e triagem

`CHATISP_TRIAGE_INBOXES` é uma lista de UUIDs de caixas, separada por vírgulas.
Ausente/vazia mantém a triagem desligada. UUID inválido falha na configuração
sem ecoar o valor. Configurar o menu pela API não liga a flag implicitamente.
É configuração de processo: aplicar a mesma lista em todas as instâncias de
workers. Alterar exige reiniciar esses processos; esta entrega não os reinicia.

`NewWorkers` injeta `Automation` no inbound. Uma mensagem atual retira o
adiamento da conversa existente na própria transação da mensagem. O gancho
roda antes da publicação do cursor e ignora mensagem já deduplicada. Não abre
outra conversa para retirar o adiamento.

Depois do commit de inbound, `ApplyTriage` resolve a mensagem persistida e
chama `triage.Decide` sob transação tenant. Essa fronteira é deliberada: o
aceite durável não fica preso às consultas de configuração/presença do menu,
e nenhum lock novo de outro agregado é tomado depois do cursor de inbound.
O retry de um item `processed` também executa essa composição. Decisão,
claim de efeitos, rota, motivo e outbox confirmam juntos; falha faz rollback
somente dessa composição e River tenta novamente. Mensagem histórica ou
anterior à última mensagem atual não reinicia triagem.

Migração `00028`: `automation_dispatches`, com FORCE RLS, FKs tenant e chave
única por conversa/origem/índice. A linha de índice -1 é o claim; seu `reason`
guarda o motivo escolhido, separado de resultado de encerramento. Não existe
agente humano artificial. Texto numerado usa o fallback declarado pelo módulo.
As mensagens têm origem `menu`; avisos têm origem `technical`. Não passam por
assinatura humana nem iniciam espera/primeira resposta humana.

A rota usa o setor validado por `Decide`; a saída humana abre a fila mantendo
o controle de menu até a tomada humana. Uma decisão nova cancela saídas de
menu ainda `queued` de entradas anteriores; envio em voo não é apresentado
como cancelável. Atribuição automática continua no wrapper existente,
posterior à triagem e condicionada à flag explícita do setor.

Desligar uma caixa impede despacho de suas saídas de triagem na fronteira
final. O tick libera conversas `triage/menu` para `open` no setor já gravado,
mesmo sem nova mensagem. Não muda conversas que já estão sob controle humano.

## Jobs e aviso

River registra `chatisp.automation.tick.v1`, fila `scheduled`, um worker,
intervalo de 10 segundos e execução ao iniciar o scheduler. O job não contém
texto/documento. Usa o catálogo de tenants via papel platform e abre transações
app com tenant explícito; tenants suspensos não são processados. A execução
reúne erros por tenant e preserva retry limitado do River existente.

Cada retorno de adiamento usa a drenagem já existente, uma conversa por
transação. `RunInactivity` bloqueia todos os candidatos em ordem antes do
primeiro fechamento/publicação, evitando obter outra conversa depois de
reter o cursor do tenant. A consulta conserva o marcador usado pelas provas
de concorrência do módulo.

Pedido de aviso é ligado a uma única tentativa/outbox na mesma transação,
por conversa e geração. O dispatcher reaplica janela, canal, bloqueio do
contato, época e lease existentes. Na fronteira final também exige o mesmo
marcador/geração/âncora/responsável ativo, espera humana, ausência de adiamento
e de mensagem posterior. Aviso obsoleto não chega ao driver.

O tick concilia a tentativa: `accepted` do driver é confirmação de aceitação,
não afirma entrega/lida remota; `failed/cancelled/uncertain` não arma fechamento.
`ConfirmWarning` revalida e só então arma o prazo. Ausência de evidência mantém
pedido pendente; não há reenvio cego de `uncertain`. Fora da janela, o envio
livre falha pela política existente e o encerramento fica sem armar.

## Limites e provas

Provas novas com PG: flag padrão desligada, triagem/menu pela outbox, retry
sem duplicação, rota humana, aviso idempotente, pendente sem fechamento,
aceitação pelo fake seguida de fechamento e resposta nova antes do despacho
sem chamada ao driver. Prova adicional cobre mensagem retirando adiamento
na composição real. As suítes existentes de triagem/lifecycle continuam
cobrindo horários, plantão, capacidade, posse, sessão, CSRF, papéis e RLS.

Não compõe bot/IA externa, mídia/áudio, ERP, reabertura automática X nem tela de
configuração do menu/adiamento. Não homologa Meta ou aceitação de operador.
