# ERP existente

A D2-01 continua sem fornecedor, versão, caminho de API e autenticação. Este arquivo não inventa endpoint de SGP, IXC nem de outro sistema.

O adaptador que o processo sobe é o fake do pacote `backend/internal/erp`, marcado como simulação. `SupplierEnabled` permanece falso. O contrato interno, o painel e o teste local estão em `docs/contracts/m6-erp-runtime.md`.

Nomes internos, que não são caminhos do fornecedor: SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument e GetPixCode. Capacidade ausente volta indisponível. O atendimento local segue.

O disjuntor é o do pacote, por conexão: três falhas abrem por 30 segundos, com relógio injetado no teste. O tempo limite padrão da chamada é 2 segundos e a concorrência do processo é 8. O módulo `sony/gobreaker` não está no `go.mod`. Handler e bot não chamam o adaptador direto; passam pelo serviço do painel.

Não houve homologação com ERP real.
