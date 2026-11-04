# M6 — contrato/fake do ERP e webhooks de saída

Entrega independente do adaptador real. Não há endpoint de fornecedor. Não compõe o roteador central, OpenAPI, schema TypeScript, `bothook`, mídia, iframe, API pública, auditoria visual, uso de plataforma, alertas operacionais nem relatórios. Não chama Meta, ERP nem Evolution.

O driver gravado só pode ser `fake`, com `simulated` verdadeiro. A conexão fica desligada até ser habilitada no teste. Um segundo adaptador no mesmo tenant conflita.

## Capacidades internas

`SearchCustomer`, `GetCustomerSummary`, `ListOpenInvoices`, `GetInvoiceDocument` e `GetPixCode`. Nome interno, sem path de fornecedor. Capacidade fora da conexão responde `capacidade indisponível`. Fatura, documento e Pix ficam `capacidade oculta` sem referência de verificação. O double, quando a capacidade está ligada e a verificação foi informada, devolve artefato fixo marcado como simulação. Não calcula valor, não gera boleto e não compensa pagamento.

A mesma referência opaca (`ref-casa`, `ref-comercio`) produz sempre o mesmo identificador. Telefone não escolhe vínculo: o atendente marca `selected`. `external_protocol` não substitui o protocolo local.

Chave local igual com o mesmo pedido devolve o resultado já gravado. Outro pedido na mesma chave conflita. Timeout ambíguo grava `uncertain` e não chama o adaptador de novo. Três falhas abrem o circuito da conexão por 30 segundos; a chamada seguinte não entra no adaptador. ERP indisponível devolve a lateral indisponível e `AcceptReply` segue.

Ação `ok` ou `uncertain` vai para `erp_actions`, para `audit_logs` (resultado e chave, sem detalhe financeiro) e, se houver conversa, para `conversation_events.kind = erp_action`.

## Webhooks

Eventos: `conversation.created`, `conversation.assigned`, `conversation.closed`, `message.created`. O texto da mensagem não entra no corpo.

O id é UUIDv5 de tenant, assinatura, tipo e origem. A retentativa reenvia o mesmo id e o mesmo corpo. HMAC-SHA256 do corpo cru vai em `X-ChatISP-Signature-256` (`sha256=` + hex). `X-ChatISP-Replay: event-id` orienta o receptor a aceitar o id uma vez. O corpo repete essa orientação em `replay`.

Cinco tentativas no máximo. 2xx entrega. 409, o receptor já guardou o id, encerra sem novo id. 408, 429, 5xx e erro de rede retentam com 1s, 2s, 4s, 8s e 16s. Outro 4xx encerra. Falha do destino não apaga a mensagem local.

O receptor fake responde 500 sem memorizar o id, aceita a retentativa e responde 409 na repetição seguinte, sem aplicar o efeito de novo. URL de loopback, metadado, rede privada ou com credencial é recusada pela política de saída antes do envio.

Segredo cifrado com AES-GCM, AAD do tenant, da assinatura e do campo `signing_secret`. A migração é `00031_m6_erp_webhook.sql`.
