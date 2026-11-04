# ERP existente

A D2-01 continua sem fornecedor, versão, caminho de API e autenticação. Este arquivo não inventa endpoint de SGP, IXC nem de outro sistema.

O adaptador que o processo sobe é o fake do pacote `backend/internal/erp`, marcado como simulação. `SupplierEnabled` permanece falso. O contrato interno, o painel e o teste local estão em `docs/contracts/m6-erp-runtime.md`.

Nomes internos, que não são caminhos do fornecedor: SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument e GetPixCode. Capacidade ausente volta indisponível. O atendimento local segue.

O disjuntor é o do pacote, por conexão: três falhas abrem por 30 segundos, com relógio injetado no teste. O tempo limite padrão da chamada é 2 segundos e a concorrência do processo é 8. O módulo `sony/gobreaker` não está no `go.mod`. Handler e bot não chamam o adaptador direto; passam pelo serviço do painel.

Não houve homologação com ERP real.

## Decisão sobre o circuit breaker (04/11/2026)

A especificação cita `sony/gobreaker` (linha 763). O requisito 14-circuit-breaker é o comportamento: ERP lento ou fora não trava o chat. O pacote `backend/internal/erp/breaker.go` entrega isso por conexão, sem dependência nova: três falhas consecutivas abrem por 30 s, circuito aberto não chama o adaptador, uma única sondagem passa depois do prazo, sucesso fecha e uma sondagem que falha reabre na hora. Esse último ponto era a única diferença de comportamento em relação ao gobreaker e foi corrigido hoje; `breaker_test.go` cobre abertura, reabertura, fechamento e isolamento entre conexões. Timeout e concorrência ficam no serviço. Decisão: manter o código próprio, sem trocar de biblioteca por preferência. A especificação deve ser lida como "disjuntor equivalente ao gobreaker"; adotar o módulo exigiria vendorizar dependência nova e só se justifica se surgirem estados que o pacote não cobre (por exemplo, contagem por janela).
