# M7 — Painel dos relatórios

Contrato da tela em Relatórios e do recorte ao vivo no Painel. O cálculo
continua em `GET /api/panel/reports`, descrito em `m7-reports.md`. Esta tela
não conclui o M7, não homologa o piloto e não cria métrica nova.

## Filtros

`from`, `to` e `group` seguem o período civil já aceito pela rota. `sector_id`
e `inbox_id` só são enviados quando a pessoa escolhe um item de
`GET /api/panel/sectors` ou `GET /api/panel/inboxes`. Não há campo para digitar
UUID. Setor com `active: false` fica de fora da lista. A caixa é a que a
listagem devolver, sem estimativa de tempo que a resposta da caixa não traz.

A chave da consulta leva tenant, sessão, usuário, vínculo, papel, modo
(`live` ou `full`), período, agrupamento, setor e caixa. A lista autorizada
usa a mesma identidade, sem o período. Outra sessão ou outro provedor não lê
o cache deste recorte. Trocar a sessão zera setor e caixa escolhidos. Depois
de um 403 da lista de setores ou de caixas, a chave do modo completo deixa
de apontar para qualquer cache anterior desse escopo.

## Troca, vazio e recusa

Enquanto o recorte atual não tem resposta nova bem-sucedida, a tela não
mostra o relatório anterior e não oferece o CSV dele. Um resultado que ainda
está em cache conta como anterior enquanto a atualização dessa seleção está
em andamento, inclusive antes de um eventual 403. Uma resposta atrasada de
um recorte abandonado não substitui o recorte atual. HTTP 403 tira da tela
os números, o mapa, o agora e o CSV daquele pedido.

Se a lista de setores ou de caixas responde 403, saem os nomes da lista, o
relatório, os números e o CSV que estavam na tela. A tela não troca sozinha
para um recorte amplo já em cache e não reaproveita esse resultado quando a
lista volta a responder. O relatório só reaparece com uma resposta nova e
bem-sucedida do recorte atual. O pedido seguinte não reenvia o identificador
da lista recusada. Outros erros ficam numa frase fixa, sem o corpo da
resposta.

O modo ao vivo conserva o último quadro já validado durante a atualização
dele.

Vazio é a resposta com conversas, ciclos e mensagens zerados, ou um mapa sem
células. Não há linha de exemplo.

## Mapa e volumes

O mapa tem sete dias e vinte e quatro horas. Dia e hora são os do cálculo no
fuso `timezone` da resposta; domingo é 0, como no servidor. O número da célula
é a contagem devolvida. Zero devolvido aparece como zero. Hora ausente na
resposta fica como sem registro e não vira zero. A legenda diz isso em texto.
A cor não é o único sinal.

Por setor: ciclos, esperas, mediana da espera e responsabilidade. Por
atendente: responsabilidade, mensagens e encerramentos. Por caixa: ciclos e
mensagens. Amostra, média, mediana e o que não se aplica continuam os do
cálculo. Não há taxa de transferência, de reabertura, reincidência, CSAT nem
construtor de indicadores.

## O que permanece

O Painel ao vivo continua sem esses filtros e sem CSV. As definições e o CSV
saem da mesma resposta. O botão baixa o campo `csv` da resposta bem-sucedida
do recorte que está na tela. As seis áreas e as permissões de cada uma não
mudam neste contrato.
