# M4B — envio de mídia no canal oficial

O atendente envia uma imagem, um documento, um áudio ou um vídeo já validado na conversa. O texto livre continua em `send_text`. A mídia usa a operação `send_media` e não divide a chave de idempotência com o texto.

## API

`POST /api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/messages`

Corpo JSON:

| campo | obrigatório | uso |
| --- | --- | --- |
| `expected_version` | sim | versão da conversa |
| `client_request_id` | sim | chave de `send_media` quando há anexo; `send_text` quando não há |
| `body` | sim no contrato HTTP | legenda; pode ser vazia no áudio |
| `attachment_id` | não | anexo `outgoing` já `validated` desta conversa |
| `reply_to_message_id` | não | mensagem da mesma conversa |

Com `attachment_id`, o serviço grava uma mensagem e uma linha de outbox. O mesmo `client_request_id`, a mesma legenda, o mesmo anexo de origem e a mesma citação devolvem a mesma tentativa. A repetição não cria outra linha e não converte de novo.

A autorização do atendente, o acesso ao setor, a atribuição, a versão e a legenda já assinada são verificados antes de ler o objeto, converter ou gravar um derivado. Se a publicação da outbox for recusada depois disso, o objeto derivado é apagado e a transação desfaz a linha. Dois envios simultâneos com a mesma chave serializam no banco: o perdedor reutiliza a tentativa já aceita.

A legenda assinada, quando a assinatura do tenant está ligada, entra no corpo da mensagem e no hash. Ela segue o limite do canal: no máximo 1024 caracteres, sem quebra de linha. Uma legenda maior, uma assinatura que ultrapasse 1024 ou uma quebra de linha é recusada antes de confirmar a outbox. O anexo convertido não entra no hash: o anexo pedido pelo cliente permanece em `content_context.attachment_id`; o arquivo efetivamente enviado fica em `content_context.send_attachment_id`.

O download do painel continua sendo a URL assinada e temporária do anexo. Este fluxo não publica URL permanente do objeto.

## Despacho

A outbox passa pelas mesmas revalidações de `dispatch.go` antes do driver:

| condição | estado | código |
| --- | --- | --- |
| janela de 24 h fechada | `cancelled` | `window_closed` |
| contato bloqueado | `failed` | `despacho_invalidado` |
| consentimento do propósito `atendimento` revogado | `cancelled` | `consent_revoked` |

`content_kind` fica `image`, `document`, `audio` ou `video`. A política de janela trata esses tipos como texto livre. O registro em `channel_policy_pending` existe só para a revogação cancelar a outbox; o texto livre sem anexo não ganha esse registro.

O driver recebe o tipo, o id do anexo de envio, a legenda e o id remoto da mensagem citada (`external_message_id`). Um driver sem `MediaSender` falha com `midia_sem_contrato` e não envia a legenda como texto. Aceite remoto grava `local_state=accepted` e o id do provedor na mensagem.

## Canal

`MediaSender.SendMedia` cobre imagem, documento, áudio e vídeo, com legenda (exceto áudio, que o Cloud API não legenda) e `context.message_id`.

O driver fake não abre rede. O driver Meta Cloud oficial envia os bytes por upload em `/{versão}/{phone}/media` e depois a mensagem com o id devolvido. O runtime liga o armazenamento privado em `Service.Files` e os limites do tenant em `Service.AttachmentLimits` antes de copiar o serviço para a API e para o worker. Sem bytes no despacho, o envio oficial falha com `midia_indisponivel` e não chama a Graph. Uma recusa local do `SendMedia`, antes de qualquer HTTP, grava `failed` com `legenda_invalida` ou `envio_recusado`. Esse caso não fica em `resultado_incerto`. Um link, se existir e não houver bytes, passa por `urlguard` antes de qualquer HTTP, inclusive na simulação: host literal privado, link-local, credencial na URL ou esquema que não seja `https` viram `url_recusada`. A simulação, com link aceito ou com bytes, devolve um id local e não chama a Graph. Os testes do driver usam `httptest`; não há chamada à rede real.

Limites do corpo no driver: 16 MiB para imagem, áudio e vídeo; 100 MiB para documento. Legenda até 1024 caracteres.

## Áudio gravado

`audio/webm`, `video/webm` (gravação do MediaRecorder), `audio/ogg`, `video/ogg` e `application/ogg` são convertidos para OGG/Opus antes de entrar na outbox. O anexo original não é alterado. O Opus publicado é um segundo anexo `validated` com `media_type` `application/ogg`, para o reconhecimento e o replay continuarem a tratar o derivado como áudio gravado. No upload `/{versão}/{phone}/media`, quando o kind é áudio, `application/ogg` e `video/ogg` saem como `audio/ogg` no campo `type` e no `Content-Type` da parte do arquivo. A Cloud API aceita OGG somente como `audio/ogg` com codec Opus. `audio/mpeg` e os demais tipos de áudio não são reescritos.

O `ffmpeg` local usa argumentos fixos, sem rede (`-protocol_whitelist file,crypto`), timeout de 20 s, duração máxima de 120 s e saída máxima de 8 MiB. Duração declarada acima de 120 s ou arquivo acima de 8 MiB é recusado antes de procurar o binário. OGG e WebM sem duração declarada são medidos com uma decodificação local do mesmo `ffmpeg`, nos mesmos limites de tempo, tamanho e rede. Acima de 120 s a conversão é recusada. Saída truncada não é aceite. Sem `ffmpeg`, ou se a conversão falha, nada é enfileirado e a resposta é 422 com um destes detalhes:

- `ffmpeg não encontrado; conversão recusada; arquivo original preservado`
- `ffmpeg falhou; conversão recusada; arquivo original preservado`

A conversão de saída precisa do armazenamento privado em `Service.Files` e do limite de anexos do tenant. O Opus é publicado pelo reservatório de anexos: `reserved_bytes` soma `expected_bytes` e a remoção do derivado devolve esse valor. O original não é alterado nem debitado por essa reserva. Sem armazenamento, o áudio gravado responde armazenamento indisponível. Sem cota, a resposta é o limite de armazenamento. Nos dois casos o original permanece e nada é enfileirado. A repetição de uma tentativa já aceita devolve a mesma outbox mesmo se o `ffmpeg` ou o armazenamento estiverem indisponíveis. Imagem, PDF e `video/mp4` não passam pelo `ffmpeg`. `video/mp4` de saída é vídeo; `video/webm` de saída é tratado como áudio gravado.

A migração `00042` só alarga `messages.content_kind` para `image`, `document`, `audio` e `video`.
