# Cache do processo (14-cache)

Cache em processo com `github.com/dgraph-io/ristretto` v0.2.0, no pacote `backend/internal/platform/tenantcache`. Não é Redis. Não guarda mensagem nem conversa. Não é a autoridade de envio, de despacho nem de permissão.

A chave é `tenant`, tipo e id. Os tipos aceitos são `config`, `inbox`, `template` e `permission`. Qualquer outro tipo, inclusive `message` e `conversation`, é recusado. O TTL de configuração, caixa e catálogo é 30 segundos. O de permissão é 15 segundos. A escrita no mesmo processo invalida a chave.

| Tipo | O que entra | O que não entra |
| --- | --- | --- |
| `config` | Identidade do provedor em `branding.Get`: nome, tipo, largura e altura. Sem os bytes do logo | Documento, cifra, prazo e `automatic_purge` |
| `inbox` | Metadado de horário da caixa em `businesshours.Get`, depois da autorização da caixa | A decisão de expediente. `EvaluateTx` continua lendo o banco |
| `template` | Lista do catálogo em `channelpolicy.ListTemplates`, depois de resolver a WABA | O `FOR SHARE` do despacho. Sincronizar o catálogo invalida a chave |
| `permission` | Papel recém-lido, com versão 1 admin, 2 supervisor, 3 agente | A decisão. Versão diferente derruba a entrada. Escrita continua revalidando no banco |

Sem banco, a leitura de caixa e de catálogo não devolve o valor que já esteja no cache. Outro tenant não lê a chave do primeiro. Valor acima de 64 KiB não entra.

O processo cria um cache quando há banco de aplicação e o fecha no encerramento. Testes que não preenchem o campo seguem direto ao banco.
