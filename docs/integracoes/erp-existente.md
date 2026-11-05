# ERP existente

A D2-01 continua sem fornecedor, versão, caminho de API e autenticação. Este arquivo não inventa endpoint de SGP, IXC nem de outro sistema.

O adaptador que o processo sobe é o fake do pacote `backend/internal/erp`, marcado como simulação. `SupplierEnabled` permanece falso. O contrato interno, o painel e o teste local estão em `docs/contracts/m6-erp-runtime.md`.

Nomes internos, que não são caminhos do fornecedor: SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument e GetPixCode. Capacidade ausente volta indisponível. O atendimento local segue.

O disjuntor é o do pacote, por conexão: três falhas abrem por 30 segundos, com relógio injetado no teste. O tempo limite padrão da chamada é 2 segundos e a concorrência do processo é 8. O módulo `sony/gobreaker` não está no `go.mod`. Handler e bot não chamam o adaptador direto; passam pelo serviço do painel.

Não houve homologação com ERP real.

## Decisão sobre o circuit breaker (04/11/2026)

A especificação cita `sony/gobreaker` (linha 763). O requisito 14-circuit-breaker é o comportamento: ERP lento ou fora não trava o chat. O pacote `backend/internal/erp/breaker.go` entrega isso por conexão, sem dependência nova: três falhas consecutivas abrem por 30 s, circuito aberto não chama o adaptador, uma única sondagem passa depois do prazo, sucesso fecha e uma sondagem que falha reabre na hora. Esse último ponto era a única diferença de comportamento em relação ao gobreaker e foi corrigido hoje; `breaker_test.go` cobre abertura, reabertura, fechamento e isolamento entre conexões. Timeout e concorrência ficam no serviço. Decisão: manter o código próprio, sem trocar de biblioteca por preferência. A especificação deve ser lida como "disjuntor equivalente ao gobreaker"; adotar o módulo exigiria vendorizar dependência nova e só se justifica se surgirem estados que o pacote não cobre (por exemplo, contagem por janela).

## Abrir cadastro no ERP (08-abrir-cadastro, 05/11/2026)

O administrador grava em Configurações → Integrações → Conector ERP o modelo do link para a tela de cliente do ERP que o provedor já usa, com `{cliente}` no lugar da referência externa (`PUT /api/panel/erp/customer-link`, controle de versão; tabela `erp_customer_link_settings`, migração `00050`, RLS por tenant). Regras (`internal/erp/customerlink.go`):

- só HTTPS, com host, sem usuário/senha na URL, sem fragmento e com exatamente um `{cliente}`, que não pode ficar no host;
- query ou caminho com nome de credencial ou documento (`token`, `senha`, `secret`, `api_key`, `cpf`, `cnpj` etc.) é recusado;
- a referência é escapada (caminho ou query) e uma referência que pareça CPF (11 dígitos) ou CNPJ (14) não vira link.

O painel do ERP mostra "Abrir cadastro no ERP" em cada vínculo, com `target=_blank`, `rel="noopener noreferrer"` e sem referrer. Nada disso conhece a API do fornecedor: falta só o endereço real do provedor (D2-01) para homologar.
