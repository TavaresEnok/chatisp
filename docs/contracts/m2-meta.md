# Contrato M2 — driver Meta de texto e decoder do webhook

Este contrato descreve o pacote `backend/internal/channel/whatsapp/metacloud`. Ele implementa o envio de texto e a expansão de um webhook já autenticado. Não escolhe tenant, não persiste envelope, não abre janela de atendimento e não chama a Meta.

A simulação deste pacote não comprova a Cloud API, a Evolution nem o ERP. O servidor `metacloud.Server` é um stand-in local do protocolo documentado. Toda resposta dele leva o cabeçalho `X-ChatISP-Simulation: meta-cloud`. Resultado de driver nesse modo sai com `Result.Simulation = true`.

Fonte de produto: `docs/especificacao.md` V4.1, B.4.1, B.4.2 e B.4.13, e `docs/plano.md` M2. A versão da Graph API continua sem número fixado no produto (D2-03).

## Documentação primária consultada

Leitura de 2026-10-29, sem chamada autenticada e sem fixar a versão de produção:

| Página | O que este pacote usa |
| --- | --- |
| `https://developers.facebook.com/documentation/business-messaging/whatsapp/messages/text-messages` | `POST https://graph.facebook.com/<API_VERSION>/<PHONE_NUMBER_ID>/messages`, corpo `type=text`, resposta com `messages[].id`. Esse id é aceitação, não entrega. |
| `https://developers.facebook.com/docs/whatsapp/api/messages` | `biz_opaque_callback_data` opcional, até 512 caracteres. A Cloud API devolve a string no webhook de status e não a trata como idempotência. |
| `https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/reference/messages/text` | Mensagem de texto: `entry[].id` (WABA), `metadata.phone_number_id`, `messages[].id`, `timestamp`, `text.body`. |
| `https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/reference/messages/status` | Status `sent`, `delivered`, `read`, `failed`, `played`, com `timestamp`, `recipient_id`, erros, pricing e a correlação opaca. |
| `https://developers.facebook.com/docs/graph-api/guides/error-handling` | Código 190: token de acesso inválido ou expirado. |
| `https://developers.facebook.com/documentation/business-messaging/whatsapp/support/error-codes` | Formato `error.code`. HTTP 429 e código 130429 não autorizam reenvio cego. |

Os exemplos públicos usam versões como v17.0, v23.0 e v26.0. Nenhuma delas foi adotada como versão do Chat ISP.

## Ativação do endpoint

`Activate` não abre conexão.

| Modo | Regra |
| --- | --- |
| `official` | `GraphVersion` explícita no formato `v<major>.<minor>`, com major diferente de zero. `PrimaryDocURL` é HTTPS, host `developers.facebook.com`, caminho não vazio, sem query, fragmento ou porta. O pacote registra essa URL; não faz GET nela. Host de envio só `https://graph.facebook.com`, sem porta, usuário ou caminho extra. |
| `simulation` | A mesma versão explícita, só para montar o caminho. `BaseURL` precisa ser `http` ou `https` em loopback (`127.0.0.0/8`, `::1` ou `localhost`). `graph.facebook.com` e endereço de metadados não passam. Documentação primária não é exigida porque o peer não é a Meta. |

O cliente HTTP não segue redirecionamento (`ErrUseLastResponse`) e não usa proxy de ambiente. `Authorization` só sai no primeiro pedido, como `Bearer`, e nunca em query. Falha de configuração devolve erro estático em pt-BR, sem token e sem a URL rejeitada.

O contexto do envio deve ter prazo. Se não tiver, o cliente aplica um teto de 30 segundos (`maxRequestTimeout`) e não fica bloqueado na leitura. Esse teto, depois que o pedido foi escrito, continua sendo resultado `uncertain`. Um prazo mais curto no contexto vence o teto.

`v23.0` aparece nos testes e no servidor simulado (`simulationGraphVersion` em `pipeline/factory.go`) somente como valor explícito do caminho local. Não é a versão ativada do produto: o modo oficial do `DriverFactory` recebe `GraphVersion` e `PrimaryDocURL` da configuração, sem padrão, e falha sem eles (até 05/11/2026 fixava `v23.0`).

## Envio de texto

`Driver` implementa `whatsapp.TextDriver`.

Pedido documentado:

```json
{
  "messaging_product": "whatsapp",
  "recipient_type": "individual",
  "to": "<RecipientID>",
  "type": "text",
  "text": { "body": "<Text>" },
  "biz_opaque_callback_data": "<CallbackData>"
}
```

`CallbackData` só entra quando não está vazio. `preview_url` não é ligado. O texto acima de 4096 runas é recusado antes do despacho. `RecipientID` é enviado no campo `to` da Messages API: neste limite é o endereço externo do canal (telefone ou id que a versão documentar), não um id de ERP e não um telefone inventado por este pacote. Não há consulta de cadastro.

`ConnectionID` fica na tentativa local e não vai no JSON.

| Evidência | Estado local | Remoto |
| --- | --- | --- |
| HTTP 200 e `messages[].id` | `accepted` | vazio. Entrega e leitura não nascem aqui. |
| Código Graph 190 | `failed`, `RejectionCode=token_invalid` | vazio. Código estável para degradar o canal e alertar. Mensagem, subcódigo e `fbtrace_id` são descartados. |
| Contexto cancelado ou conexão recusada antes de escrever o pedido | `cancelled` ou `retrying` | vazio. Há prova de que a chamada não começou. |
| Timeout, queda ou corpo ilegível depois da escrita | `uncertain` | vazio. O id que o servidor falso tenha gerado não volta no resultado. |
| HTTP 408, 409, 425, 429, 5xx, código 1, 2, 130429 ou `is_transient` | `uncertain` | vazio. `HTTPStatusAllowsRetry` continua falso. |
| HTTP 3xx | `uncertain` | vazio. O cliente não segue o `Location`. |
| Outro 4xx com `error.code` numérico e sem id | `failed`, `graph_<code>` | vazio. O texto remoto não é copiado. |
| 200 sem id, ou id junto de `error` | `uncertain` | vazio. |

Capacidades verdadeiras: texto, recibo de entrega e recibo de leitura (estes dois só quando o decoder vê o webhook). Mídia, template, interativo, preço e janela oficial ficam falsos: este driver não os executa. A janela de 24 horas continua sendo regra da plataforma, aplicada por camada acima antes do despacho.

## Decoder

`Parse(appID, raw, ingestedAt)` recebe o equivalente ao `authmeta.Envelope`: o App ID já confiável e o corpo bruto. Não valida HMAC e não lê `tenant_id`, `app_id` ou um tenant padrão do JSON.

Cada item independente carrega `AppID`, `WABAID` (`entry.id`) e `PhoneNumberID`. O tenant continua sendo o do registro app–WABA–número, fora deste pacote.

| Item | Identidade e dedup |
| --- | --- |
| `message` | Só `type=text` com `messages[].id`, `entry.id` (WABA) e `from` não vazios. Chave `message:<id>`. O mesmo id deduplica; o mesmo texto com outro id não. |
| `status` | `sent`, `delivered`, `read`, `failed`, `played`, com id e WABA. Chave JSON com id, tipo, timestamp bruto, destinatário, códigos de erro, categoria/tipo/modelo/billable e `biz_opaque_callback_data` quando cabe em 512 bytes. `delivered` e `read` preenchem `Remote`. `sent` não é aceitação local nem entrega. |
| `quarantine` | JSON inválido, objeto ou campo desconhecido, número ausente, item sem id, WABA vazio, mensagem sem `from`, tipo que não é texto, status desconhecido, grupo, item grande demais, excesso do teto global. `Reason` é um código estável. Sem id, a chave é hash curto, não o envelope. |

WABA vazio ou `from` vazio não vira mensagem nem status. A razão é `sem_identidade`. O tenant continua fora deste pacote.

A contagem de itens é global no corpo, não por `change`. Os primeiros 128 objetos decodificados seguem com o seu tipo. Cada objeto além disso vira uma quarentena `limite_de_itens`, com o objeto daquele item em `Preserved`. Não há descarte silencioso e não há um segundo marcador vazio. Um `change` com mais de 128 mensagens conserva o excedente.

`Preserved` dessas quarentenas (JSON inválido, campo não suportado, número ausente, texto sem id, status sem id, excesso) é o objeto daquele item, cortado em 64 KiB. Não é o envelope. O corpo acima de 1 MiB continua uma única quarentena `corpo_acima_do_limite`, com no máximo 64 KiB iniciais.

`OriginAt` vem do `timestamp` Unix do objeto. `IngestedAt` é o relógio que o chamador passou; o decoder não usa `time.Now`. `Preserved` é o objeto daquela mensagem ou daquele status, para a área restrita. Não é o envelope e não deve ir para a API do tenant. Este pacote não registra corpo, token ou envelope em log.

Pricing é copiado como evidência (`category`, `type`, `pricing_model`, `billable`). Não há valor em dinheiro e a capacidade `Pricing` permanece falsa.

`MarshalTextEnvelope` e `MarshalStatusEnvelope` montam corpos reutilizáveis no formato acima. São fixtures, não gravações da Meta.

## Servidor falso

O stand-in está em `server_test.go`. Só entra no binário de `go test` deste pacote. A biblioteca não importa `net/http/httptest` e não registra as flags desse pacote. `NewServer(version, phoneNumberID, token)` escuta em `127.0.0.1` e responde `POST /<version>/<phone>/messages`. Não há chamada à Meta.

| Modo | Comportamento |
| --- | --- |
| `accept` | 200 com `messages[].id` sintético `wamid.sim.<n>`. |
| `token_invalid` | 401, código 190, mensagem e `fbtrace_id` propositalmente sujos. O driver não os repete. |
| `lose_response` | Registra o id interno e fecha a conexão sem resposta. |
| `timeout` | Registra o id interno e espera o cancelamento do cliente. |
| `status_before_response` | Entrega um webhook `delivered` ao hook e só depois escreve o 200. |
| `http_429` / `http_500` | Resposta sem id de aceitação para o cliente. |
| `redirect` | 302 para `/stolen` no mesmo listener. O driver não deve gerar hit nesse caminho. |

Bearer diferente do token configurado também responde 190. O servidor não faz saída de rede.

## Fora deste contrato

Persistência do envelope, quarentena operacional, resolução de tenant, outbox, River, cursor, painel e qualquer chamada real continuam nas entregas seguintes do M2. Trocar `BaseURL` da simulação pelo host oficial exige o modo `official`, a versão explícita e a URL primária; isso ainda não é teste real e não está executado aqui.
