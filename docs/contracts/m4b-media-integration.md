# M4B — composição da mídia inbound com o envio outbound

Árvore que junta o inbound já integrado em `8f88a0a98a7d8053a7de2f70bd4fad5da1425d34` com o outbound revisado `6526bdf938d6bd85e94381a27d239894385d3470`. Não chama Meta, ERP nem Evolution. O recibo `media-integration-conflicts.json` não estava neste disco.

## Ancestrais

Os dois commits são os pais pretendidos. O diretório Git desta worktree está montado somente leitura (`index.lock`: Read-only file system), então esta sessão não gravou `MERGE_HEAD` nem o commit. A gerência cria o merge com esses dois pais, sem squash e sem rebase, sobre esta árvore. `git diff --check` nesta árvore saiu 0.

## Conflitos

Três arquivos divergiram no `git merge-file`. O restante dos caminhos alterados nos dois lados coincidiu com o merge automático.

`backend/cmd/chatisp/main.go` usa um único `privateMedia`, criado com `attachmentStorage` antes de `attendanceSvc`. `Files` e `AttachmentLimits` (`tenantAttachmentLimits`) são atribuídos nesse valor antes de copiá-lo para a API e para os workers. O mesmo storage vai para `attachments.Service` e para `WorkersConfig.MediaStorage`. Permanecem `MediaFetcher`, `Simulation` e o comentário de que uma atribuição posterior não alcança handlers já construídos.

`attendance/service/service.go` mantém `Files` e `AttachmentLimits`. O comentário pede as duas coisas: um storage compartilhado pela API e pelos workers, atribuído depois de `New` e antes da cópia. As ações de mídia ficam em `media_outbound.go`. O código de ERP que já estava fora deste arquivo não foi reescrito. O diff deste arquivo contra `8f88a0a` é só esse comentário.

`channel/configuration/service_test.go` recusa `SELECT body FROM messages` e `SELECT contact_id FROM conversations` com o mesmo `assertDenied` dos dois lados. `count(*)` da role `chatisp_platform` não entra nessa recusa. `count(*)` de `chatisp_ingress` continua recusado. `has_table_privilege` e as demais recusas permanecem. O conflito era só o comentário.

## Merge automático

`store/message.go` e `store/types.go` ficam com o contexto inbound (`content_kind`, citação) e com `AllowEmptyBody` do outbound. O `git merge-file` desses arquivos, de `routes.go`, `types.go`, `openapi.json` e `web/src/api/schema.ts` coincidiu com a árvore. O JSON do OpenAPI parseia. `SendBody` tem `attachment_id` e `reply_to_message_id`. O histórico mantém `content`, `content_kind` e `reply_to_message_id`.

`media/pipeline_test.go` junta os dois temporários executáveis. O primeiro recusa sem `ffmpeg` (`TMPDIR` absoluto e `PATH` vazio). O segundo grava o falso `ffmpeg` em diretório executável, com cleanup, e restaura o `PATH` por `t.Setenv`. A segunda declaração de `wd` ficou `=`, porque `:=` não compila no mesmo bloco.

`backend/internal/attendance/pipeline/media_inbound.go` não entra nesta composição: o arquivo não está na lista autorizada e permanece igual a `8f88a0a`.

## Migração 00042

No commit outbound, `00042` alargava `content_kind` a partir de `text` e `template` e o `Down` voltava a esses dois. Nesta linha, `00041` já inclui `image`, `document`, `audio`, `voice`, `video`, `sticker`, `location`, `contacts`, `interactive` e `button`. O `00042` composto repete a união. O `Down` volta ao check de `00041`, não a `text`/`template`.

## Outbound preservado

Arquivos só do outbound, inclusive `attachments/service.go`, `convert.go`, `media_send.go` e `convert_ffmpeg_test.go`, permanecem o comportamento revisado de `6526bdf`. `media_outbound.go` acrescenta o lock da citação antes do orçamento. O envio usa `send_media`, citação, conversão para OGG/Opus e upload `audio/ogg` (`metacloud/media_send.go` reescreve `application/ogg` e `video/ogg` quando o kind é áudio). `lockMediaSend` usa `pg_advisory_xact_lock` da tentativa; em seguida `admit` mantém a ordem de telefone/conector, contato e conversa. A autorização continua antes da leitura, da conversão e do objeto derivado.

## Ordem de locks

`media_inbound.go` permanece igual a `8f88a0a`. `commitStored` continua travando a mensagem (`FOR UPDATE`), depois `attachment_budgets`, e o `INSERT` em `attachments` pede `KEY SHARE` na conversa.

`SendMedia` com citação trava essa mensagem com `FOR KEY SHARE` (`send-media-reply`) depois da admissão e antes do orçamento e da conversa exclusiva. A admissão segue telefone/conector, contato e conversa `FOR SHARE`; esse modo convive com o `KEY SHARE` do anexo inbound. Áudio gravado trava em seguida `attachment_budgets` (`send-media-budget`). Imagem e documento não travam o orçamento e usam o mesmo lock da citação. A autorização sobe a conversa para `FOR UPDATE` só depois. Leitura, conversão e `PublishReserved` continuam depois da autorização. O `UPDATE` de `reply_to_message_id` pede outra vez `KEY SHARE` na mensagem já travada nesta transação. Sem citação, o áudio segue admissão, orçamento e conversa exclusiva. `PublishReserved` não mudou e repete o lock do orçamento na mesma transação.

`TestSendMediaConvertedAudioOverlapsInboundCommit` segura `attachment_budgets`, espera `DownloadInboundMedia` (`commitStored`) bloquear nesse orçamento e só então chama `SendMedia` do webm na mesma conversa, sem citação. Com os dois à espera, um `SELECT … FOR KEY SHARE` na conversa conclui: o envio ainda não está em `FOR UPDATE`. O bloqueio é solto em seguida. Os dois terminam sem `40P01`. A reserva fica a soma do JPEG de entrada com o OGG derivado; há um anexo de entrada, um `audio.ogg` e uma tentativa. Cada linha de `attachments` aponta para um objeto e o storage não fica com chave sobrando.

`TestSendMediaCitationOverlapsInboundCommit` repete o bloqueio com imagem e `ReplyToMessageID` na mensagem que `commitStored` segura. `TestSendMediaConvertedCitationOverlapsInboundCommit` faz o mesmo com webm convertido. Os dois esperam o envio parar em `send-media-reply` antes de soltar o orçamento. Nesse intervalo o `FOR KEY SHARE` na conversa conclui. Não há `40P01`. A outbox fica em uma tentativa e `reply_to_message_id` aponta para a mensagem inbound. A reserva da imagem é só o JPEG; a do áudio soma esse JPEG ao OGG. Não sobra objeto. Documento percorre o mesmo ramo que a imagem, sem orçamento antecipado. O `ffmpeg` desses testes é um stub executável no diretório de trabalho: mede `00:00:01.00` e grava `OggS`. Não chama Meta, ERP nem Evolution.

## Limites

A medição real de duração subdeclarada e a conversão com o binário do sistema continuam nos testes que pulam quando `ffmpeg` não está no `PATH`: `TestTranscodeRecordedWithFFmpeg`, `TestTranscodeRecordedRefusesUnderdeclaredWebM`, `TestTranscodeRecordedRefusesRealUndeclaredOverLimit` e `TestSendMediaUnderdeclaredWebMRefusesWithoutOutbox`. O stub não substitui essa medição. Se o runner da suíte obrigatória não tiver o binário, esses quatro pulam e a gerência executa essa lista uma vez num host com `ffmpeg`.

Checks desta árvore, uma vez, saída 0: `npm ci --prefix backend/tools/pg --no-audit --no-fund`; `go -C backend build ./...`; `go -C backend test -count=1` de attendance, channel, media, attachments, platform/runtime e platform/jobs. Não há cliente Graph, ERP nem Evolution nesta composição.
