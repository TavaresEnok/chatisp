# M6 — API pública e webhooks de saída

Este contrato cobre os itens 11-api-conversas, 11-api-contatos, 11-api-template, 11-tokens, 11-rate-limit, 11-openapi, 11-webhook-conversa, 11-webhook-mensagem e 11-api-cadastros. A leitura antiga em `m6-public-read.md` continua válida para `public_read_tokens`. Este documento acrescenta tokens com escopo, escrita limitada e a entrega de webhooks. Não há chamada real a Meta, ERP ou Evolution.

## Eventos

A migração `00044_m6_public_api.sql` grava a origem dentro da mesma transação do atendimento:

| Evento | Quando | `source_id` |
| --- | --- | --- |
| `conversation.created` | insert da conversa | id da conversa |
| `conversation.assigned` | `assignee_id` passa a um valor novo | id desta ocorrência |
| `conversation.closed` | status passa a `closed` | id desta ocorrência |
| `message.created` | insert da mensagem | id da mensagem |

Cada mudança real de atendente gera um `conversation.assigned`. Cada novo ciclo que chega a `closed` gera um `conversation.closed`. O `source_id` identifica essa ocorrência e permanece o mesmo nas novas tentativas da mesma entrega. O envelope traz `conversation_id` com a conversa. Repetir a atribuição ou o encerramento sem nova mudança não gera outro evento nem outro job. Transferir para outro atendente, reabrir e encerrar de novo geram eventos novos.

O corpo assinado não inclui texto de mensagem. O worker da fila `webhooks` (`chatisp.webhook.deliver.v1`) está registrado no mesmo cliente River de `ingress`, `inbound`, `outbound` e do job periódico `lifecycle`. Esse cliente é o único que disputa liderança no processo. A manutenção desta versão do River resgata jobs em execução de todas as filas e descarta o tipo que o líder não conhece; um segundo cliente só com webhooks descartaria as outras filas quando liderasse e, enquanto liderasse, deixaria de enfileirar o lifecycle. Dois processos worker usam o mesmo registro, então qualquer um pode assumir a liderança. O worker materializa a entrega, assina com `X-ChatISP-Signature-256` e repete com espera de 1, 2, 4, 8 e 16 segundos, contada a partir do fim da tentativa. Um 503 lento ou um timeout do cliente que dure mais que essa espera deixa a entrega `pending` e o job adiado; o worker não confirma o job enquanto houver tentativa pendente. Sucesso (`2xx` ou `409`) fica `delivered` e não é reenviado. Depois da quinta tentativa a entrega fica `dead` e o job encerra. A posse de cada linha é marcada na hora da aquisição, e a tentativa termina antes dos 30 segundos dessa posse. Um POST lento não deixa a assinatura seguinte com um horário já vencido. Um segundo worker da mesma origem espera a posse da linha: só um POST ocorre, e um `UPDATE` tardio não faz `delivered` voltar a `pending`. Destino recusado pelo urlguard fica `dead` sem esgotar as tentativas.

O POST resolve o nome na hora e liga só no endereço que o urlguard aceitou. Uma resposta com IP privado, metadata ou mistura público/privado não é discada. Redirecionamento não é seguido, mesmo que o cliente HTTP informado fosse seguir. Em produção, IP privado e loopback são recusados, inclusive quando o nome resolve para eles. Em `test` e `development`, só loopback literal em HTTP(S), em qualquer porta, é aceito além do que o urlguard já permite.

Se o schema River ainda não existe, a origem fica com `enqueued_at` nulo e a transação do atendimento confirma. `webhook_recover_sources` insere o job e marca a origem na mesma transação. O processo worker chama essa recuperação ao iniciar, antes de buscar a fila no cliente compartilhado. Uma segunda chamada não cria outro job.

A recuperação roda quando existem URL de jobs, anel de chaves e store. Sem um deles, o atendimento segue e a entrega fica para um processo configurado. A fila `webhooks` só é buscada quando o handler de entrega está ligado nesse cliente.

## Painel

Cookie `__Host-session`, CSRF `X-CSRF-Token` e `Origin` confiável nas escritas. Papel `admin` do tenant. Superadmin e sessão só de cadastro recebem 403.

| Método | Caminho | Efeito |
| --- | --- | --- |
| POST | `/api/panel/outbound-webhooks` | Cria assinatura. O segredo HMAC aparece uma vez, em hexadecimal, e é a chave usada na assinatura. |
| GET | `/api/panel/outbound-webhooks` | Lista sem segredo. |
| POST | `/api/panel/outbound-webhooks/{id}/disable` | Desliga a assinatura. |
| GET | `/api/panel/outbound-webhooks/{id}/deliveries` | Status `pending`, `delivered` ou `dead`. |
| POST | `/api/panel/api-tokens` | Emite token. O segredo aparece uma vez. O banco guarda SHA-256. |
| GET | `/api/panel/api-tokens` | Lista sem segredo. |
| POST | `/api/panel/api-tokens/{id}/revoke` | Revoga. O Bearer seguinte responde 401. |

Escopos: `read`, `contacts.read`, `contacts.write`, `conversations.read`, `messages.read`, `templates.send`, `directory.read`. O escopo legado `read` cobre leitura de conversas, mensagens, contatos e cadastros. Não cobre `templates.send` nem `contacts.write`.

## API pública

Prefixo `/api/public`. Credencial `Authorization: Bearer`. O limite por token e o teto de 600 pedidos por minuto do tenant aplicam-se a todas as rotas de dados. Escopo insuficiente responde 403 antes de consumir o limite. Tenant suspenso responde 403. Outro tenant não é visível e responde 404.

| Método | Caminho | Escopo |
| --- | --- | --- |
| GET | `/api/public/openapi.json` | nenhum |
| GET | `/api/public/v1/contacts` | `contacts.read` |
| POST | `/api/public/v1/contacts` | `contacts.write` |
| GET | `/api/public/v1/conversations` | `conversations.read` |
| GET | `/api/public/v1/conversations/{ref}` | `conversations.read` |
| GET | `/api/public/v1/conversations/{ref}/messages` | `messages.read` |
| GET | `/api/public/v1/conversations/{ref}/contact` | `contacts.read` |
| POST | `/api/public/v1/conversations/{ref}/template` | `templates.send` |
| GET | `/api/public/v1/agents` | `directory.read` |
| GET | `/api/public/v1/sectors` | `directory.read` |
| GET | `/api/public/v1/inboxes` | `directory.read` |

`POST /v1/contacts` aceita nome, telefone, e-mail e `external_ref`. Não grava nem devolve observações. O mesmo `external_ref` devolve o contato já existente.

A leitura de conversa, no detalhe e na lista, inclui `version`. Esse é o `expected_version` do envio. Uma versão diferente da atual responde 409; a releitura traz o valor que o próximo envio novo precisa usar.

`POST /v1/conversations/{ref}/template` chama `Service.SendTemplate`. A revalidação é `channelpolicy.EvaluateDispatchTx`: consentimento da finalidade e template aprovado (`sync_source=driver`, `state=APPROVED`) correm antes do ramo de texto livre que aplica a janela de 24 horas. Sem consentimento a resposta é 422 `consent_required`. A finalidade vazia usa `atendimento`.

A chave `client_request_id` apresentada pelo cliente é do token e do tenant, não da membership do atendente atual. O atendimento recebe outra chave, derivada com o espaço `pubapi` e o id do token, dentro do limite de 200 caracteres. Dois tokens com a mesma chave externa enviam mensagens independentes. A mesma chave usada no painel também não reaproveita o envio da API. O replay só devolve o resultado daquele token.

O hash do conteúdo (`template_id`, idioma, finalidade e conversa) fica na mesma linha que o resultado. Repetir a chave depois de transferir ou desatribuir devolve a mesma mensagem, tentativa e job, sem novo envio. A mesma chave com outro conteúdo responde 409. Um envio novo continua exigindo atendente ativo e a versão atual.

O resultado público é gravado na mesma transação que aceita a mensagem na outbox. Se essa linha ainda estiver aberta e o envio já tiver confirmado, a repetição recupera a mensagem, a tentativa e o job originais. Não há segundo envio, e a recuperação não depende de a membership daquele envio continuar ativa.

Atendentes devolvem só id, papel e status da membership. Caixas devolvem id e nome, sem credencial.

O documento servido em `/api/public/openapi.json` é o arquivo `backend/api/public-openapi.json`. Ele descreve corpos, campos obrigatórios, limites, escopos e os códigos de resposta, inclusive 201, 409 e 422 do template. Ele não entra no OpenAPI do painel.
