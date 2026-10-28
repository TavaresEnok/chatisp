# Perguntas e decisões externas

Fonte normativa: `docs/especificacao.md` V4.1, seção D.2, de 28/10/2026.
Estas decisões são de implantação. Nenhuma delas bloqueia a especificação nem o webchat independente de ERP, Meta real e Evolution.
Integrações reais permanecem pendentes até existir documentação, credencial e autorização. O plano de marcos está em `docs/plano.md`.

Cada item traz o impacto de seguir sem a informação e uma alternativa reversível. O caminho enquanto a decisão não chega é o da própria D.2.

## D.2 Decisões pendentes

### D2-01 — ERP existente, versão, API e autenticação

Necessária antes do adaptador real.

Impacto: o Chat ISP precisa de um único ERP, o que a operação já usa. Sem versão, autenticação e capacidades documentadas, qualquer URL, verbo ou campo de fornecedor seria invenção. Uma oferta que prometa resumo, fatura, segunda via ou Pix fica sem evidência. O atendimento em si segue.

Caminho enquanto pendente: contrato interno mínimo e fake usado só em desenvolvimento e teste. O fake responde “capacidade indisponível” para o que não estiver implementado no double. O webchat, as filas e o histórico não esperam essa conexão.

Nomes internos, definidos em B.4.8, sem serem endpoints do fornecedor:

| Nome interno | Uso |
| --- | --- |
| SearchCustomer | Buscar cadastro por referência autorizada |
| GetCustomerSummary | Ler o resumo mínimo autorizado |
| ListOpenInvoices | Listar faturas em aberto quando a API real suportar |
| GetInvoiceDocument | Obter documento que o ERP já sabe gerar |
| GetPixCode | Obter código que o ERP já sabe emitir |

“Obter segunda via” só será classificado como leitura ou geração quando a API real disser qual é o efeito. O chat não calcula, não gera boleto e não compensa pagamento.

Alternativa reversível: substituir o fake pelo adaptador documentado em `docs/integracoes/erp-existente.md`, atrás da mesma interface. SGP, IXC, Hubsoft ou outro só entram se forem o ERP utilizado e tiverem documentação. Um segundo adaptador continua V2.

### D2-02 — Mecanismo existente de autorização do cliente

Necessária antes de mostrar dados sensíveis ou executar ações de ERP diante do cliente.

Impacto: telefone, nome, JID, LID ou CPF informado na conversa não autenticam o titular. Exibir fatura, documento ou Pix sem o mecanismo que o provedor já usa trataria identificação como autorização.

Caminho enquanto pendente: a capacidade fica oculta. O atendimento geral, as notas e o histórico continuam. O atendente não recebe um formulário padrão pedindo CPF completo pelo WhatsApp.

Alternativa reversível: quando o mecanismo existir, gravar escopo, validade e referência verificável em `customer_verifications` e só então habilitar as ações já previstas. Dá para desligar de novo por caixa, sem migração destrutiva.

### D2-03 — Propriedade e configuração Meta, permissões e número

Necessária antes do teste real e da abertura comercial da modalidade oficial.

Impacto: token preenchido não prova onboarding. App, WABA, número, permissões e quem reconecta precisam de dono. Sem isso não há teste real nem oferta comercial da Cloud API. A versão da Graph API também continua sem número fixado: este plano não escolhe uma versão de corredor.

Caminho enquanto pendente: servidor Meta falso, claramente identificado, e configuração manual documentada (token, App ID, App Secret, WABA ID, Phone Number ID). O webhook de verificação usa token próprio, distinto do App Secret. A assinatura é HMAC do corpo bruto com o App Secret daquele aplicativo. Templates permanecem propriedade da WABA. A janela de 24 horas é regra da plataforma oficial.

Alternativa reversível: apontar o mesmo driver da base URL falsa para o host oficial, com credencial real e a versão da Graph API registrada em `docs/integracoes/` no momento em que a documentação vigente for fixada. Outbox, idempotência e assinatura não mudam de contrato.

### D2-04 — Evolution exata, licença e condições aplicáveis

Necessária antes de habilitar a modalidade Web.

Impacto: ligar Web sem release, licença, condições e teste apresentaria a modalidade como pronta. Aprovação de implantação não é homologação da Meta. O binário do Chat ISP não embute Baileys.

Caminho enquanto pendente: feature flag desligada. A Cloud API segue independente. Eventos de Evolution não são assumidos como HMAC da Meta.

Alternativa reversível: habilitar por inbox, com aceite explícito, depois de fixar a release e registrar licença, backup de sessão e dependências. A troca de driver cria conexão nova e preserva o histórico. O namespace antigo de IDs não é reutilizado. Desligar a flag volta ao estado anterior.

### D2-05 — Regras vigentes de cobrança da Meta

Necessária antes de estimativa monetária ou oferta de preço.

Impacto: a revisão de 28/10/2026 não confirmou a mudança tarifária citada em fonte secundária. A página pública ainda descrevia gratuidade de mensagens de serviço, e a página técnica detalhada não ficou acessível o bastante para virar regra. Codificar cobrança ou franquia agora publicaria uma tarifa inexistente no produto. Ausência de recibo também não significa mensagem gratuita.

Caminho enquanto pendente: guardar ID da mensagem, categoria reportada, indicador de cobrança, data e origem. Quando o dado faltar, gravar “desconhecido”. Agregados são reconstruíveis e evitam dupla contagem. Valor em dinheiro fica na V2.

Alternativa reversível: introduzir tarifas versionadas, com vigência, moeda, mercado, fonte oficial e conciliação, sem reescrever eventos antigos desconhecidos. Tráfego Web continua fora da tarifa Cloud API.

### D2-06 — Enquadramento do provedor e retenção

Necessária antes de produção com dados reais e antes de expurgo automático.

Impacto: uma duração única para mensagem, mídia, envelope, log e dado financeiro misturaria classes diferentes. Apagar cedo pode atingir registro que o provedor precise guardar. A orientação de atendimento da Anatel é referência geral; a aplicação ao provedor e o manual vigente ainda precisam de validação. O software não declara conformidade genérica.

Caminho enquanto pendente: definir o perfil em `docs/legal/` antes de ativar expurgo. Minimizar coleta. CPF/CNPJ permanece opcional, cifrado e mascarado, e só entra por fluxo autorizado. O expurgo automático fica desligado.

Alternativa reversível: ativar `retention_tasks` por tenant depois do perfil aprovado. Exclusões já registradas são reaplicadas depois de um restore. Desligar o expurgo não exige apagar o histórico.

### D2-07 — Perfil de carga, metas e RPO/RTO

Necessária antes do aceite do piloto.

Impacto: número de tenants, atendentes, mensagens, mídia, p95, RPO ou RTO inventado neste repositório seria um benchmark falso. A especificação deixa esses valores para o perfil real do piloto.

Caminho enquanto pendente: testes funcionais, de isolamento, de idempotência e de concorrência. Nenhuma tela, documento ou oferta anuncia capacidade.

Alternativa reversível: registrar o perfil e só então medir com k6 e com um restore real. Os testes funcionais anteriores continuam válidos. O relatório separa dado sintético, ambiente e limitação.

### D2-08 — Política de suspensão e limite transitório de entrada

Necessária antes da operação comercial.

Impacto: sem política escrita, o ingresso ou confirma webhook que não foi persistido, ou aceita armazenamento sem limite. Os dois resultados contrariam B.4.16.

Caminho enquanto pendente: configuração explícita e teste de degradação. Tenant suspenso bloqueia operação e envio novo, mantém trilha e o procedimento de exportação autorizado. Webhook durante a suspensão fica em retenção transitória limitada, sem bot, distribuição ou envio. Limite esgotado alerta e aplica a política configurada. Reativação concilia o backlog e não dispara despedida, menu ou ação vencida.

Os números do limite e o destino do alerta são a parte que ainda depende de decisão. O comportamento qualitativo acima já está especificado.

Alternativa reversível: guardar os números em configuração do tenant ou da instalação. Ajustar o limite não muda o esquema de mensagens. Suspender continua reversível e auditado; suspender não apaga dado.

## Limitações do ambiente deste host

Medidas em 28/10/2026 no host de desenvolvimento. Não descrevem o servidor de produção, que continua previsto com Compose, Caddy e PostgreSQL 18 conforme a especificação.

| Limitação | O que se observa | Efeito no trabalho | Caminho reversível |
| --- | --- | --- | --- |
| Sem Docker e sem Podman | `docker` e `podman` ausentes | Sem testcontainers, sem Compose local, sem Garage/Caddy em contêiner neste host | ADR 0001 para o banco de teste. Os arquivos Compose continuam sendo a instalação prevista, executada onde Docker existir. Storage de teste usa double local identificado como simulação até haver endpoint S3 compatível |
| Sem make | `make` ausente | `make test` e `make lint` não rodam aqui | ADR 0002: Makefile permanece para CI e para hosts com make; scripts equivalentes rodam os mesmos comandos |
| Sem root e sem sudo | uid 1000, sem `sudo` | Não instalar pacote de sistema, não abrir porta privilegiada, não colocar binário em `/usr/local` | Ferramentas no cache do Go e no prefixo do Node 24 |
| Sem PostgreSQL | `psql` ausente; nenhum servidor local | RLS e concorrência não podem usar um Postgres do sistema | PostgreSQL 18.6.0 embutido só nos testes, ou `CHATISP_TEST_DATABASE_URL` |
| Sem xz e sem unzip | Os dois binários ausentes | Artefato `.txz`, `.xz` ou extração via `unzip` não é opção | Extração por biblioteca Go a partir de `.tar.gz` (gzip). ZIP, se aparecer, por `archive/zip` da biblioteca padrão, sem o binário `unzip` |
| npm 10 no PATH antes do npm 11 | Node v24.22.1; npm 11.19.1 no prefixo desse Node; `/usr/local/bin/npm` é 10.9.9 e aparece primeiro | Lockfile gerado pelo npm 10 pode divergir do padrão do projeto | ADR 0002: o projeto usa o npm 11 que acompanha o Node 24 |
| pnpm presente e fora do projeto | pnpm 12 disponível por Corepack | Um lockfile `pnpm-lock.yaml` dividiria o frontend | npm e `package-lock.json`. pnpm não é chamado |
| ffmpeg ausente | `ffmpeg` não está no PATH | A conversão de áudio do M4 não roda neste host até o binário existir | Levar ffmpeg limitado ao worker no marco M4, sem root se houver binário de usuário; até lá o teste dessa conversão fica explícito como não executado |
| gcc presente | gcc 12.2.0 | Há compilador C se alguma dependência exigir | Preferir o toolchain Go puro. cgo não é requisito do núcleo |
| Go presente | go1.27.3 em `/usr/local/go` | Atende o mínimo 1.25 da especificação | Fixar `go 1.27.3` no `backend/go.mod` no M1 |

Rede para baixar módulo Go, imagem npm ou o binário de Postgres do teste pode ser necessária na primeira execução. Isso não autoriza chamada a Meta, Evolution ou ERP.

## Integrações reais pendentes

| Sistema | Estado | O que já é conhecido | O que não será inventado |
| --- | --- | --- | --- |
| Meta Cloud API | Pendente de credencial, dono e teste real | Autenticação de webhook, campos de configuração manual, templates por WABA, janela oficial | Versão da Graph API, paths não lidos na documentação fixada, tarifa |
| Evolution / WhatsApp Web | Pendente e desligada | Conector externo opcional, eventos distintos, QR efêmero | HMAC no formato da Meta, Baileys dentro do binário, templates oficiais |
| ERP existente | Pendente de versão, API e autenticação | Cinco nomes internos de capacidade em B.4.8 | Paths de SGP, IXC ou qualquer fornecedor; cadastro financeiro local |
| IA externa | Pendente de endpoint autorizado | Um gancho por caixa, HMAC, Bearer, prazo e handoff | Modelo, RAG ou ferramenta embutida |
| SMTP | Pendente de serviço já disponível | Convite e recuperação de senha | Canal de atendimento por e-mail |
| S3 de desenvolvimento | Pendente neste host | API S3 via aws-sdk-go-v2; Garage é o exemplo da especificação quando há Docker | MinIO como dependência; armazenamento apresentado como produção |

Contrato simulado não comprova esses sistemas. Teste real autorizado é registrado à parte, como pede o fecho de B.8.
