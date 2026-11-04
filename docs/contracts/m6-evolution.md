# Evolution — contrato local com a flag desligada

Requisito `01-evolution`. Pacote `backend/internal/channel/whatsapp/evolution`. Não há release, licença, endpoint, Baileys nem chamada de rede. `Enabled()` não tem como ser ligado neste código. A D2-04 continua em aberto.

## O que o fake faz

| Ação | Resultado |
| --- | --- |
| Enviar ou conectar | `conector evolution desligado`. Nada é registrado |
| QR para atendente | Recusado |
| QR para admin | Recusado enquanto a flag está desligada. Não há token |
| Guardar QR | Recusado. O campo interno permanece vazio |
| Queda de uma conexão | Só essa conexão muda. A outra permanece como estava |

## Eventos

`Classify` não persiste e não devolve o texto do evento.

| Classe | Aceita | Automação |
| --- | --- | --- |
| `inbound` | sim | sim |
| `echo` | sim, marcado como duplicata | não |
| `external_device` | sim, autoria «dispositivo externo», revisão humana | não |
| `history`, `edit`, `revoke` | sim | não |
| `unsupported` | não | não |
| grupo ou classe desconhecida | erro | não |

A caixa oficial não passa por este pacote. O driver de simulação de texto em `whatsapp.Fake` continua recusando `evolution_web` antes de capturar o pedido.

O painel de integrações mostra a modalidade desligada e não desenha QR. Isso não é homologação.
