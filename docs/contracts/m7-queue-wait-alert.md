# M7 — Alerta de espera na fila do atendente

`GET /api/panel/queue` passa a incluir `wait_alert` em cada item. Os outros campos
mantêm o mesmo significado. Não há migração e a consulta não atribui nem
reatribui conversa.

## Limite

O sinal lê `distribution_settings.wait_alert_seconds` do tenant. Sem linha, vale
os 900 segundos já definidos pela distribuição. Não existe outro prazo.

`wait_alert` é verdadeiro só quando há intervalo de fila aberto, a prioridade é
`normal` e `started_at` é anterior ou igual a agora menos o limite. Urgente não
entra. Conversa atribuída, em triagem ou encerrada não tem intervalo aberto e
permanece falsa. O campo fica de fora da impressão do cursor de página.

## Fila visível

A lista da página de conversas destaca o item com `wait_alert` verdadeiro, com
fundo de alerta e o texto "Passou do tempo de espera." Se o campo não vier, a
tela não calcula espera a partir de `wait_since` e não destaca ninguém.
