# M4B — Mídia, citação e conversão sem ffmpeg

Bloco isolado sobre o storage de anexos já validado. Não substitui upload, quarentena, assinatura curta nem a allowlist de `attachments`. Não chama Meta, ERP nem Evolution.

## O que fica gravado

`00034_m4b_media.sql` acrescenta `messages.reply_to_message_id` e a tabela `media_items`. A citação só aponta para uma mensagem interna da mesma conversa. O objeto continua na chave versionada do anexo. `media_items` guarda direção (`incoming` ou `outgoing`, copiada do anexo), tipo, tamanho, duração e o estado da conversão.

A duração só entra quando o container já a declara: WAV pelo `fmt`/`data`, MP4/M4A pelo `mvhd`, MP3 pelo quadro ID3 `TLEN`, WebM pelo elemento `Duration`. Zero, ausência ou um Ogg sem campo de duração ficam `duration_source=absent`. Não há estimativa por bitrate nem decodificação de amostras.

## HTTP

`media/httpapi.Register` usa a sessão, a caixa e o `attachments.Service` já existentes. Prefixo `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/media`.

| Método | Caminho | Efeito |
| --- | --- | --- |
| POST | `/media` | JSON `attachment_id`, `message_id` opcional, `reply_to_message_id` opcional, `voice`. Exige anexo `validated` desta conversa. |
| GET | `/media/{media_id}` | Metadados, sem chave, versão ou hash. |
| POST | `/media/{media_id}/convert` | Pede OGG/Opus local. |

Cookie `__Host-session`. Escrita exige Origin confiável e `X-CSRF-Token`. Sem ffmpeg, a conversão responde 422 com `ffmpeg não encontrado; conversão recusada; arquivo original preservado`. O anexo de origem não é reescrito. Se um ffmpeg local produzir saída válida, o resultado é um segundo anexo validado e o original permanece. Falha do conversor também recusa e preserva o original. A resposta não marca `converted` sem esse segundo anexo.

O conversor só lê arquivo local em diretório temporário, com tempo máximo de 20s, duração limitada a 120s e saída limitada a 8 MiB. Não busca URL.

## Painel

O compositor grava com `MediaRecorder` e envia o `Blob` original, com o MIME efetivo do navegador e o ID interno opcional da mensagem citada. O navegador não transcodifica. Formatos fora da allowlist atual de anexos (por exemplo WebM) continuam sendo recusados na validação existente; esta entrega não alarga essa allowlist e não finge que o arquivo virou OGG.

## Fora deste bloco

OpenAPI gerado, `schema.ts`, bothook, relatórios e integração não foram alterados. Não houve chamada externa nem execução do ffmpeg do sistema: o teste de sucesso usa um executável local falso só para provar que a saída é um objeto novo e que o original fica intacto.
