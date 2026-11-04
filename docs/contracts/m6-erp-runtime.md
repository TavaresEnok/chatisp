# M6 — ERP simulado no runtime do painel

Contrato do fake já persistido em `00031`, agora ligado ao processo e às rotas chi do painel. Não há endpoint de fornecedor, cliente HTTP de ERP, nem conector real composto. `SupplierEnabled` permanece falso. O adaptador instalado no runtime é somente `fake`, marcado como simulação.

A migração nova é `00043_m6_erp_runtime.sql`: permissão explícita para leitura sensível e `DELETE` em `erp_links`. Não guarda valor, boleto nem segredo.

## Rotas

Todas exigem sessão do painel. Escrita exige `Origin` confiável e `X-CSRF-Token`. Corpo JSON até 8 KiB. Resposta de sucesso é `application/json`. Erro é `application/problem+json`.

| Método | Caminho | Uso |
| --- | --- | --- |
| GET | `/api/panel/conversations/{conversation_id}/erp/links` | Lista os vínculos do contato da conversa |
| POST | `/api/panel/conversations/{conversation_id}/erp/links` | Vincula o contato a um cliente/contrato opaco |
| DELETE | `/api/panel/conversations/{conversation_id}/erp/links/{link_id}` | Remove um vínculo |
| POST | `/api/panel/conversations/{conversation_id}/erp/identify` | Identifica cadastro por referência autorizada |
| GET | `/api/panel/conversations/{conversation_id}/erp/summary` | Resumo do vínculo escolhido |
| GET | `/api/panel/conversations/{conversation_id}/erp/invoices` | Faturas em aberto, sem valor |
| POST | `/api/panel/conversations/{conversation_id}/erp/second-copy` | Segunda via ou Pix, e texto na outbox |
| POST | `/api/panel/conversations/{conversation_id}/erp/protocol` | Associa protocolo externo sem trocar o local |

### Vínculo

```json
{"external_customer_id":"fake-customer-casa","external_contract_id":"fake-contract-casa","selected":true}
```

O mesmo contato aceita casa e comércio. Só um vínculo da conversa fica `selected`. Telefone (`+...` ou só dígitos) responde 422. Sem seleção única, resumo e faturas respondem 409 ou 404. `DELETE` remove só o vínculo da conversa da URL: o UUID de um vínculo de outra conversa do mesmo contato responde 404 e não altera o outro setor.

`external_contract_id` é opcional. Quando vem preenchido, resumo e faturas exigem o par do catálogo. `fake-customer-casa` com `fake-contract-comercio` responde `outcome=not_found`, sem `customer` e sem a fatura da casa. Sem contrato no vínculo, o cadastro do cliente continua valendo: `fake-customer-comercio` devolve o contrato e a fatura de comércio.

Vincular e desvincular tomam o advisory da conversa (`erp-conv`) antes do `FOR SHARE` da autorização, na mesma ordem do protocolo e da segunda via. A seleção ou a remoção não segura a conversa enquanto o protocolo ainda atualiza `erp_links`.

### Identificar

```json
{"reference":"ref-casa","local_key":"id-casa"}
```

`ref-casa` e `ref-comercio` são o catálogo fixo. A identificação não autentica o cliente e não escolhe o vínculo sozinha. A resposta guarda `customer` e `consulted_at`. A referência é normalizada (espaços nas pontas saem) antes do hash e da consulta. Repetir o mesmo JSON, inclusive com esses espaços, devolve `customer` e `consulted_at` com `replayed=true`, também num processo novo, sem chamar o fake de novo.

### Faturas

`GET .../invoices?verification=verificacao-1`

Sem `verification`, a capacidade sensível fica oculta (`outcome=hidden`) e o adaptador não é chamado. Com verificação e sem linha em `erp_capability_grants`, a rota responde 403 e grava auditoria `denied`. O grant é da membership atual e vale também no replay: revogar o grant, ou repetir a chave com outra membership do mesmo setor sem grant, responde 403, não devolve faturas e não chama o fake. Com `local_key`, o replay devolve a lista `invoices` gravada.

### Segunda via

```json
{
  "invoice_id":"fake-inv-casa-1",
  "verification":"verificacao-1",
  "local_key":"pix-1",
  "client_request_id":"req-pix-1",
  "artifact":"pix"
}
```

`artifact` é `pix` ou `document`. O texto entra em `messages` e `outbound_attempts` pela outbox já existente (`erp_second_copy`). O envio é proativo, finalidade `cobranca`: exige consentimento vigente e janela oficial aberta no driver `meta_cloud`. Sem isso a rota responde 409 e não enfileira.

A fatura tem de pertencer ao cliente e ao contrato selecionados na conversa. `fake-inv-comercio-1` com a casa selecionada responde 404, não enfileira e não devolve o Pix de comércio. Resumo e faturas aplicam a mesma relação quando o vínculo traz contrato: combinação fora do catálogo fica `not_found`, e a segunda via dessa fatura continua 404.

A mesma `local_key` com o mesmo `client_request_id` devolve o par original `message_id`/`attempt_id` e o `pix_code` ou `document` gravado, sem chamar o fake de novo. O par fica preso à idempotência `erp_second_copy` do principal que enfileirou. Outro `client_request_id`, outra fatura ou um id que já existe na conversa respondem 409 e não devolvem outra mensagem. O grant também é revalidado nesse replay. Não há valor monetário no corpo.

Pedidos sobrepostos com a mesma `local_key` esperam um ao outro. O perdedor do mesmo pedido recebe o resultado já gravado e, na segunda via, o par original da outbox: uma ação e uma mensagem. Outro pedido na mesma chave responde 409, com evento e auditoria de conflito. A segunda via adquire, antes do `FOR SHARE` da autorização, os locks de linha do channelpolicy na ordem do despacho: conector, contato e conversa. Protocolo, vínculo e desvínculo serializam pelo mesmo advisory da conversa antes desse `FOR SHARE`.

`local_key`, quando informada, tem de 1 a 200 caracteres depois do corte nas pontas. Vale para identificar, faturas e segunda via. `client_request_id` da segunda via tem o mesmo limite e não pode ser vazio. Acima do limite a rota responde 422, grava evento `invalid` e auditoria `conflict`, não chama o fake e não grava a chave rejeitada em `erp_actions` nem na correlação.

### Protocolo

```json
{"external_protocol":"ERP-99"}
```

A resposta traz `local_protocol` intacto e `external_protocol` como referência adicional. O protocolo externo tem de 1 a 128 caracteres, sem quebra de linha. Vazio ou maior que o limite responde 422 antes do SQL.

## Autorização

Papel `admin` ou `supervisor` alcança as conversas do tenant. `agent` só alcança conversa do próprio setor (`sector_memberships`) e só quando a conexão fake está presa a esse setor. Outro setor, ou conexão sem setor, recebe 403, com evento na linha do tempo e linha em `audit_logs`. Essa checagem é a `Authorize` do contrato e roda de novo antes de qualquer replay, inclusive num processo novo: a chave gravada não devolve `customer`, faturas, Pix nem documento e o fake não é chamado. O setor da conexão, quando existe, ainda precisa ser o setor da conversa. Outro tenant não lê os vínculos: RLS em `erp_links`, `erp_actions`, `erp_connections` e `erp_capability_grants`.

Ação sensível (`ListOpenInvoices`, `GetInvoiceDocument`, `GetPixCode`) exige grant explícito da membership atual, além da verificação, em toda chamada, inclusive no replay da mesma chave.

Cada ação e cada falha autenticada grava `conversation_events.kind` no formato `erp_action:<capacidade>:<resultado>:<motivo>`. A história da conversa já devolve esse `kind`, então o motivo sai na linha do tempo sem coluna extra. Motivos estáveis: `ok`, `sent`, `linked`, `unlinked`, `protocol`, `timeout`, `circuit_open`, `down`, `grant`, `sector`, `choice`, `missing`, `local_key`, `outbox`, `invoice`, `not_found`, `hidden`, `unavailable`, `uncertain`, `dispatch`, `invalid`. Resultado `ok` ou `uncertain` também fica em `erp_actions`, com o JSON do resultado permitido (`customer`, `invoices`, `pix_code`, `document`, `consulted_at`) e, na segunda via, o par da outbox. A auditoria usa `result=denied` na recusa e `result=unavailable:timeout` ou `unavailable:circuit_open` quando o motivo é tempo ou circuito. Conflito de chave fica `result=conflict`. Uma falha não confirma vínculo, ação nem mensagem parcial: o registro ocorre depois de desfazer a tentativa.

Validação de quem já passou da autorização da conversa também é falha registrada. Referência inválida ou telefone no vínculo e na identificação, artefato fora de `pix`/`document`, fatura inválida, `client_request_id` ausente ou acima de 200, `local_key` acima de 200 e protocolo vazio ou acima de 128 respondem 422. O evento usa motivo `invalid` e a auditoria `result=conflict`. A correlação guarda no máximo a `local_key` já aceita; não guarda telefone, referência rejeitada, chave acima do limite nem o protocolo inválido. Não nasce vínculo, ação, mensagem nem protocolo parcial.

## Limites do conector

- Um registro `erp_connections` por tenant, `driver=fake`, `simulated=true`.
- Timeout padrão de cada chamada ao fake: 2s. O contexto cancela a espera.
- Concorrência padrão do processo: 8 chamadas ao adaptador.
- Circuito por conexão: 3 falhas abrem por 30s. Com o circuito aberto o fake não é chamado e o painel responde 503 `circuito do erp aberto`. O tempo esgotado responde 503 `tempo do erp esgotado`. Os dois ficam distintos na linha do tempo e na auditoria.
- Métrica `chatisp_erp_connector_calls_total{connector,result}` no registro Prometheus já usado por `/metrics`. `connector` só aceita `fake` ou `other`.
- Capacidade ausente responde `outcome=unavailable` sem derrubar o atendimento local. `AcceptReply` continua independente destas rotas.
