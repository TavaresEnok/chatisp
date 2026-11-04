# M4B — Mídia recebida do canal oficial

Contrato do que o painel lê depois que um webhook do WhatsApp Cloud API entra no tenant. Não chama Meta, ERP nem Evolution. O download de teste usa um `MediaFetcher` em memória.

## Mensagem

`messages.content_kind` passa a aceitar `image`, `document`, `audio`, `voice`, `video`, `sticker`, `location`, `contacts`, `interactive` e `button`, além de `text` e `template`. O objeto estruturado fica em `messages.content_context`. Citação resolvida usa `messages.reply_to_message_id` quando a mensagem citada já está na mesma conversa. O id remoto continua em `content.context_id`.

```json
{
  "type": "image",
  "caption": "foto da fachada",
  "filename": "boleto.pdf",
  "mime": "image/jpeg",
  "media_id": "100",
  "voice": false,
  "latitude": -23.5,
  "longitude": -46.6,
  "location_name": "Sede",
  "address": "Rua 1",
  "contacts": [
    {
      "name": "Bia",
      "phones": ["+5511988887777"],
      "emails": [],
      "org": "ISP",
      "vcard": "BEGIN:VCARD\nVERSION:3.0\nFN:Bia\nTEL:+5511988887777\nEND:VCARD"
    }
  ],
  "interactive_type": "button_reply",
  "reply_id": "yes",
  "title": "Sim",
  "context_id": "wamid.quoted",
  "reply_to_message_id": "uuid-interno",
  "media_status": "stored",
  "media_error": "",
  "sniffed_mime": "image/jpeg",
  "attachment_id": "uuid-do-anexo",
  "bytes": 4
}
```

Campos ausentes não aparecem. `media_status` é `pending`, `stored` ou `failed`. `media_error` usa `mime_divergente`, `tamanho_excedido`, `download_falhou`, `mime_nao_reconhecido`, `vazio`, `armazenamento_indisponivel` ou `limite_excedido`. A mensagem permanece. Áudio com `voice: true` grava `content_kind=voice`.

Limites de download por tipo: imagem 5 MiB, figurinha 100 KiB, áudio e vídeo 16 MiB, documento 32 MiB. O MIME gravado é o detectado nos bytes, não o declarado pelo webhook. `application/octet-stream` declarado aceita o tipo detectado. MP4 é `audio/mp4` quando o container tem faixa de áudio e nenhuma de vídeo, inclusive com marca genérica `isom`. Marca `M4A` sem faixas continua áudio. Um container com faixa de vídeo é `video/mp4` e, se chegou como áudio, fica `mime_nao_reconhecido`. Documento aceito: PDF, texto (`text/plain`, `text/xml`, `application/xml`), RTF, ZIP, Office antigo (OLE) e Office Open XML (Word, Excel, PowerPoint). Imagem, áudio e vídeo enviados como arquivo também passam. HTML e conteúdo sem assinatura reconhecida ficam `mime_nao_reconhecido`. O texto do cliente nunca é relido como estrutura: `content` chega no item do webhook em campo próprio.

O download bem-sucedido também reserva os bytes em `attachment_budgets.reserved_bytes`, o mesmo contador lido pelo registro de anexos de saída. A capacidade de entrada é `attachment_plan_bytes` mais `attachment_reserve_bytes`. O arquivo não pode passar de `attachment_file_bytes`. Estouro desse orçamento, do limite individual ou do teto `max_storage_bytes` grava `limite_excedido`, apaga o objeto ainda não publicado e não cria a linha em `attachments`. Uma nova tentativa do mesmo webhook não duplica a reserva.

Durante o download, `media_status` pode aparecer como `reserved`, com `reserve_token` e `reserved_at`. Esses campos somem quando o estado vai para `stored` ou `failed`. A gravação no storage é create-only. Se o processo cai depois de gravar o objeto e antes do commit, a tentativa seguinte apaga o objeto órfão e grava de novo. Uma execução sobreposta não rebaixa `stored` para `failed`.

## Reação

Reação não cria mensagem e não abre nem reabre atendimento. Existe uma linha por tenant, conector, alvo e ator. Um emoji novo substitui o anterior. Um evento mais antigo não desfaz um mais novo. O emoji vazio remove a reação da lista visível sem apagar a linha. O histórico omite emoji vazio.

## Histórico

`GET /api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/history` repete os campos em cada mensagem:

```json
{
  "id": "uuid",
  "content_kind": "image",
  "content": {},
  "reply_to_message_id": "uuid",
  "attachment": {
    "id": "uuid",
    "name": "imagem",
    "media_type": "image/jpeg",
    "state": "validated",
    "bytes": 4
  },
  "reactions": [
    {"emoji": "👍", "external_id": "wamid.react", "actor": "5511999990001"}
  ]
}
```

`content` é o mesmo objeto de `content_context`. `attachment` só existe depois do download validado. `reactions` lista as reações da conversa ligadas àquela mensagem.

## Eventos

O evento `conversation.message` inclui `content_kind`, `content` e, quando a mensagem citada já está na conversa aberta, `reply_to_message_id` e `reply_to_text` (o texto da mensagem citada). O mesmo id interno também fica em `content.reply_to_message_id`. O download concluído ou recusado publica `conversation.inbound_media` com `conversation_id`, `message_id`, `content_kind`, `content` e, no sucesso, `attachment_id`. A reação publica `conversation.reaction` com `message_id`, `emoji`, `external_reaction_id` e `target_external_id`. Emoji vazio nesse evento significa remoção. O payload não leva bytes nem URL assinada.

## Download

O job River `chatisp.inbound.media.v1` carrega só `tenant_id` e `message_id`. A fila é `inbound_media`. O processo (`cmd/chatisp`) passa o storage privado e o fetcher escolhido por `runtime.FetcherForProcess` para `pipeline.NewWorkers`. Em simulação o fetcher é o stand-in local, sem cliente Graph. Fora de simulação o fetcher é o Graph com `urlguard`. Testes usam `whatsapp.FakeFetcher` nesse mesmo `NewWorkers` e não discam para a rede. O arquivo fica no storage privado do tenant, na linha `attachments` com estado `validated`, e em `media_items`. O mesmo webhook não cria segunda mensagem nem segundo anexo.
