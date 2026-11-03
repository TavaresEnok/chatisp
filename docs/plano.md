# Plano do Chat ISP

Fonte normativa: `docs/especificacao.md`, revisão V4.1, de 28/10/2026.
Este arquivo relaciona requisitos, marcos, riscos e testes. Ele não substitui a especificação e não declara marco concluído.

Estado verdadeiro de cada marco, inclusive o M0: **pendente**.
Estado verdadeiro de cada requisito listado: **pendente**.
Nada além da preparação documental foi implementado. A conclusão de um marco só muda quando o critério correspondente tiver evidência e este arquivo for atualizado de propósito.

Escopo implementável: as 257 linhas V1 e V1 simples das seções 01 a 14 (196 V1 e 61 V1 simples). A seção 10 não tem linha V1 nem V1 simples. A seção 15 é índice e não soma requisito novo. V2 fica de fora, salvo os pontos de extensão citados no fim deste plano. Itens “Não fazer” permanecem fora do produto.

Identificadores e código em inglês. Interface, mensagens e documentos em pt-BR. Módulo Go `chatisp`, em `backend/`. Webchat primeiro. O ERP existente é integração.

## Versões fixadas nesta preparação

| Item | Versão ou identificação | Onde vale |
| --- | --- | --- |
| Especificação | V4.1, 28/10/2026 | Fonte de produto e de engenharia |
| Go | 1.27.3, acima do mínimo 1.25 | `backend/go.mod` no M1. Hoje o host já tem esse toolchain |
| Node | v24.22.1 | Frontend no M1 |
| npm | 11.19.1, o que acompanha esse Node | ADR 0002. O npm 10.9.9 de `/usr/local/bin` não grava lockfile |
| PostgreSQL de produção | 18, minor fixado no deploy | Serviço externo. Fora do binário |
| PostgreSQL de teste | 18.6.0 embutido, ou `CHATISP_TEST_DATABASE_URL` | ADR 0001. Só testes |
| Referência Chatwoot | commit `f428ad8e6f0390faa0ef2ac8e94d8886d56f3017` | Fora do build. Ver `docs/chatwoot-referencia.md` |
| Graph API, Evolution, ERP | Sem número de versão | Pendentes em `docs/perguntas.md` |

Bibliotecas nomeadas na especificação, com versão travada só quando o marco as adicionar ao manifesto: chi, Huma v2, coder/websocket, pgx/v5, sqlc, goose, River open-source, Ristretto, slog, Prometheus (`client_golang`), Argon2id, `pquerna/otp`, sony/gobreaker, aws-sdk-go-v2, caarlos0/env, ffmpeg, React 19, TypeScript strict, Vite, React Router, TanStack Query, TanStack Virtual, Zustand, React Hook Form, Zod, Tailwind, shadcn/ui, Geist, openapi-typescript e openapi-fetch. Este plano não inventa o número de release de cada uma.

testcontainers-go permanece citado na especificação e não roda neste host. A exceção está na ADR 0001.

## Fluxo de telas

Seis áreas, com link direto. O caminho da URL fica para o router do M1; o nome da área é o contrato de navegação.

| Área | Uso | O que aparece antes do marco da área | Marco em que passa a ter dado real |
| --- | --- | --- | --- |
| Painel | Fila, espera, presença e volume | Rota vazia e honesta | M7 |
| Conversas | Lista, histórico, compositor e lateral | Rota vazia no M1 | Texto no M2; operação do atendente no M3 |
| Contatos | Diretório e histórico da pessoa | Rota vazia no M1 | M3 |
| Equipes | Setores, membros e capacidade | Rota vazia no M1 | Cadastro usado pela visão no M3; distribuição no M5 |
| Relatórios | Períodos, tempos e CSV | Rota vazia no M1 | M7 |
| Configurações | Usuários, números, horários, respostas prontas, etiquetas e integrações | Usuários e sessão no M1 | Canal no M2 e M4; automação no M5; ERP e Web no M6 |

Fluxo mínimo de atendimento, alvo do M2 e enriquecido depois:

1. Pessoa não autenticada vê o login.
2. Administrador convida o atendente. O primeiro acesso define senha. Administrador conclui o MFA.
3. O painel abre nas seis áreas. Área sem dado mostra vazio. Nenhum número de fila fictício aparece como operação real.
4. Um texto chega pelo conector Meta. Nos testes, o servidor é falso e nomeado como simulação. A mensagem persiste, a conversa e o protocolo nascem e o histórico abre no painel.
5. O atendente assume, responde e encerra com motivo, resultado e síntese.
6. A partir do M3 ele também filtra a fila, busca, transfere, anota, etiqueta, marca leitura, guarda rascunho e recebe aviso de colisão.
7. A lateral mostra o contato. Dados de ERP só entram no M6, e só com adaptador real ou com fake identificado. Falha de ERP não impede o passo 5.
8. Canal degradado, envio incerto e falha ficam distintos na conversa. Rascunho não é mensagem aceita. Sem servidor, o rascunho permanece e o envio não é apresentado como aceito.

## Contratos externos conhecidos

Nenhuma chamada real a Meta, Evolution ou ERP faz parte deste plano. Onde o sistema externo ainda não tem documento, o módulo independente segue com double identificado. Double não é integração entregue.

### Meta Cloud API

Conhecido pela especificação, sem cliente real: configuração manual com token, App ID, App Secret, WABA ID e Phone Number ID; verificação de webhook com token próprio, distinto do App Secret; HMAC do corpo bruto; templates pertencentes à WABA; janela de 24 horas da plataforma só no conector oficial; `biz_opaque_callback_data` como correlação onde o endpoint suportado o tiver, sem tratar isso como idempotência. A versão da Graph API não está fixada. Cobrança em dinheiro não entra na V1 (D2-05).

### Evolution

Serviço externo e opcional. A flag nasce desligada. A autenticação é a da release escolhida, quando D2-04 existir. O driver não inventa HMAC no formato da Meta e não embute Baileys. Eventos previstos na especificação: entrada atual, eco, aparelho externo, histórico, edição ou revogação, e evento não suportado. QR não é persistido.

### ERP

Os nomes SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument e GetPixCode são capacidades internas. Não são paths de SGP, IXC nem de outro fornecedor. `docs/integracoes/erp-existente.md` ainda não existe. Até a D2-01, o adaptador de teste é fake. Sem a D2-02, dado sensível e ação diante do cliente ficam ocultos. O chat não gera boleto, não calcula juros e não guarda cadastro financeiro.

### IA externa

Um gancho por caixa, webhook com HMAC e API Bearer. HTTP 200 significa que o gancho recebeu o evento. Prazo vencido devolve a conversa ao humano. Não há modelo dentro do processo.

### E-mail e storage

SMTP, por interface pequena, só para convite e recuperação de senha. Storage de produção é S3 compatível, via aws-sdk-go-v2. O exemplo de desenvolvimento com Docker é um serviço local compatível, como Garage. MinIO não é dependência. Neste host, sem Docker, o teste de anexo usa double local marcado como simulação até existir endpoint S3.

## Testes de aceite de B.8

Os 22 cenários abaixo são os de B.8. Cada um tem um marco responsável, repetido na seção desse marco. Contrato simulado não comprova Meta, Evolution nem ERP reais.

| ID | Cenário | Evidência esperada | Marco responsável |
| --- | --- | --- | --- |
| B8-01 | Tenant A tenta vincular mensagem à conversa do tenant B | FK e regras rejeitam. Busca, erro e evento não vazam o dado | M2 |
| B8-02 | Envelope Meta contém itens dos tenants A e B | A expansão roteia cada item. Nenhum tenant lê o envelope completo | M2 |
| B8-03 | Consulta ou operação sem contexto de tenant | O acesso de domínio rejeita antes de executar. O banco permanece fechado | M1 |
| B8-04 | Dois envios com a mesma chave em paralelo | Uma mensagem e uma outbox, com o mesmo resultado. Payload diferente conflita | M2 |
| B8-05 | O processo cai depois da chamada remota e antes de gravar a resposta | Estado uncertain. Sem repetição automática | M2 |
| B8-06 | O bot enfileira, o humano assume e o worker executa | A pendência é cancelada. Epoch antiga recebe conflito | M5 |
| B8-07 | A tomada humana ocorre depois do despacho remoto | O estado em voo é reconhecido. A UI não promete cancelar a entrega externa | M5 |
| B8-08 | O cliente responde enquanto o job de inatividade começa | A resposta fica preservada. O job obsoleto não encerra | M5 |
| B8-09 | Duas primeiras mensagens concorrentes | Uma conversa ativa e um protocolo consistente | M2 |
| B8-10 | Dois workers reservam a última vaga do atendente | Uma atribuição é aceita. A outra conversa permanece na fila | M5 |
| B8-11 | O adiamento vence ou recebe resposta antecipada | Volta conforme a capacidade, sem encerramento automático | M5 |
| B8-12 | O bot confirma o webhook e depois trava | O prazo vence e a conversa chega ao humano | M5 |
| B8-13 | A janela oficial vence durante a espera na fila | O envio revalida a janela e impede mensagem livre não permitida | M4 |
| B8-14 | Sincronização histórica ou eco da Evolution | Não duplica mensagem e não dispara automação atual | M6 |
| B8-15 | Snapshot inicial e evento concorrente | Nenhuma mudança é pulada. Repetição é deduplicada | M2 |
| B8-16 | Evento invisível entre dois visíveis | O cursor avança. O payload não vaza e o cliente não entra em loop | M3 |
| B8-17 | Transferência de setor com sessão aberta | API e eventos futuros são negados. O cache visual é invalidado | M3 |
| B8-18 | ERP, IA e Evolution indisponíveis | O chat oficial e as ações independentes continuam | M6 |
| B8-19 | O ERP conclui a ação e a resposta se perde | Conciliação ou incerto, sem executar de novo às cegas | M6 |
| B8-20 | Relatório das 8h às 12h com 3h de espera | 4h totais e 1h operacional. Sobreposição não desconta duas vezes | M7 |
| B8-21 | O bot resolve sem humano | A primeira resposta humana fica não aplicável e o resultado é classificado | M7 |
| B8-22 | Restore de banco, anexos e chaves | Conteúdo acessível, referências consistentes e exclusões reaplicadas | M8 |

## Riscos que o plano carrega

- Este host não executa Docker, make, PostgreSQL de sistema nem ffmpeg. As ADR 0001 e 0002 e `docs/perguntas.md` descrevem o contorno. Produção não herda o Postgres embutido.
- D2-01 a D2-08 continuam abertas. Marco que dependa delas entrega o comportamento degradado especificado e deixa a integração real pendente.
- Tratar fake como conector homologado, ou publicar número de capacidade antes da D2-07, invalidaria o piloto.
- O npm 10 no `PATH` pode gerar lockfile divergente se o M1 não fixar o npm 11.
- A primeira subida do Postgres 18.6.0 embutido precisa de rede e de um artefato gzip. Se o binário não carregar na glibc local, os testes usam `CHATISP_TEST_DATABASE_URL` em vez de trocar o banco por um substituto sem RLS.

## M0 — Preparação

- Estado: pendente
- Entrega (C.1): ler a especificação; registrar escopo, contratos externos conhecidos, fluxo de telas e plano; fixar versões.
- Critério: este arquivo relaciona requisitos a marcos e testes; lacunas de ERP, Meta e Evolution ficam explícitas, sem API inventada.
- Dependências: nenhuma de código. A especificação já está em `docs/especificacao.md`.

### Requisitos deste marco

Não há linha V1 ou V1 simples entregue aqui. As linhas abaixo são o próprio trabalho de preparação. Estado de cada uma: pendente, até a revisão deste marco.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| M0-leitura | Preparação | Ler a especificação V4.1 e mantê-la como fonte única | Este plano cita A.6, B.4, B.8, C.1 e D.2 sem contradizer esses trechos |
| M0-plano | Preparação | Relacionar os 257 requisitos V1 e V1 simples aos marcos M1–M8 | Tabelas das seções seguintes e a conferência de contagem no fim |
| M0-contratos | Preparação | Registrar os contratos externos já conhecidos | Seção de contratos. Nomes internos do ERP separados de endpoint de fornecedor |
| M0-telas | Preparação | Registrar o fluxo das seis áreas | Seção de fluxo de telas |
| M0-versoes | Preparação | Fixar as versões que o host e o plano já conhecem | Tabela de versões. Graph API, Evolution e ERP ficam sem número inventado |
| M0-perguntas | Preparação | Registrar a D.2 com impacto e alternativa reversível | `docs/perguntas.md` |
| M0-adr | Preparação | Registrar as exceções de banco de teste e de build | ADR 0001 e ADR 0002, com problema, alternativas, custo, operação e reversibilidade |
| M0-chatwoot | Preparação | Fixar o checkout Community e a tabela de estudos | `docs/chatwoot-referencia.md`, commit `f428ad8e6f`, tabela vazia |

### Testes relacionados

Nenhum cenário B.8 executa no M0: ainda não há banco nem API. O relacionamento exigido pelo critério é o mapa da seção “Testes de aceite de B.8”: os 22 cenários têm marco responsável. A nota final de B.8 já vale: simulação não será apresentada como prova de Meta, Evolution ou ERP.

Revisão documental deste marco: as oito linhas M0 existem; M1–M8 existem; a contagem por seção fecha em 196 e 61; a D.2 está completa em `docs/perguntas.md`.

## M1 — Fundação e entrada no painel

- Estado: pendente
- Entrega (C.1): monorepo, Compose, PostgreSQL, storage de desenvolvimento, Makefile, CI, goose, sqlc, Huma, RLS, identidade, sessões, MFA, CSRF, tenants e navegação básica.
- Critério: dois tenants isolados em banco real; login, convite, reset e revogação; papéis testados; painel navegável, sem dado mock apresentado como real.
- Dependências: M0. Banco conforme ADR 0001. Comandos conforme ADR 0002. Compose versionado, sem execução obrigatória neste host.
- Storage: double local identificado até haver S3 compatível. Não é o armazenamento de produção.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 12-tenants | V1 | Contas (tenants) independentes | Um provedor equivale a um tenant, com fuso, status e limites próprios. |
| 12-isolamento | V1 | Isolamento de dados por tenant | RLS forçada, FK composta no domínio e tabelas globais com acesso por finalidade (B.4.4). |
| 12-usuarios-convites | V1 | Usuários e convites | Convite por e-mail ou link, token de uso único e prazo curto. |
| 12-papeis | V1 | Papéis Administrador, Supervisor e Atendente | Matriz fixa da seção 12, checada no backend, sem editor de permissões. |
| 12-login | V1 | Login por e-mail e senha | Senha com Argon2id, parâmetros versionados, sem senha reversível. |
| 12-reset-senha | V1 | Recuperação de senha | Token de uso único, validade curta e limite de tentativas. |
| 12-mfa | V1 | MFA com TOTP e códigos de recuperação | Obrigatório para superadmin e administradores; opcional para atendente. Segredo cifrado; códigos com hash. |
| 12-sessoes | V1 | Gestão de sessões e revogação | Cookie __Host-session, hash do ID, expiração por inatividade e absoluta. Admin revoga sessão. |
| 12-bloqueio-login | V1 | Bloqueio por tentativas de login | Força bruta no login e no MFA é limitada e auditada. |
| 12-https | V1 | HTTPS / TLS | Caddy na instalação prevista. Cookie Secure. Este host não sobe o proxy; o arquivo de deploy sim. |
| 12-credenciais-cifradas | V1 | Credenciais cifradas | AES-GCM, chave versionada fora do banco, AAD por tenant/entidade/campo. Valor armazenado não volta inteiro à tela. WhatsApp usa no M2; ERP no M6. |
| 12-fuso | V1 | Fuso horário por provedor | O tenant guarda o fuso. Relatórios do M7 agrupam por ele; horários de caixa usam o mesmo dado. |
| 12-auditoria | V1 | Auditoria de eventos críticos | Login, convite, reset e revogação neste marco. Transferência, exclusão, configuração e ação de ERP entram no marco que cria a ação, no mesmo registro. |
| 05-convite | V1 | Cadastro e convite de atendentes | Mesmo fluxo de 12-usuarios-convites, com papel e vínculo ao tenant. |
| 06-fuso | V1 | Fuso horário do provedor na operação da caixa | Mesma configuração de 12-fuso. A seção 06 não cria um segundo fuso. |
| 13-painel-web | V1 | Painel web com seis áreas | Rotas de Painel, Conversas, Contatos, Equipes, Relatórios e Configurações. Vazio honesto, sem mock apresentado como atendimento. |
| 13-responsiva | V1 | Interface responsiva | Desktop primeiro. Largura de supervisor em tela estreita continua utilizável nas áreas já entregues. |
| 13-pt-br | V1 | Português do Brasil | UI, mensagens e documentos em pt-BR. Identificadores de código em inglês. |
| 13-a11y | V1 | Acessibilidade básica | Teclado, foco, rótulos, contraste e estados de erro, carregamento e vazio nas áreas existentes. Marcos seguintes estendem a mesma barra. |
| 13-self-host | V1 | Instalação própria | Compose, Caddy e exemplo de ambiente sem segredo, para instância dedicada onde houver Docker. |
| 13-saas | V1 | Instalação SaaS compartilhada | Vários provedores no mesmo processo, isolados pelo banco. O teste do marco sobe dois tenants. |
| 13-compose | V1 | Docker Compose | Arquivos em deploy/ para hosts com Docker. Este host não executa Compose (ver perguntas). |
| 14-rls | V1 | Papéis PostgreSQL sem BYPASSRLS e com FORCE RLS | chatisp_app opera com políticas USING e WITH CHECK. Migração usa chatisp_owner. |
| 14-csrf | V1 | CSRF, Origin e separação de credenciais | API do painel com cookie e CSRF. Bearer da API pública não aceita cookie. WebSocket fecha a autenticação no M2, quando o socket existir. |
| 14-cache | V1 | Cache com chave por tenant, TTL e invalidação | Ristretto só para configuração, metadado de caixa e template. Permissão cacheada tem versão. Mensagem não vira segunda autoridade. |
| 14-isolamento | V1 | Isolamento por tenant em banco, cache, filas e anexos | Banco e cache neste marco. Filas no M2 e anexos no M4 usam a mesma regra, sem atalho novo. |
| 14-logs | V1 | Logs estruturados com tenant_id | slog em JSON. Sem corpo de mensagem, segredo ou CPF. |
| 14-migracoes | V1 | Migrações de banco versionadas | goose em SQL, papel de migração separado do runtime. |
| 14-jobs-restart | V1 | Jobs sobrevivem a reinício | Esquema River sobe com o processo. Um job de prova continua depois de reiniciar o worker. |
| 14-modos | V1 | Web e workers separáveis | Um binário chatisp, modos api, worker, migrate e all. |
| 14-provisionamento | V1 | Provisionamento de provedor sem alterar código | Superadmin cria o segundo tenant por operação de plataforma, não por patch. |

### Testes de aceite

- B8-03. Consulta e operação de domínio sem contexto de tenant são rejeitadas. A política no PostgreSQL também fecha.
- Critério do marco, em banco real: tenant A não lê tenant B; convite, login, reset de senha, MFA de administrador e revogação de sessão; papel de atendente não executa ação de administrador.
- O painel abre as seis áreas em pt-BR. Listas vazias não exibem conversa, contato ou métrica fictícios.
- `scripts/test.sh` e, onde houver make, `make test` rodam a mesma suíte. Lint no par equivalente.

## M2 — Atendimento vertical oficial

- Estado: pendente
- Entrega (C.1): caixa Meta, texto recebido, conversa e protocolo, assumir, responder, encerrar, outbox e idempotência, cursor e histórico na interface.
- Critério: o fluxo inteiro no painel com servidor Meta falso; teste real só quando houver credencial e autorização; duplicação, corrida, incerteza e isolamento cobertos.
- Dependências: M1. D2-03 para qualquer chamada real. A versão da Graph API é registrada nesse momento, a partir da documentação vigente, não antes.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 01-cloud-api | V1 | WhatsApp Business Cloud API | Driver oficial principal. Único serviço de mensagens ativo neste marco. Servidor falso nos testes. |
| 01-config-manual | V1 | Configuração manual da Cloud API | Token, App ID, App Secret, WABA ID e Phone Number ID. Sem Embedded Signup. |
| 01-multi-numeros | V1 | Múltiplos números por provedor | Cada número é uma caixa. O teste cobre duas caixas no mesmo tenant. |
| 01-multi-caixas | V1 | Múltiplas caixas por conta | Caixas restritas a números WhatsApp. |
| 01-escolher-conector | V1 | Escolher conector por número | connector_type imutável enquanto conectado. Neste marco só meta_cloud fica utilizável. Evolution permanece desligada até o M6. |
| 02-receber-texto | V1 | Receber texto | Webhook autenticado persiste o envelope antes do HTTP 200 e abre ou reutiliza a conversa. |
| 02-enviar-texto | V1 | Enviar texto | Compositor mínimo, outbox e status local distintos de entrega. |
| 02-status | V1 | Status enviada, entregue e lida | Eventos que o driver de fato reporta. Aceitação da API é diferente de entrega ao cliente. |
| 02-falha-envio | V1 | Falha de envio com motivo | Erro legível, sem segredo e sem transformar falha em sucesso. |
| 02-identidade-canal | V1 | Identidade do contato no canal | contacts.id interno. Identidade externa por conexão. Telefone não é obrigatório para criar o contato. |
| 03-caixa-compartilhada | V1 | Caixa de entrada compartilhada | A mensagem recebida aparece na fila da caixa, com protocolo. |
| 03-tempo-real | V1 | Atualização em tempo real | Evento durável na mesma transação e recuperação por cursor. |
| 03-historico | V1 | Histórico completo da conversa | A conversa aberta no painel mostra as mensagens persistidas, não um mock. |
| 03-estado-aberta | V1 | Estado Aberta | Transição conforme A.6, com versão da conversa. |
| 03-estado-aguardando | V1 | Estado Aguardando cliente | Só começa com resposta pública humana aceita pelo conector, se não houver mensagem posterior do cliente. |
| 03-estado-encerrada | V1 | Estado Encerrada | Data, ator, motivo e resultado. no_response não conta como solução confirmada. |
| 03-assumir | V1 | Assumir conversa | Responsável explícito. Duas pessoas não assumem a mesma versão. |
| 03-protocolo | V1 | Protocolo visível na conversa | Número local imutável, gerado na criação. Falha em comunicá-lo ao cliente fica pendente. |
| 03-eventos | V1 | Eventos do sistema na linha do tempo | Assumir e encerrar geram evento com ator, visível no histórico. |
| 04-encerrar-motivo | V1 | Encerrar com motivo | Motivo, resultado e síntese obrigatórios. Despedida opcional passa pela outbox. Falha da despedida não desfaz o encerramento local. |
| 11-websocket | V1 | Tempo real via WebSocket | Sessão, Origin permitida e autorização por assinatura. NOTIFY é só sinal. |
| 14-id-mensagem | V1 | Identificador único por mensagem | Chave (tenant_id, connector_id, external_message_id). |
| 14-webhook-duravel | V1 | Recebimento durável do webhook | Sem persistência, sem HTTP 200. Envelope multitenant fica na área de ingresso. |
| 14-assinatura-meta | V1 | Validação de assinatura do webhook da Meta | HMAC do corpo original com o App Secret daquele app. Assinatura inválida não é persistida como entrega aceita. |
| 14-dedup | V1 | Deduplicação de eventos separada da mensagem | Evento repetido não repete efeito. Vários status da mesma mensagem são preservados. |
| 14-outbox | V1 | Outbox com resultado incerto | Timeout e processo caído depois da chamada remota viram uncertain. Sem reenvio automático. |
| 14-fila | V1 | Fila assíncrona com River | River open-source. Job de domínio entra na mesma transação da mudança local. |
| 14-filas-limite | V1 | Filas por criticidade e limite por tenant | ingress, inbound e outbound existem neste marco. Reserva atômica limita um tenant barulhento. |
| 14-retry | V1 | Retentativas com backoff | Backoff, jitter e limite. 429/5xx seguem o contrato e a evidência, não só o código HTTP. |
| 14-dlq | V1 | Fila de falhas | Evento esgotado fica com motivo, visível à operação. Nada é descartado em silêncio. |
| 14-ordenacao | V1 | Ordenação consistente de mensagens | Timestamp confiável do driver, desempate estável, hora de ingestão separada. |
| 14-conciliacao | V1 | Conciliação de status | Status de mensagem ainda desconhecida fica pendente. Correlação usa a referência que o driver devolveu. |
| 14-concorrencia | V1 | Concorrência em atribuição e controlador | conversations.version. Duas assumições simultâneas: uma vence, a outra recebe conflito. |
| 14-rate-limit | V1 | Rate limit de envio por número | Lease com owner_id, lease_version e expires_at no PostgreSQL. Memória não é autoridade. |
| 14-realtime | V1 | Tempo real com reconexão e ressincronização | Carga inicial e next_cursor formam fronteira única. Lacuna pede ressincronização explícita. |
| 14-notify | V1 | LISTEN/NOTIFY como sinal | Contador por UPDATE ... RETURNING em realtime_tenant_cursors. BIGSERIAL sozinho não ordena commit. |
| 14-autorizacao | V1 | Autorização em cada endpoint e evento | Tenant, papel e, quando já existirem, setor, caixa, busca, anexo, exportação e socket. Escrita revalida a autorização vigente. |
| 14-alerta-token | V1 | Alerta de token do WhatsApp inválido | Falha de credencial avisa o admin e marca o canal degradado. O produto não promete prever toda revogação. |
| 14-limites-recursos | V1 | Limites de recursos por tenant | Reserva de concorrência com expiração. Um provedor não ocupa todos os workers. |

### Testes de aceite

- B8-01, B8-02, B8-04, B8-05, B8-09 e B8-15.
- A fronteira de despacho da outbox humana já existe aqui. O cenário B8-07, com bot, fecha no M5.
- Fluxo de tela: receber texto, ver protocolo e histórico, assumir, responder, encerrar com motivo. O servidor Meta do teste é identificado como falso.
- Assinatura inválida não recebe ACK de sucesso. Reinício do worker no meio do envio, sem prova de que a chamada não começou, produz uncertain.

## M3 — Webchat do atendente

- Estado: pendente
- Recorte M3A: implementações reunidas e provas dirigidas em [m3a-entrega.md](m3a-entrega.md); migração incremental 00012 permite revogar vínculo populado preservando histórico, com validação concorrente de atribuição. Fixture M2 usa configuração explícita de setor padrão e mantém suas assertions. Aceite aguarda revisão independente e gate final do controlador. Não equivale à conclusão deste marco.
- Entrega (C.1): filas, abas, filtros, busca, contato, transferências, notas, etiquetas, prioridade, leitura por usuário, rascunho, atalhos e colisão.
- Critério: Playwright cobre receber, assumir, transferir, responder e encerrar. Teclado, falhas e reconexão são verificados.
- Dependências: M2. Setores existem porque a transferência e a visão do atendente precisam deles. Round-robin fica no M5.

### Requisitos V1 e V1 simples

O aceite integral das linhas permanece pendente. O recorte M3A e suas evidências são acompanhados separadamente. A integração M3B de transferências, capacidade manual, abas, busca fulltext, notas, tags, prioridade, leitura e colisão tem roteiro e fixture opt-in em [m3b-entrega.md](m3b-entrega.md). Em 03/11/2026, `npm --prefix web run test:e2e:m3b` passou no Chromium `140.0.7339.186` provisionado: receber, assumir, transferir, responder, encerrar e revogação entre dois setores. Isso não conclui o marco. Auto-reabertura X, CPF/CNPJ autorizado com cifra/HMAC e os demais itens M3–M8 continuam no backlog aprovado. Revisão independente e o gate amplo final permanecem a cargo do controlador.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 02-nao-lidas | V1 | Contador de não lidas | Contador por atendente, separado dos recibos da Meta. |
| 03-abas | V1 | Abas Minhas, Não atribuídas e Todas | Visão padrão da lista. Atendente não vê setor alheio na aba Todas. |
| 03-ve-setores | V1 | Atendente vê só os setores dele | Interseção de setores e caixas. Estar atribuído não contorna o corte. |
| 03-historico-contato | V1 | Histórico por contato | Conversas anteriores na lateral, filtradas pela permissão. |
| 03-reabrir-manual | V1 | Reabrir manualmente | Novo ciclo, mesmo protocolo, se não houver outra conversa ativa do mesmo tenant, caixa e contato. |
| 03-transferir-atendente | V1 | Transferir para atendente | Valida setor, caixa e vaga. Histórico e autoria permanecem. |
| 03-transferir-setor | V1 | Transferir para setor | Remove responsável, abre intervalo de fila e revoga quem perdeu acesso. |
| 03-retirar-responsavel | V1 | Retirar responsável | Devolve à fila sem apagar o histórico. |
| 03-notas | V1 | Notas internas privadas | Nota não vai à Meta, não inicia espera e não conta como resposta humana. |
| 03-busca-nome | V1 | Busca por nome e telefone | Busca de contatos e conversas, isolada por tenant e por setor. |
| 03-busca-conteudo | V1 | Busca por conteúdo das mensagens | Full-text do PostgreSQL, plano medido no marco, sem promessa de escala. |
| 03-busca-protocolo | V1 | Busca por protocolo | O número citado pelo cliente encontra o atendimento autorizado. |
| 03-filtro-estado | V1 | Filtro por estado, setor e atendente | Filtros fixos, sem linguagem de consulta. |
| 03-filtro-caixa | V1 | Filtro por caixa / número | A lista restringe à caixa escolhida entre as permitidas. |
| 03-ordenacao | V1 | Ordenação por maior espera ou última mensagem | Padrão: maior espera primeiro. |
| 03-lida | V1 | Marcar como lida ou não lida | conversation_read_markers por usuário. Independente do status remoto. |
| 03-colisao | V1 | Detecção de colisão | Avisa quem está vendo ou digitando. Não bloqueia a resposta. |
| 03-reabrir-mensagem | V1 simples | Reabrir ao receber nova mensagem | Dentro da janela X, novo ciclo e o mesmo protocolo. Fora dela, nova conversa e novo protocolo. |
| 03-etiquetas | V1 simples | Etiquetas na conversa | Lista curta por provedor, sem hierarquia. Motivo principal é campo à parte. |
| 03-prioridade | V1 simples | Prioridade Normal e Urgente | Dois valores. Supervisor pode alterar. A fila urgente do M5 usa este campo. |
| 03-filtro-etiqueta | V1 simples | Filtro por etiqueta e período | Filtro fixo, sem AND/OR livre. |
| 04-emojis | V1 | Emojis no compositor | Inserção de emoji na mensagem de texto já entregue no M2. |
| 04-rascunho | V1 | Rascunho por conversa | Isolado por usuário, tenant e conversa. Trocar de conversa não perde o texto. Logout limpa o armazenamento local. |
| 04-notif-ui | V1 | Notificações na interface | Conversa nova e mensagem nova ficam visíveis sem depender do ERP. |
| 04-notif-som | V1 | Notificações sonoras | Som distinto para conversa nova e para mensagem. |
| 04-notif-navegador | V1 | Notificações do navegador | Notification API com permissão explícita. Sem promessa de push com o navegador fechado. |
| 04-formatacao | V1 simples | Formatação de texto do WhatsApp | Negrito e itálico na sintaxe do canal. Sem editor rico genérico. |
| 04-atalhos | V1 simples | Atalhos de teclado essenciais | Enviar, próxima conversa, nota e buscar. Playwright cobre teclado. |
| 04-prefs-notif | V1 simples | Preferências de notificação por usuário | Som, navegador ou silencioso. Silenciar conversa continua fora do produto. |
| 05-equipes | V1 | Equipes / setores | O provedor cria setores. Transferência e visão do atendente usam essa lista. |
| 05-varios-setores | V1 | Atendente em vários setores | A visão é a união dos setores do membership, ainda cortada pelas caixas permitidas. |
| 05-acesso-caixa | V1 | Acesso por caixa de entrada | Vínculo por tenant, setor e número. A lista de conversas obedece ao vínculo. |
| 07-criar-receber | V1 | Criar contato ao receber WhatsApp | Resolve identidade externa para ID interno. Não mescla só por nome ou telefone. |
| 07-criar-manual | V1 | Criar contato manualmente | Nome e telefone. Conversa ativa ainda nasce pelo canal ou pelo template do M4. |
| 07-nome-telefone | V1 | Nome, telefone e identificador do canal | Telefone é atributo opcional normalizado. O identificador externo fica em contact_channel_identities. |
| 07-editar | V1 | Editar contato | Alteração fica registrada. Não reescreve mensagem já enviada. |
| 07-historico | V1 | Histórico de conversas do contato | A lateral lista ciclos anteriores autorizados. |
| 07-bloquear | V1 | Bloquear contato | Spam e abuso. Mensagem nova de contato bloqueado não entra na distribuição. |
| 07-mesmo-numero | V1 | Mesmo número em vários provedores | Registros separados por tenant. Não há mescla entre provedores. |
| 07-email | V1 simples | E-mail opcional do contato | Só o campo. Não abre canal de e-mail. |
| 07-avatar | V1 simples | Avatar | Exibido só quando o WhatsApp fornecer. Sem upload de avatar como recurso de produto. |
| 07-notas | V1 simples | Notas do contato | Um campo de observações, separado das notas da conversa. |
| 07-busca | V1 simples | Busca, filtro e ordenação de contatos | Nome, telefone, CPF/CNPJ quando houver, e últimos contatos. Busca de documento é exata, por HMAC. |
| 07-cpf | V1 simples | CPF/CNPJ do titular | Opcional, cifrado, mascarado, só por fluxo autorizado. Não é obrigatório para conversar e não autentica. |
| 12-acesso-setor | V1 | Acesso por setor e caixa | A mesma regra vale na lista, na API, no histórico e no evento. |
| 12-mascara-cpf | V1 | Mascaramento de CPF na tela | A UI mostra máscara. Log e busca não carregam o documento em claro. |
| 13-lateral | V1 | Painel lateral do contato na conversa | Contato, histórico e observações. Bloco de ERP permanece indisponível até o M6. |
| 13-atalhos | V1 simples | Atalhos de teclado essenciais na interface | Mesmos atalhos de 04-atalhos, aplicáveis na área Conversas. |
| 13-notif-navegador | V1 | Notificações do navegador na interface | Mesma permissão de 04-notif-navegador. A linha da seção 13 não cria um segundo canal. |
| 14-fulltext | V1 | Busca full-text por tenant | PostgreSQL com unaccent e pg_trgm onde o campo pedir. Índice não atravessa tenant nem setor. |

### Testes de aceite

- B8-16 e B8-17.
- Playwright: receber, assumir, transferir para atendente e para setor, responder e encerrar.
- Teclado nos atalhos essenciais. Queda de rede preserva rascunho e não mostra envio aceito. Reconexão reutiliza a chave e recupera o cursor.
- Atendente de outro setor não recebe a conversa na busca, no histórico, no evento nem no cache depois da transferência.

## M4 — Canal e produtividade completos

- Estado: pendente
- Entrega (C.1): mídias da V1, gravação de áudio, templates e WABA, janela oficial, consentimento, respostas prontas, saúde do canal e uso reportado.
- Critério: uploads e URLs seguros, conversão limitada, janela revalidada no envio e estados remotos corretos.
- Dependências: M2 para o driver e a outbox. M3 para o compositor já usado pelo atendente. ffmpeg ainda não está neste host; a evidência da conversão espera o binário. D2-05 impede qualquer valor monetário.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 02-receber-imagens | V1 | Receber imagens | A mídia é baixada antes de a URL remota expirar e aparece na conversa. |
| 02-enviar-imagens | V1 | Enviar imagens | Tipos e limites do driver oficial. Validação do arquivo antes do envio. |
| 02-receber-documentos | V1 | Receber documentos e PDF | Guarda no storage do tenant, com MIME conferido. |
| 02-enviar-documentos | V1 | Enviar documentos e PDF | Anexo de saída com URL assinada e temporária. |
| 02-receber-audio | V1 | Receber áudio e nota de voz | Player na conversa a partir do objeto guardado. |
| 02-enviar-audio | V1 | Enviar arquivo de áudio | O driver recebe o formato combinado depois da conversão limitada, quando ela for necessária. |
| 02-gravar-audio | V1 | Gravar áudio no painel | MediaRecorder no navegador. Conversão para OGG/Opus no worker, com ffmpeg limitado. |
| 02-receber-video | V1 | Receber vídeo | Preview e download seguro. Enviar vídeo continua V2. |
| 02-receber-localizacao | V1 | Receber localização | A mensagem mostra a localização recebida. Sem motor de rede. |
| 02-citacao | V1 | Responder mensagem específica | Campo de contexto do driver. A citação aponta para mensagem do mesmo tenant. |
| 02-janela | V1 | Indicador da janela de 24 horas | Só na Cloud API. A conversa Web, quando existir, não mostra indicador oficial. |
| 02-biblioteca-templates | V1 | Biblioteca de templates aprovados | Somente Meta, por WABA. Caixa acessa os templates autorizados daquela WABA. |
| 02-sync-templates | V1 | Sincronização de templates | Consulta à WABA pelo driver oficial. Web não simula essa sincronização. |
| 02-envio-template | V1 | Envio individual de template | Mesma idempotência e autorização do compositor. Fora da janela, mensagem livre é recusada. |
| 02-download-midia | V1 | Download e guarda da mídia recebida | A URL da Meta não é a cópia durável. O objeto fica no storage do tenant. |
| 02-consentimento | V1 | Consentimento e opt-in | Finalidade, canal, origem, evidência, data e revogação. Opt-out cancela proativas pendentes. Contato criado não implica consentimento. |
| 02-vcard | V1 simples | Receber contato (vCard) | Exibe nome e número. Não cria mescla automática com o ERP. |
| 02-figurinhas | V1 simples | Figurinhas recebidas | Exibe a figurinha. Enviar figurinha fica fora. |
| 02-reacoes | V1 simples | Reações recebidas | Exibe na mensagem citada. Enviar reação fica V2. |
| 02-interativos | V1 simples | Botões e listas interativas | Capacidade do driver oficial, com fallback em texto numerado. O menu do M5 consome esta capacidade. Sem editor visual. |
| 02-saude-numero | V1 simples | Saúde e qualidade do número | Qualidade e erros essenciais visíveis ao admin. Sem painel de tarifa. |
| 02-limites-numero | V1 simples | Limites de mensagens do número | Tier atual para diagnóstico. O limite local de envio continua sendo o lease do M2. |
| 04-compositor | V1 | Compositor de mensagem | Texto, anexos, emoji e indicador de janela na Cloud API. Rascunho continua local até a aceitação. |
| 04-drag-colar | V1 | Arrastar arquivo e colar imagem | O anexo entra na mesma validação de upload. |
| 04-preview | V1 | Pré-visualização de anexos | Imagem, PDF, áudio e vídeo recebidos, sem executar HTML ou SVG no contexto do painel. |
| 04-download-seguro | V1 | Download seguro de anexos | Autorização antes da URL assinada. A URL expira e não fica pública. |
| 04-respostas-prontas | V1 | Respostas prontas compartilhadas | Admin e supervisor gerem. Atendente insere. Não há resposta privada neste lançamento. |
| 04-atalho-barra | V1 | Atalho / para respostas prontas | Exemplo: /reiniciar abre a lista e insere o texto no rascunho. |
| 04-assinatura | V1 simples | Assinatura do atendente | Prefixo configurável Nome: por provedor. Desligada, a mensagem sai sem o prefixo. |
| 04-variaveis | V1 simples | Variáveis simples nas respostas prontas | Nome do cliente, nome do atendente e protocolo. Sem variável arbitrária nem lógica. |
| 07-iniciar-template | V1 | Iniciar conversa com contato por template | Só Cloud API, com janela e consentimento revalidados no despacho. Cria conversa e protocolo novos quando não há conversa ativa. |
| 14-download-midia | V1 | Download da mídia no worker | Mesma obrigação de 02-download-midia, com timeout, limite de bytes e falha visível. |
| 14-s3 | V1 | Anexos em storage S3 compatível, por tenant | aws-sdk-go-v2. Chave com namespace opaco do tenant. Neste host o teste usa double identificado; produção usa destino S3 validado. |
| 14-url-assinada | V1 | Acesso a anexos com URL assinada e expirável | Sem link público permanente. A autorização é revista na emissão. |
| 14-upload | V1 | Validação de upload | Tipo real, tamanho, duração e nome seguro. |
| 14-ffmpeg | V1 | Conversão de áudio com limites | ffmpeg em arquivo local, com teto de duração, tamanho, CPU, memória e tempo. Sem URL arbitrária na conversão. ffmpeg ainda ausente neste host: o marco registra a evidência onde o binário existir. |
| 14-uso-meta | V1 simples | Contabilização de uso reportado pela Meta | Categoria, indicador de cobrança ou desconhecido. Sem valor em dinheiro. Agregado reconstruível, sem dupla contagem. |

### Testes de aceite

- B8-13. A janela que vence com a mensagem ainda na fila impede o envio livre. Template permitido continua sendo outra operação, também revalidada.
- Upload com tipo disfarçado é recusado. URL assinada expira e exige autorização. HTML e SVG recebidos não executam no painel.
- Uso reportado guarda desconhecido quando a Meta não manda categoria ou indicador. O agregado não conta o mesmo evento duas vezes.
- Conversão de áudio recusa entrada acima do limite e não recebe URL para o ffmpeg buscar na rede.

## M5 — Distribuição, automações e bot

- Estado: pendente
- Entrega (C.1): round-robin, vagas atômicas, presença, horários, plantão, triagem, adiamento, inatividade e gancho de IA com handoff.
- Critério: o cliente não fica preso no menu nem no bot; a tomada humana invalida envios pendentes; job antigo não fecha a conversa atual.
- Dependências: M3 para setores, prioridade e etiquetas. M4 para botões interativos, quando o driver oficial os tiver; o texto numerado é o fallback e não espera mídia. Endpoint real de IA só com autorização. Até lá o gancho de teste é identificado como simulação.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 03-triagem | V1 simples | Estado Em triagem | O estado Pendente da referência vira Em triagem. Controlador menu ou bot, separado do estado. |
| 03-adiar | V1 simples | Adiar com data e hora | Motivo, destino e snoozed_until. Não é estado e não encerra. Mensagem atual do cliente tira o adiamento. |
| 04-presenca | V1 | Presença disponível, ocupado e offline | Agrega abas. Fechar uma aba não marca offline quem segue em outra. Heartbeat no PostgreSQL. |
| 05-ativar-desativar | V1 | Ativar e desativar atendente | Desativado mantém histórico, perde acesso e tem as conversas reatribuídas. |
| 05-responsavel-unico | V1 | Responsável único por conversa | Um membership atribuído. Não é sinônimo de controlador. |
| 05-atribuicao-manual | V1 | Atribuição manual | Assumir, transferir e devolver usam a mesma reserva de vaga da atribuição automática. |
| 05-atribuicao-auto | V1 | Atribuição automática | Ligada por setor. Sem vaga, a conversa fica na fila, visível a quem pode ver o setor. |
| 05-round-robin | V1 | Round-robin | Entre disponíveis abaixo do limite. A reserva da vaga ocorre na mesma transação da escolha. |
| 05-disponibilidade | V1 | Considerar disponibilidade | Offline e ocupado não recebem conversa nova. |
| 05-prioridade-espera | V1 | Prioridade por tempo de espera | Urgentes antes de normais. FIFO dentro da prioridade. Alerta de normal em espera excessiva. |
| 05-reatribuir-desativado | V1 | Reatribuir conversas de atendente desativado | As conversas voltam à fila ou a outro atendente com vaga. O histórico conserva o ator anterior. |
| 05-supervisor-filas | V1 | Supervisor vê filas dos setores | Todos os setores do tenant, conforme a seção 12. Sem acesso a outro tenant. |
| 05-supervisor-altera | V1 | Supervisor altera responsável e prioridade | Não configura número nem integração. |
| 05-limite-vagas | V1 simples | Limite de conversas por atendente | Abertas e aguardando cliente consomem vaga. Adiadas, encerradas e triagem de bot não consomem. Sem vaga, transferência manual conflita. |
| 05-apos-transferencia | V1 simples | Comportamento após transferência | Para atendente: valida acesso e capacidade. Para setor: remove responsável e volta à fila. |
| 05-desatribuir-offline | V1 simples | Desatribuir quem fica offline | Só depois da tolerância configurável. Queda curta não tira conversas. |
| 05-ocupacao | V1 simples | Indicador de ocupação | Vagas usadas e limite. Abertas, aguardando e adiadas aparecem separadas. |
| 06-horario | V1 | Horário de atendimento por dia da semana | Por caixa. Não há escala por atendente. |
| 06-fora-expediente | V1 | Resposta fora do expediente | Texto por caixa, com orientação de plantão quando houver. |
| 06-setor-padrao | V1 | Setor padrão por caixa | Destino quando não há menu ou quando o menu estoura as tentativas. |
| 06-atribuicao-chegar | V1 | Atribuição automática ao chegar | Depois da triagem ou do setor padrão, usa as regras da seção 05. |
| 06-menu | V1 | Menu de triagem | Interativo quando o driver permitir; texto numerado como fallback. Sempre há saída para humano. Conteúdo imprevisto tem limite curto de tentativas. |
| 06-inatividade | V1 | Encerramento automático por inatividade | Só depois de resposta pública humana confirmada. Aviso e, depois, encerramento com no_response. Revalida estado, posse, adiamento e mensagem nova. |
| 06-retorno-adiada | V1 | Retorno de conversa adiada à fila | No prazo, tenta o antigo responsável se elegível; senão volta à fila. Não encerra. |
| 06-plantao | V1 simples | Setor de plantão fora do expediente | Opcional por caixa. Em vez de só responder, encaminha ao setor de plantão. |
| 06-boas-vindas | V1 simples | Mensagem de boas-vindas | Uma por caixa, concisa, com o número do protocolo. Falha de envio deixa o protocolo pendente de comunicação. |
| 06-ninguem-disponivel | V1 simples | Resposta quando ninguém está disponível | Mensagem configurável. A conversa permanece na fila. |
| 06-gancho | V1 simples | Gancho para bot ou IA externa | Um webhook assinado por caixa. HTTP 200 significa recebimento, não resolução. |
| 06-etiqueta-menu | V1 simples | Etiqueta automática por opção do menu | A opção financeiro pode gravar o motivo. Sem autoetiqueta por palavra-chave. |
| 06-gatilhos | V1 simples | Gatilhos fixos | Conversa criada, mensagem e mudança de estado, internos ao núcleo, sem editor de regras. |
| 08-historico-bot | V1 | API de histórico para o bot | Limitada à conversa e às categorias autorizadas. Nota interna e outro setor não saem por associação ao contato. |
| 08-resposta-bot | V1 | Resposta como bot identificado | Token com escopo e controller_epoch vigente. API idempotente. Epoch antiga recebe conflito. |
| 08-handoff | V1 | Handoff para humano | Motivo, nota e setor autorizado. Transferência atômica. Indisponibilidade do receptor não impede a tomada humana. |
| 08-interruptor | V1 | Interruptor do bot por caixa e horário | Desligar incrementa a epoch, cancela envios automáticos ainda não despachados e devolve as conversas do bot à fila humana. |
| 08-aviso-assistente | V1 | Aviso de assistente virtual | A conversa informa que há assistente e oferece humano. |
| 08-humano-assume | V1 | Humano assume a qualquer momento | Incrementa controller_epoch e cancela pendências da posse antiga na mesma transação. Pedido de humano no menu vai direto à fila. |
| 08-timeout-bot | V1 | Timeout e falha do bot | bot_deadline devolve à fila humana. Não repete boas-vindas nem perde histórico. |
| 08-autorizacao-acao | V1 | Autorização por ação do bot | Tenant, caixa, conversa e operação revalidados no servidor. Credencial externa não fica protegida só por uma aprovação local. |
| 08-entregar-ia | V1 simples | Entregar conversa à IA externa | Um gancho por caixa, prazo e retentativa idempotente do handoff. Sem modelo hospedado no chat. |
| 08-aprovacao | V1 simples | Aprovação humana de ação sensível já prevista | Liga operação, parâmetros, hash, prazo e aprovador. Uso único. Mudança de parâmetro invalida. Não cria desbloqueio, OS nem função financeira. |
| 11-gancho | V1 simples | Gancho de bot na API | Mesma capacidade de 06-gancho e 08-entregar-ia. A linha da seção 11 não cria um segundo bot por caixa. |

### Testes de aceite

- B8-06, B8-07, B8-08, B8-10, B8-11 e B8-12.
- A classificação usada por B8-21 nasce aqui: encerramento só pelo bot não preenche primeira resposta humana. A conta do relatório fecha no M7.
- Parte de B8-18 já vale para a IA: gancho indisponível devolve à fila humana e o chat oficial segue. O trio ERP, IA e Evolution fecha no M6.
- Menu com entrada inválida oferece humano depois do limite. Cliente que pede humano não permanece no bot.
- Job de inatividade que perde a corrida para uma mensagem nova não encerra. Aviso incerto não dispara o encerramento que dependia dele.

## M6 — Integração existente e modalidade Web

- Estado: pendente
- Entrega (C.1): API pública e webhooks, contexto externo e iframe, adaptador fino do ERP utilizado e Evolution condicionada.
- Critério: o ERP não bloqueia o atendimento; operação externa não repete efeito às cegas; contratos diferem por driver; a queda de um conector fica isolada. Web permanece desligada sem os requisitos da D2-04.
- Dependências: D2-01 e D2-02 para declarar capacidade real de ERP. D2-04 para ligar Web. Sem esses dados, o marco entrega contrato, fake identificado, flag desligada e a API do próprio chat.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 01-evolution | V1 simples | WhatsApp Web por Evolution | Conector condicionado. Flag desligada até D2-04. QR efêmero só para admin, sem log persistente. Queda da Evolution afeta só as caixas desse conector. |
| 07-id-erp | V1 | ID do cliente no ERP | Referência opaca. Não cria cadastro comercial no chat. |
| 07-vinculo-erp | V1 | Vínculo do contato a um ou mais clientes ou contratos | O mesmo número pode ter casa e comércio. O atendente escolhe o vínculo da conversa. Telefone não escolhe sozinho. |
| 08-referencia-erp | V1 | Referência ao cliente e ao contrato | IDs externos no contato e seleção por conversa. A consulta é sob demanda. |
| 08-contexto-painel | V1 | Contexto do painel externo | Escopo por usuário, tenant e conversa. Troca curta, sem segredo de ERP ou WhatsApp na URL. |
| 08-registro-acao | V1 | Registro de ação e falha na linha do tempo | Ator, referência externa, resultado e ID da operação. Sem copiar detalhe financeiro para tabela local. |
| 08-permissao-erp | V1 | Permissão de ação de ERP por papel e setor | Backend revalida antes de executar pendência. Atendente só no setor permitido. |
| 08-contrato-minimo | V1 simples | Contrato mínimo de capacidades | Os cinco nomes internos de B.4.8. Resultado tipado. Capacidade ausente volta como indisponível. |
| 08-conector-erp | V1 simples | Conector do ERP existente | Um adaptador. Fake só para teste, identificado como simulação. Sem SGP e IXC ao mesmo tempo. |
| 08-identificar | V1 simples | Identificar cadastro por referência autorizada | Busca e confirmação do vínculo. Identificação não é autenticação. |
| 08-verificacao-cliente | V1 simples | Verificação de autorização do cliente | Reutiliza o mecanismo do provedor quando D2-02 existir. Até lá a ação sensível permanece oculta. |
| 08-resumo | V1 simples | Resumo do cliente na lateral | Campos mínimos autorizados, com hora da consulta. ERP fora mostra indisponível e o chat segue. |
| 08-faturas | V1 simples | Faturas em aberto | Exibição transitória se a API real suportar. Sem tabela local de contas a receber. |
| 08-segunda-via | V1 simples | Obter e enviar segunda via ou Pix | O ERP fornece o artefato. O chat envia como mensagem ou anexo e registra a ação. Timeout ambíguo não repete o efeito. |
| 08-abrir-cadastro | V1 simples | Abrir cadastro no ERP | Link para o ambiente já existente, sem credencial e sem CPF na URL. |
| 08-iframe | V1 simples | Painel lateral externo em iframe | Página já existente, origem permitida, sandbox e postMessage com origem verificada. Se a página proibir incorporação, abre fora, sem contornar a proteção. |
| 08-protocolo-erp | V1 simples | Associar protocolo do ERP | external_protocol é referência adicional. Não substitui o protocolo local já comunicado. |
| 11-api-conversas | V1 | API REST de conversas e mensagens | Enviar, mudar estado, atribuir e ler histórico com a mesma idempotência e autorização do painel. |
| 11-api-contatos | V1 | API REST de contatos | Criar, atualizar e gravar a referência externa. Sem espalhar documento em claro. |
| 11-api-template | V1 | API REST de envio de template | Somente driver oficial. Mesmas regras de janela, autorização e idempotência da interface. |
| 11-tokens | V1 | Tokens de API por provedor | Opacos, com hash, escopo, rotação e revogação. O valor aparece uma vez na emissão. |
| 11-rate-limit | V1 | Rate limit da API pública | Por token e por tenant. A API do painel permanece no cookie. |
| 11-openapi | V1 | Documentação OpenAPI | Gerada do backend Huma. O cliente TypeScript do painel já nasce no M1; esta linha fecha quando a API pública está no contrato publicado. |
| 11-webhook-conversa | V1 | Webhooks de conversa criada, atribuída e encerrada | HMAC, ID estável do evento, retentativa e orientação de replay para o receptor. |
| 11-webhook-mensagem | V1 | Webhook de mensagem criada | Mesma entrega durável. Falha do destino não desfaz a mensagem local. |
| 11-api-cadastros | V1 simples | API de atendentes, setores e caixas | API pública lê. Escritas de administração ficam na API autenticada do painel. |
| 11-dashboard-app | V1 simples | Painel lateral externo (Dashboard App) | Mesma capacidade de 08-iframe. Não é uma segunda interface de ERP construída aqui. |
| 11-conector-erp | V1 simples | Conector com o ERP na lista de integrações | Mesma capacidade de 08-conector-erp. A pendência de D2-01 continua visível. |
| 14-circuit-breaker | V1 | Timeout, concorrência e circuit breaker no ERP | sony/gobreaker por conexão. Timeout do ERP não segura o pedido do chat sem limite. Handler e bot não chamam o adaptador direto. |
| 14-observabilidade | V1 | Observabilidade e condições por conector | Número ou instância ligados a um tenant, credencial segregada, desconexão visível, aceite explícito para Web e flag para desligar. |

### Testes de aceite

- B8-14, B8-18 e B8-19.
- ERP lento ou parado: a resposta no chat segue, a lateral mostra indisponível e nenhuma ação é confirmada sem evidência.
- Ação com efeito externo usa chave local. Resposta perdida depois do sucesso remoto vira conciliação ou incerto.
- Dois drivers: teste de contrato da Cloud API e, com a flag ainda desligada, teste de que a caixa Web não envia. Eco e histórico da Evolution, no double, não criam automação.
- Iframe só aceita origem configurada. URL de ERP, bot e webhook passa pela proteção de SSRF de B.4.15.

## M7 — Gestão do atendimento

- Estado: pendente
- Entrega (C.1): painel, relatórios V1 e CSV, histórico estruturado, retenção, exportação e exclusão, limites e superadmin.
- Critério: métricas com cenários calculados; acesso correto por papel; a exportação do chat não é apresentada como histórico completo do ERP.
- Dependências: eventos e intervalos gravados desde o M2 e o M5. D2-06 antes de ligar expurgo em dado real. D2-08 para os números comerciais de suspensão e de ingresso; o comportamento qualitativo já é testado.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 09-painel-rt | V1 | Painel em tempo real | Na fila agora, em atendimento, atendentes online e maior espera, a partir de eventos persistidos. |
| 09-sem-responsavel | V1 | Conversas sem responsável | Destaque no painel, coerente com a fila. |
| 09-total-periodo | V1 | Total de conversas por período | Filtro separa conversa, ciclo e mensagem. Amostra visível. |
| 09-estados | V1 | Abertas, aguardando e encerradas | Encerradas separadas por resultado. Adiadas visíveis sem virar estado novo. |
| 09-primeira-resposta | V1 | Tempo da primeira resposta humana | Média e mediana. Nota, falha, menu e bot ficam de fora. Sem humano, o valor é nulo, não zero. |
| 09-tempo-encerramento | V1 | Tempo até encerramento, total e operacional | Operacional desconta uma vez a união de aguardando cliente e adiada. Relógio corrido, inclusive fora do expediente, com essa ressalva no relatório. |
| 09-espera-fila | V1 | Tempo de espera na fila | Da entrada na fila até atribuição, cancelamento ou fechamento. Transferência de setor abre novo intervalo. |
| 09-volume-setor | V1 | Volume por setor | Respeita o setor registrado nos intervalos, não só o setor final. |
| 09-volume-atendente | V1 | Volume e tempos por atendente | Intervalos reais de responsabilidade. Mensagem pelo autor. Encerramento pelo ator. O último atendente não herda o tempo todo. |
| 09-volume-etiqueta | V1 | Volume por etiqueta e motivo | Motivo principal entra no total. Etiquetas adicionais são identificadas como sobreposição. |
| 09-mapa-calor | V1 | Mapa de calor por hora e dia da semana | Agrupamento no fuso do provedor. Horários armazenados em UTC. |
| 09-agrupamento | V1 | Agrupamento por dia, semana e mês | Média, mediana e tamanho da amostra. Não aplicável fica fora do denominador. |
| 09-painel-saas | V1 | Painel do dono do SaaS | Métricas técnicas e de uso de todos os provedores, sem leitura de conversas. Acesso auditado. |
| 09-volume-mensagens | V1 simples | Volume de mensagens | Contagem no período, distinta de contagem de conversas. |
| 09-volume-caixa | V1 simples | Volume por caixa / número | Filtro sobre os relatórios acima, limitado às caixas autorizadas. |
| 09-csv | V1 simples | Exportação CSV | Mesmas definições da tela. Não é um construtor de BI. |
| 09-alerta-espera | V1 simples | Alerta simples de espera | Destaque na fila e no painel depois de X minutos sem resposta. Sem reatribuição automática, que é V2. |
| 12-retencao | V1 simples | Política de retenção de mensagens e anexos | Prazo por provedor depois de D2-06. Expurgo automático permanece desligado até esse perfil. Registros de guarda obrigatória não são apagados antes do prazo. |
| 12-exportacao-contato | V1 simples | Exportação e exclusão de dados de um contato | Pedido do titular, executado pelo admin. A exclusão preserva o mínimo legal e registra deletion_records para reaplicar depois do restore. |
| 12-historico-demandas | V1 simples | Histórico de demandas exportável | Protocolo, datas, motivo, síntese, encaminhamento e autoria. Referência externa quando houver. Não é o histórico completo do ERP. |
| 12-logo | V1 simples | Logo e nome do provedor | Só logo e nome na interface. Sem white label, cor de marca nem domínio próprio. |
| 12-limites-plano | V1 simples | Limites por plano | Atendentes, números e storage. Excesso bloqueia cadastro ou upload novo e não apaga histórico. Mídia de entrada segue a reserva operacional de B.4.16. |
| 12-superadmin | V1 | Superadmin do SaaS | Opera tenants e vê o painel técnico. Sem acesso livre às conversas. Toda entrada extraordinária é auditada. |
| 12-criar-suspender | V1 | Criar e suspender provedor | Suspender é reversível e não apaga dados. O comportamento de suspensão segue B.4.16. |
| 14-retencao | V1 simples | Retenção e descarte de dados | Classes diferentes para conteúdo, mídia, envelope, log e job. Cópias temporárias e índices elegíveis entram no descarte. Backup imutável não é prometido como apagado na hora. |
| 14-suspensao | V1 | Suspensão reversível e auditada | Bloqueia acesso operacional e envio. Webhook na suspensão fica retido com limite, sem automação. Reativação não dispara ação vencida. |

### Testes de aceite

- B8-20. Ciclo das 8h às 12h com 3h de espera: 4h totais e 1h operacional.
- B8-21. Conversa resolvida só pelo bot: primeira resposta humana nula e resultado explícito, separado de solução humana.
- Atendente não abre relatório de outro setor. Supervisor não altera integração. Superadmin não lê o corpo das conversas pelo painel da plataforma.
- Exportação de demandas traz protocolo, datas, motivo, síntese, encaminhamento e autoria. O rótulo da exportação não a chama de histórico completo do provedor.
- Suspensão bloqueia envio e mantém o dado. Expurgo automático não roda enquanto D2-06 estiver aberta.

## M8 — Validação do piloto

- Estado: pendente
- Entrega (C.1): carga, falhas, restore, alertas, revisão de segurança e guia de instalação.
- Critério: evidências reais e limitações registradas; fluxo de atendimento validado por operador; critérios de C.2 cumpridos na parte já possível.
- Dependências: M1–M7 no que for oferecido. D2-07 antes de publicar número. D2-03 antes de chamar a abertura comercial de oficial. Web continua pendente sem D2-04. ERP real continua pendente sem D2-01.

### Requisitos V1 e V1 simples

Estado de todas as linhas: pendente.

| ID | Prioridade | Requisito | Evidência |
| --- | --- | --- | --- |
| 14-backup | V1 | Backup e restauração testada | Banco, anexos, configuração necessária e chaves. Restore isolado, referências consistentes e exclusões reaplicadas. |
| 14-carga | V1 | Teste de carga multi-tenant | k6 só depois do perfil da D2-07. Mede p95, CPU, RAM e I/O. Número estimado não entra no relatório como se tivesse sido executado. |
| 14-metricas-alertas | V1 | Métricas e alertas | Idade de job, erro por fila e conector, atraso de webhook, incerteza, WebSocket, pool, storage e duração de restore. Destino operacional definido. Sem segredo, CPF ou texto em label. |

### Entregas de C.1 e C.2 que não são linha nova da matriz

Estado: pendente. Não entram na contagem 196 + 61.

| ID | Requisito | Evidência |
| --- | --- | --- |
| M8-guia | Guia de instalação | Passos de Compose, configuração sem segredo real, migração e limites deste ambiente de desenvolvimento. |
| M8-revisao-seguranca | Revisão de segurança do piloto | RLS, CSRF, webhook, SSRF de URL de saída, mídia ativa e vazamento entre tenants reexecutados e registrados. |
| M8-piloto | Validação do piloto com operador | Um operador percorre receber, assumir, responder, transferir e encerrar no navegador. O registro separa simulação de validação real e lista o que a D.2 ainda segura. |

### Testes de aceite

- B8-22. Restore de banco, anexos e chaves em ambiente isolado. Uma exclusão feita depois do backup é reaplicada antes de reabrir o serviço.
- B8-18 repetido como ensaio de falha: ERP, IA e Evolution fora; a caixa oficial segue.
- Carga somente com o perfil escrito da D2-07. Sem perfil, o relatório diz que a carga não foi aceita como capacidade.
- Alertas têm destino. Revisão de segurança cobre os contratos da seção “Contratos inegociáveis” do prompt de C.3, com evidência de teste ou com pendência nomeada.
- Um operador percorre o fluxo principal no navegador. O registro separa o que foi simulado do que foi validado em sistema real.

## Pontos de extensão da V2 que a V1 já deixa preparados

Só estes, porque a especificação pede o ponto e não o recurso:

- Tokens de cor no painel, para o modo escuro da V2. O modo escuro em si não é ligado.
- Vínculo usuário–tenant no modelo, para um usuário em mais de um provedor na V2. A interface da V1 opera um tenant por vez.
- Capacidades declaradas no driver WhatsApp, para a Cloud API e, se um dia for ligada, a Evolution. Canal API genérico, widget, e-mail e outros mensageiros continuam fora.

Não há extensão para ERP financeiro, OS, rede, campanha, RAG ou Kubernetes.

## Fora do lançamento

A.5 conta 51 linhas V2 e 131 linhas “Não fazer” nas seções 01 a 14. Elas não são agendadas em M0–M8. Em particular continuam fora: widget e SDK público, apps nativos, campanhas, SLA contratual, CSAT, Captain e qualquer IA nativa, Help Center, SGP e IXC como segundo adaptador, geração de boleto ou Pix, desbloqueio, OS, consulta de rede, Redis ou Valkey no núcleo, Kubernetes, Kafka, RabbitMQ, NATS, Next.js e ORM.

A abertura comercial de C.2 não é um marco extra. Ela só acontece depois do M8, com evidência da modalidade que a oferta de fato incluir.

## Contagem para conferência

Linhas V1 e V1 simples nas tabelas M1–M8, por seção da matriz:

| Seção | V1 | V1 simples | Total |
| --- | ---: | ---: | ---: |
| 01 | 5 | 1 | 6 |
| 02 | 22 | 6 | 28 |
| 03 | 25 | 6 | 31 |
| 04 | 13 | 5 | 18 |
| 05 | 14 | 4 | 18 |
| 06 | 8 | 6 | 14 |
| 07 | 10 | 5 | 15 |
| 08 | 12 | 12 | 24 |
| 09 | 13 | 4 | 17 |
| 10 | 0 | 0 | 0 |
| 11 | 9 | 4 | 13 |
| 12 | 17 | 5 | 22 |
| 13 | 9 | 1 | 10 |
| 14 | 39 | 2 | 41 |
| Total | 196 | 61 | 257 |

Por marco, só essas linhas: M1 31, M2 39, M3 50, M4 37, M5 41, M6 30, M7 26, M8 3. As linhas M0 e as três entregas M8-guia, M8-revisao-seguranca e M8-piloto ficam de fora dessa soma porque não são linhas da matriz 01–14.

## Ordem

M0, M1, M2, M3, M4, M5, M6, M7 e M8, nessa ordem. Investigar o ERP não atrasa o fluxo de texto do M2. Integração mínima pode entrar quando o contrato real existir; isso não puxa módulo financeiro para dentro do chat.
