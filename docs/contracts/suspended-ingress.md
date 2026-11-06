# Entrada durante suspensão (V1 parametrizada)

`pipeline.SuspendedIngressConfig`, ligado ao webhook real e ao Compose, controla a retenção por provedor. Não há limite comercial implícito. Variáveis:

- `CHATISP_SUSPENDED_INGRESS_ENABLED=false` por padrão: novas entregas de um provedor suspenso são recusadas, com resposta diferente de 200 e alerta persistido. Dados recusados não são persistidos.
- Se habilitada, `CHATISP_SUSPENDED_INGRESS_MAX_DELIVERIES` e `CHATISP_SUSPENDED_INGRESS_MAX_BYTES` devem ser inteiros positivos. A aplicação recusa configuração incompleta.
- O limite conta envelopes distintos por provedor e soma os bytes dos corpos originais. Índices, trilha e cópia segregada em quarentena consomem espaço adicional; esse limite não representa o tamanho físico total do banco.

A admissão e a alteração de estado do provedor usam a mesma trava transacional. Entregas concorrentes não excedem o limite. Replay de corpo já persistido não consome outra reserva. A associação do provedor vem do registro App/WABA/número; um tenant informado pelo remetente não é aceito.

Um envelope com vários provedores é confirmado atomicamente. Se alguma parte suspensa não couber, o envelope inteiro é recusado para não confirmar a perda dessa parte. A Meta pode tentar novamente; a deduplicação protege entregas já aceitas. Provedores ativos em envelopes independentes continuam sendo admitidos.

Entregas aceitas na suspensão geram `suspended_ingress_holds`, emitem sinal persistido em `suspended_ingress_alerts` e viram itens segregados em quarentena. Não geram inbound, bot, distribuição ou envio. A reativação não libera esses itens automaticamente, mesmo se ocorrer antes de o worker expandir o envelope. O painel de alertas mostra a necessidade de conciliação e a recusa por limite.

A política de admissão ao esgotar é **recusar sem 200**, mantendo os dados anteriores. Não há descarte automático nem promessa de armazenamento ilimitado. As reservas permanecem até conciliação administrativa: inventariar itens retidos, conferir a vinculação do canal e as datas, decidir quais dados serão incorporados sem executar ações vencidas, registrar o resultado e só então liberar as reservas. Não excluir linhas do livro como forma de reprocessar mensagens automaticamente. A frequência, os números e eventual procedimento de desligar o canal pertencem à D2-08; a implementação não escolhe esses valores.

Verificação local: `go test ./internal/attendance/pipeline -run TestSuspendedIngress -count=1` usa HTTP assinado, PostgreSQL e River reais de teste: desligado, limite de bytes/entregas, replay, concorrência, isolamento, alerta no painel, reativação antes da expansão e ausência de job inbound.
