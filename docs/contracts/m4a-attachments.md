# M4A — Anexos persistentes privados

Implementação modular para composição posterior. Migração exclusiva `00020_m4_attachments.sql`; nenhuma alteração de runtime, roteador central, OpenAPI, dbgen, compositor ou pipeline de texto. Não envia mensagens a drivers.

## Contrato HTTP

`attachments/httpapi.Register(huma.API, Dependencies)` recebe Auth real, Service e origens exatas confiáveis. Prefixo: `/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments`.

| Método/caminho relativo | Entrada e resultado |
| --- | --- |
| POST vazio | JSON `request_id` UUID idempotente, `name`, `bytes`; registra saída em `upload`, retorna Record |
| PUT `/{attachment_id}/content` | Corpo binário exato, limitado; grava uma vez e passa a `quarantine`; retorna 204 |
| POST `/{attachment_id}/validate` | Inspeciona a versão gravada e persiste `validated` ou `failed`; rejeição retorna 422 |
| GET `/{attachment_id}` | Record com id/state/name/bytes/media_type/expires_at; não revela key, versão ou hash |
| POST `/{attachment_id}/download` | JSON url/expires_in=60; `Cache-Control: no-store` |
| POST `/cleanup` | Remove até 100 registros incompletos vencidos desta conversa; preserva validados; retorna 204 |

Cookie `__Host-session` em todas as rotas. Escritas e emissão de URL exigem Origin permitido e X-CSRF-Token. Superadmin/enrollment são recusados. A política real revalida tenant ativo, membership/papel, setor, caixa e conversa na mesma transação da ação. O gate estreito existente `collision_live_session` também confere usuário/sessão ativos e expiração sem expor users globais. Locks permanecem durante acesso ao storage e assinatura; falha de commit não retorna URL. URLs são capacidades temporárias: revogar acesso impede novas emissões, mas uma URL já emitida pode durar seus 60 segundos. Não registrar URL, corpo nem credenciais em logs de aplicação/proxy/APM.

Erros: 401 sessão, 403 autorização, 404 caixa incorreta para conversa independentemente autorizada, 409 estado/expiração/idempotência divergente, 413 limite de plano/arquivo, 422 bytes/nome/tipo, 503 configuração/storage. Erros externos são reduzidos a mensagem genérica.

## Serviço e persistência

`Service{DB:*db.Store, Storage:Storage, Limits:ResolveLimits, Now:func()time.Time}` publica `Register`, `Get`, `Upload`, `Validate`, `Download`, `ReceiveRemote`, `Cleanup`. Todos recebem contexto, auth.Principal com sessão viva, inbox e conversa; ações de item recebem também attachment UUID. `RegisterInput{RequestID, Name, Bytes, Direction}` aceita incoming/outgoing somente no serviço; HTTP sempre outgoing. `ReceiveRemote` é contrato para etapa posterior do worker, sem ligação atual ao driver.

`attachments`: PK tenant/id, FK composta tenant/conversation, idempotência tenant/conversation/request_id, direção, nome, bytes reservados, chave única, versão, state, tipo detectado, SHA256, timestamps. `attachment_budgets`: namespace UUID aleatório opaco e bytes reservados por tenant. Ambas usam RLS ENABLE + FORCE para o papel de aplicação. Namespace não contém nome, telefone, UUID público do tenant ou identificador da conversa. Não há URL pública no banco.

Registro reserva bytes atomicamente sob lock de orçamento. RequestID repetido exige mesmos nome/bytes/direção; devolve o mesmo registro, inclusive quando expirado. Um novo intento exige novo RequestID. Upload não pode ser repetido/substituído: registro nasce antes da rede e sua chave jamais é reutilizada. Create exige condição create-only e retorna versão imutável; validação e URL usam exclusivamente essa versão. A validação é idempotente após sucesso. Falha de storage/commit conserva reserva; uma escrita cujo resultado se perdeu não é sobrescrita em retry e requer cleanup. Falha de tipo persiste `failed` e não é acessível. Não existe exclusão de mídia validada nesta etapa; retenção fica para política posterior.

`ResolveLimits(ctx, tx, tenantID) (Limits,error)` deve consultar configuração confiável do plano vigente e reserva operacional, dentro da transação; não pode usar dados fornecidos pelo cliente. `PlanBytes>0`, `IncomingReserveBytes>=0`, `0<FileBytes<=32 MiB`; ausência/zero inválido bloqueia admissão. 32 MiB é teto técnico de memória por arquivo desta implementação, não limite comercial. Valores de plano/reserva não têm defaults comerciais. Soma de reservas é a autoridade: saídas param no plano; entradas podem usar somente a reserva operacional adicional configurada; ao esgotar, ErrLimit e alerta/degradação pelo integrador, nunca sucesso fictício ou armazenamento infinito. Registros existentes permanecem. Limites do canal e duração/codecs devem ser revalidados no futuro pipeline de envio.

Upload/quarentena/falha vencem para cleanup após 30 minutos; validados não expiram nesse relógio. `Cleanup` autoriza a conversa, trava budget antes dos registros, remove versões do storage antes de liberar reserva/deletar registro; erro conserva orçamento. Operação precisa agendar invocações por conversa com identidade autorizada e monitorar falhas/idade; não há worker global ou bypass de tenant nesta entrega. Bucket precisa de inventário/reconciliação de chaves sem registro e uploads cujo resultado se perdeu, com período de segurança superior aos timeouts/TTL; isso é uma responsabilidade operacional explícita ainda não automatizada. Não configurar expiração indiscriminada dos objetos validados. O adapter limita recuperação a 100 versões e falha fechado se houver paginação, exigindo intervenção operacional no objeto anômalo.

## Storage S3 de produção

`Storage` exige Create(ctx,key,io.ReadSeeker,size) -> version; Read(ctx,key,version), Sign(ctx,key,version,name,ttl), Delete(ctx,key,version). Versão vazia em Delete significa remover todas as versões exatas de uma chave incompleta; nunca prefixos vizinhos. O double LOCAL SIMULADO existe somente em testes, não é fallback de produção.

`NewS3(S3Config)` usa aws-sdk-go-v2/service/s3 real com endpoint HTTPS, região, bucket, credenciais explícitas e path-style opcional. Falta de credenciais/configuração retorna erro, sem ambient credentials, metadata service ou chamadas de descoberta. HTTP tem timeout de 30s, uma tentativa e sem redirects. Não define ACL pública. Exige bucket privado com **versioning habilitado**, suporte a If-None-Match e política que impeça acesso público. Resposta de PUT sem VersionId imutável é erro; objetos não versionados nunca são publicados. GET confere VersionId retornado. IAM mínimo: PutObject, GetObjectVersion, DeleteObjectVersion, ListBucketVersions, restritos ao bucket/namespace; acesso administrativo externo deve preservar objetos e suas versões. SDK signing inclui VersionId, expiração 60s, response-content-type octet-stream, content-disposition attachment e cache privado/no-store. Storage/previews devem usar origem isolada sem cookies do painel e política apropriada de nosniff/CSP.

Não há preview inline nesta entrega. Tudo sai como download octet-stream, inclusive PDF/imagens. HTML/SVG/XML, executáveis, ZIP/office macros e tipos desconhecidos são recusados. Allowlist por assinatura de bytes: JPEG, PNG, GIF, WebP, PDF, MP3, Ogg, WAV, M4A; MP4 somente recebido. Nome não autoriza tipo e MIME declarado é ignorado. Assinatura não equivale a antivírus, parsing completo, certificado de duração ou codec. Políglotas não obtêm execução inline. Nomes com caminhos, controles, marcas invisíveis de formatação ou mais de 180 bytes são rejeitados. Bytes devem coincidir exatamente com a reserva. Memória é limitada por arquivo; concorrência/global admission adicional pertence ao runtime posterior.

`ReceiveRemote` aceita exclusivamente *urlguard.Client existente: scope deve corresponder ao tenant e connector da caixa; DNS, redirects e destino efetivo passam pelo guard. Busca e gravação mantêm autorização fixada, deadline total de 30s e leitura limitada aos bytes reservados +1. Não há chamadas externas em testes. Nunca entrega URL remota a ffmpeg.

## Conversão e evidências

`ConversionRequest` publica somente contrato de etapa futura: arquivo local/hash, MIME de saída, duração máxima, timeout e bytes máximos. Implementador posterior deve impor CPU/memória/duração, desativar rede e protocolos externos e revalidar saída antes de publicá-la. **Nenhum ffmpeg, conversão, audioRecorder, envio de vídeo, sticker ou reaction foi implementado/executado.**

Testes dirigidos cobrem PostgreSQL real com migrações e papel app/RLS, sessão/CSRF/Origin, tenant/setor/revogação, idempotência, limite/reserva/expiração, estado durável e imutabilidade. Transporte fake do teste SDK verifica serialização, condição de escrita, VersionId e assinatura curta; isso **não comprova S3 real**. O fluxo de dados usa double LOCAL SIMULADO, identificado no código de teste. Uma prova com endpoint S3 real privado/versionado e política de bucket permanece requisito operacional de implantação; não houve integração externa nesta entrega.

Referência de API usada: [exemplos oficiais AWS SDK Go V2](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/go_s3_code_examples.html). SDK fixado e vendorizado sem atualização das dependências não relacionadas.
