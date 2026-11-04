# Continuação — frente de produto

Worktree: `/home/enok/srv_projetos/workspace/chat-isp-cursor-produto`
Branch: `continuation/cursor-produto`
Base: `54febe97b69ff131d80ff3cc880eac33f832e958` (`orchestration/eec2118bdf14`)

Dependência local: `npm ci --prefix backend/tools/pg` (PostgreSQL embutido dos testes). `npm ci --prefix web` (537 pacotes). O Vitest deste host precisou de Node 24.11.0 em `/tmp/chatisp-node24` (o Node do sistema é 20 e o jsdom não sobe). O binário não entra no repositório.

## Privacidade, exportação e exclusão

Estado: **implementado e verificado** no banco sintético do teste. Não é homologação com titular real nem com backup de produção.

O que fechou:

- Exportação do titular em `GET /api/panel/contacts/{id}/privacy-export`, só administrador. Inclui perfil, canal, corpos, notas, metadados de anexo e demandas. Não inclui cifra, HMAC nem chave de objeto. O rótulo deixa claro que não é o histórico do ERP e que backup imutável fica de fora.
- Exclusão em `POST .../privacy-deletion` com confirmação `excluir titular`. Anonimiza o contato, esvazia corpos, limpa documento e apaga anexos. Protocolo, ciclo, motivo e protocolo externo do vínculo permanecem. Cada anexo vai para `deletion_records` (`attachment`) para o ensaio de restore reaplicar. A trilha por classe fica em `retention_deletion_records`.
- Repetição devolve `already_erased` e não apaga o ciclo. Se o armazenamento recusa o objeto, a transação volta atrás.
- `automatic_purge` continua falso. `POST /api/panel/retention/purge` segue 409 e não apaga conteúdo por classe.
- Painel do administrador: exportar dados do titular e excluir só depois da frase de confirmação.

Commits: ver `git log` desta branch. O primeiro bloco é o commit de privacidade.

Evidência:

- `go test -count=1 ./internal/retention/ ./internal/retention/httpapi/` — ok (retention 1,994s, httpapi 1,816s).
- `go vet` dos mesmos pacotes — ok.
- Vitest `retention-panel.test.tsx` e `identity.test.tsx` com Node 24.11.0 — 2 arquivos, 9 testes, ok.

Lacuna restante:

- Expurgo operacional permanece desligado (D2-06). Não há prazo regulatório inventado.
- Backup imutável não é apagado na hora. A reaplicação depois do restore continua sendo a do ensaio existente, que lê `deletion_records`; este bloco não reexecutou o ensaio de restore.
- A cifra do documento não é devolvida em claro. A exportação informa só que o documento existe e o tipo.
- Sem storage configurado, a exclusão de um contato que ainda tem anexo falha por inteiro em vez de deixar o arquivo para trás. Com storage indisponível de propósito (`UnavailableStorage`), o mesmo vale.

## Mídia e ffmpeg

Estado: **implementado e verificado** com o ffmpeg do host (`/usr/bin/ffmpeg` 4.2.7, já instalado; nenhum pacote novo). Não é homologação de envio na Meta.

O que fechou:

- A rota de conversão do painel recusa duração declarada acima de 120 s antes de procurar o ffmpeg e preserva o arquivo original. Sem o binário, um arquivo dentro do limite continua recusado como antes.
- `audio/mpeg`, `audio/mp4` e WAV passam pelo mesmo conversor limitado já usado para WebM/OGG. O painel avisa que a conversão é no servidor. O navegador continua sem transcodificar.
- MPEG de 1 s gerado pelo ffmpeg do sistema saiu OGG/Opus. WebM/OGG acima de 120 s, inclusive com duração subdeclarada, foram recusados.

Evidência:

- `go test -count=1 ./internal/media/ -timeout 180s` — ok, 6,956s. Inclui `TestTranscodeRecordedWithFFmpeg`, `TestTranscodeRecordedMPEGWithFFmpeg`, `TestTranscodeRecordedRefusesUnderdeclaredWebM` e `TestTranscodeRecordedRefusesRealUndeclaredOverLimit`, sem skip.
- `go test -count=1 ./internal/attendance/service/ -run 'TestSendMediaRecordedAudioBudgetReplayAndCompensation|TestSendMediaUnderdeclaredWebMRefusesWithoutOutbox'` — ok, 4,769s.
- `go vet ./internal/media/ ./internal/attendance/service/` — ok.
- Vitest `media-panel.test.tsx`, `message-composer.audio.test.tsx`, `message-composer.media.test.tsx` com Node 24.11.0 — 3 arquivos, 15 testes, ok.

Lacuna restante:

- Não houve envio real para a Cloud API. O binário do host não tem limite de CPU/memória imposto pelo cgroup do worker; o processo aplica tempo, duração, tamanho e `-protocol_whitelist file,crypto`.
- `video/mp4` continua vídeo e não entra no ffmpeg.

## API pública e integrações

Estado: **parcial**. A atualização de contato está implementada e verificada em banco sintético. Enviar mensagem, atribuir e mudar o estado da conversa pela API pública continuam só no fluxo do painel e no envio de template já existente. Não houve chamada a Meta, ERP ou Evolution.

O que fechou neste bloco:

- `PATCH /api/public/v1/contacts/{id}` com escopo `contacts.write`. Atualiza nome, telefone, e-mail e referência externa. Não grava documento nem observações. Referência repetida de outro contato responde 409. Contato anonimizado pela exclusão do titular responde 409 e o nome permanece. `contacts.read` recebe 403.
- O documento `backend/api/public-openapi.json` descreve a rota.

Evidência:

- `go test -count=1 ./internal/publicapi/ -timeout 180s` — ok, 2,181s.
- `go vet ./internal/publicapi/` — ok.

Lacuna restante:

- `POST` de mensagem de texto, atribuição e mudança de estado ainda não têm rota pública. O template (`POST /v1/conversations/{ref}/template`) e a criação de contato permanecem como estavam.
- Webhook de contato criado ou alterado é V2 e não foi adicionado.
- Homologação com ERP, Meta ou Evolution reais não foi feita e não deve ser feita com credencial deste ambiente.

## Fechamento da frente

No tree `f7be14d`, a validação conjunta foi `go test -count=1 -timeout 240s ./internal/retention/ ./internal/retention/httpapi/ ./internal/media/ ./internal/publicapi/`. Resultado: ok em 2,010s, 1,940s, 7,245s e 2,290s. A suíte inteira do repositório não rodou.

## O que a frente de operação não deve sobrescrever

- `backend/db/migrations/00046_privacy_subject.sql`
- `backend/internal/retention/privacy.go` e o painel em `web/src/features/retention/`
- `backend/internal/media/convert.go`, `needsOpus` em `media_outbound.go` e `needsServerConversion` em `web/src/features/media/model.ts`
- `PATCH /v1/contacts/{id}` em `backend/internal/publicapi/` e `backend/api/public-openapi.json`
- Este arquivo, `docs/CONTINUACAO-PRODUTO.md`

`docs/CONTINUACAO-CURSOR.md` não foi criado nem editado aqui.
