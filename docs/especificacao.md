Chat ISP — Especificação V4 revisada
Webchat de atendimento para provedores · Dois conectores WhatsApp · ERP existente integrado por API

Data: 28/10/2026.
Revisão: V4.1 — consolidação da V4 após revisão de produto e engenharia. O nome do arquivo permanece chat-isp-especificacao-v4-duplo-whatsapp.md.
Status: especificação; não declara código implementado, testes executados ou integrações homologadas.
Prioridade: construir o webchat. O ERP já existe e continua responsável pelas regras de negócio do provedor.
Conectores previstos: Meta Cloud API e Evolution/WhatsApp Web; o segundo depende de validação técnica e das condições de uso da versão escolhida.
Como usar este documento
Parte	Conteúdo
A	Escopo funcional, interface, fluxos e limites do produto
B	Arquitetura, regras de consistência, segurança, modelo de dados e testes
C	Ordem de implementação e prompt para a IA de desenvolvimento
D	Fontes oficiais, limitações da verificação e decisões pendentes
A matriz e os contratos desta revisão substituem os trechos correspondentes da V4 anterior. O histórico de decisões antigas não é uma segunda fonte de requisitos. Em caso de contradição remanescente, registrar a divergência antes de implementar a parte afetada. Documentação técnica efetiva, licenças e normas aplicáveis prevalecem sobre suposições deste arquivo.

PARTE A — Produto e escopo funcional
A.1 Objetivo e fronteiras
O Chat ISP é o posto de trabalho de quem atende o cliente pelo navegador. Seu núcleo é receber e enviar mensagens, organizar conversas, controlar filas e responsáveis, preservar histórico e medir o atendimento.

Neste documento, webchat significa o painel web usado pelos atendentes e supervisores. O canal de comunicação do cliente na V1 continua sendo WhatsApp. Um widget de chat para visitantes do site não foi incluído nesta revisão.

O ERP já existe. Não será recriado, substituído ou incorporado ao Chat ISP. SGP, IXC ou o ERP próprio da operação são integrações possíveis; a implementação começa pelo ERP efetivamente utilizado, sem pressupor que seja necessário desenvolver dois adaptadores ao mesmo tempo.

Responsabilidade do webchat	Responsabilidade do ERP existente	Responsabilidade da IA externa
Conversas, mensagens, filas, setores e atendentes	Cadastro comercial de clientes, planos e contratos	Interpretar e responder dentro das permissões concedidas
Protocolo local, histórico e resultado do atendimento	Faturas, boletos, Pix, pagamentos e regras financeiras	Usar ferramentas externas autorizadas
Notas, etiquetas, distribuição e relatórios de atendimento	OS, agenda técnica, desbloqueio e operação de rede	Entregar a conversa ao humano quando necessário
Mostrar contexto externo e solicitar operações documentadas	Autorizar e executar suas próprias operações	Respeitar a posse da conversa e o desligamento do bot
O webchat pode guardar referências externas, evidências de ações e documentos efetivamente enviados na conversa. Isso não autoriza criar uma réplica de faturas, contratos ou cadastro financeiro. O ERP é a autoridade desses dados.

Critério de utilidade: um atendente deve conseguir receber, assumir, responder, transferir, consultar o histórico e encerrar um atendimento mesmo quando o ERP ou a IA estiver indisponível. A integração acrescenta contexto; sua falha não paralisa o chat.

Premissas: vários provedores isolados na mesma instalação; instalação dedicada opcional; múltiplos números por provedor; escolha do conector por número; seis áreas na interface: Painel, Conversas, Contatos, Equipes, Relatórios e Configurações.

A.2 Legenda e prioridade
Decisão	Significado
V1	Recurso do lançamento; critérios de aceite definidos nesta especificação
V1 simples	Versão reduzida descrita na linha, sem extensões implícitas
V2	Não implementar no lançamento; criar apenas os pontos de extensão explicitamente necessários
Não fazer	Fora do produto
Recursos de integração marcados V1 dependem de a capacidade existir no sistema externo escolhido. O contrato e o comportamento de indisponibilidade são obrigatórios; endpoints reais não podem ser inventados. Ausência de documentação não bloqueia os módulos independentes do webchat, mas impede declarar aquela integração pronta.

A coluna Chatwoot é referência aproximada de comportamento, não auditoria de versão, licença ou plano. Community, Comercial, Via integração e Conforme canal não constituem garantia sobre a versão atual do Chatwoot. O escopo do Chat ISP é determinado por sua própria coluna.

A.3 Mudanças consolidadas desta revisão
Ajuste	Decisão atual
Foco do produto	Webchat; ERP existente acessado por integração fina
Adaptadores ERP	Um conector real para o ERP utilizado; SGP/IXC somente quando necessários e documentados
Resultado do atendimento	Estado “Encerrada” separado do resultado “resolvido”, “sem resposta”, “encaminhado” ou “spam”
Bot e humano	Versão de posse própria; envio pendente do bot é invalidado na tomada humana
Repetição de comandos	Idempotência também na API do webchat e nas solicitações ao ERP
Estados e distribuição	Transições, capacidade do atendente e recuperação de falhas explícitas
Multi-tenant	RLS, relações com tenant em chaves estrangeiras e ingresso de webhook antes do roteamento
Identidade do cliente	ID interno e identidades externas; CPF não é prova de autorização nem coleta padrão no WhatsApp
Janela oficial	Vinculada ao relacionamento cliente–número; independente do ciclo de atendimento
Protocolo	Local imutável; referência ERP associada sem troca silenciosa
Custos Meta	Dados observados e regras verificadas; sem afirmar como confirmada a alteração de tarifa de outubro de 2026
Execução	Interface e fluxo completo do webchat aparecem cedo; integração não expande o escopo para ERP
Lançamento: Cloud API funcional e validada no ambiente real é a base da abertura comercial. Evolution/Web permanece desabilitado por padrão até cumprir seus critérios. A ausência dessa validação não impede lançar a modalidade oficial, mas a modalidade Web deve ser apresentada como pendente, nunca como já entregue.

A.4 Matriz completa
01. Canais de comunicação
Somente o WhatsApp entra na V1, mas com duas formas de conexão: Cloud API oficial ou Evolution API usando WhatsApp Web, selecionadas por número e com isolamento por tenant. Evolution via Cloud API não elimina tarifas da Meta; o nosso driver oficial já a cobre sem intermediário. O modelo de dados prevê outros canais posteriormente.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
WhatsApp Business Cloud API	Community	V1	Driver oficial principal do lançamento; WhatsApp continua sendo o único serviço de mensagens
Configuração manual da Cloud API	Community	V1	Token, App ID/App Secret, WABA ID e Phone Number ID; webhook configurado por aplicativo ou WABA quando permitido
Embedded Signup (conectar pelo fluxo da Meta)	Community	V2	Facilita a venda; exige app verificado na Meta
Múltiplos números por provedor	Community	V1	Cada número é uma caixa de entrada
Múltiplas caixas de entrada por conta	Community	V1	Restritas a números WhatsApp
Inbox multicanal unificada	Community	Não fazer	A normalização na V1 cobre os dois conectores do WhatsApp; não implementar outros canais
Coexistência com o app WhatsApp Business	Community (elegíveis)	V2	Provedor pequeno que não quer largar o app
WhatsApp via Twilio	Community	Não fazer	Cloud API direto basta
Escolher conector por número (oficial ou Web/Evolution)	Não é recurso do Chatwoot	V1	Tipo imutável enquanto conectado; troca requer desligamento, procedimento e validação explícita; número não deve operar simultaneamente nos dois modos fora de fluxo oficial de coexistência
WhatsApp Web por Evolution API (QR Code/Baileys)	Não é canal nativo oficial do Chatwoot	V1 simples	Conector condicionado à versão validada, licença, políticas e testes; habilitação por inbox e aceite explícito, com operação degradada visível
E-mail IMAP/SMTP	Community	V2	Segundo canal mais provável
Continuidade da conversa por e-mail	Community	Não fazer	Depende do widget
Chat no site (widget)	Community	Não fazer	O foco atual é o painel web de atendimento via WhatsApp
Chat em aplicativos via SDK	Community	Não fazer	O app do assinante abre o WhatsApp
Facebook Messenger	Community	Não fazer	Fora do canal definido para a V1
Instagram Direct	Community	V2	Só se o comercial pedir para captação
TikTok Direct	Community	Não fazer	
Telegram	Community	Não fazer	
LINE	Community	Não fazer	
SMS	Community	Não fazer	
X / Twitter	Descontinuado (“em breve”)	Não fazer	
Telefonia via Twilio	Comercial	Não fazer	O provedor já tem PABX
Chamadas pelo WhatsApp	Comercial	Não fazer	
Videochamadas (RealtimeKit, Dyte)	Via integração	Não fazer	
Canal API personalizado	Community	V2	Porta para canais futuros sem mexer no núcleo
02. WhatsApp: mensagens, regras do canal e operação
Como WhatsApp é o único serviço de mensageria da V1, a qualidade do atendimento deve ser superior à de uma integração genérica. A tabela abaixo é uma união de capacidades, não uma promessa de equivalência: templates aprovados, janela de 24h da plataforma e cobrança da Meta pertencem ao modo Cloud API. O driver Evolution/Web habilita somente recursos comprovados por versão e não deve fingir possuir status, templates ou contratos oficiais.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Receber texto	Community	V1	
Enviar texto	Community	V1	Com rastreio de status
Receber imagens	Community	V1	Fotos de ONU, roteador, comprovante
Enviar imagens	Community	V1	Tipos e limites da Cloud API
Receber documentos e PDF	Community	V1	
Enviar documentos e PDF	Community	V1	Boleto, contrato
Receber áudio e nota de voz	Community	V1	Player na conversa
Enviar arquivo de áudio	Community	V1	
Gravar áudio no painel	Community	V1	Conversão para OGG/Opus no worker
Receber vídeo	Community	V1	
Enviar vídeo	Community	V2	Pouco usado pelo atendente
Receber localização	Community	V1	Endereço para viabilidade e visita
Receber contato (vCard)	Community	V1 simples	Exibir nome e número
Figurinhas recebidas	Conforme canal	V1 simples	Exibir; não enviar
Reações recebidas	Conforme canal	V1 simples	Exibir na mensagem
Enviar reações	Conforme canal	V2	
Responder mensagem específica (citação)	Community	V1	Campo de contexto da API
Botões e listas interativas	Community	V1 simples	Menu de triagem e confirmações; sem editor visual
Status enviada / entregue / lida	Community	V1	Eventos efetivamente reportados pelo driver; aceitação da API e entrega ao cliente são estados diferentes
Falha de envio com motivo	Community	V1	Erro legível; ex.: fora da janela
Contador de não lidas	Community	V1	Separado dos recibos da Meta
Indicador da janela de 24 horas	Parcial	V1	Apenas para regras da plataforma Cloud API; no Web ocultar indicador oficial, sem afirmar que a automação não oficial está dispensada das políticas do WhatsApp
Biblioteca de templates aprovados	Community	V1	Somente conector Meta Cloud API, por conta WhatsApp
Sincronização de templates	Community	V1	Somente Cloud API; não aplicável ao modo WhatsApp Web
Envio individual de template	Community	V1	Somente Cloud API; no Web não apresentar a mesma ação como template oficial
Criar template e enviar para aprovação pelo painel	Conforme conexão	V2	V1 cria no Gerenciador da Meta
Campanhas / broadcast	Community	Não fazer	Aviso operacional em lote fica na V2 (seção 15)
Analytics de campanhas	Comercial	Não fazer	
Saúde e qualidade do número	Community	V1 simples	Qualidade e erros essenciais
Limites de mensagens do número	Community	V1 simples	Tier atual para diagnóstico
Situação do nome de exibição	Community	V2	
Custo estimado da Meta por provedor	Não tem	V2	Painel de custo e projeção somente para mensagens Cloud API, com conciliação; Evolution/Web tem custo próprio de operação, licença e suporte, sem tarifa da Cloud API
CSAT pelo WhatsApp	Community	V2	Ver seção 09
Chamadas WhatsApp	Comercial	Não fazer	
Identidade do contato no canal	Community	V1	ID interno estável + identidades externas por conexão; wa_id no driver oficial, JID/LID quando aplicável no Web
Download e guarda da mídia recebida	Community	V1	A URL da Meta expira; baixar para o storage
Consentimento e opt-in para mensagens ativas	Obrigação do canal	V1	Evidência de origem, data, finalidade e revogação; verificação central antes do envio aplicável
03. Inbox, conversas e organização
O coração do produto: quase tudo entra no lançamento. Os cortes são filtros e visualizações configuráveis, trocados por filtros fixos.

Estados da conversa: Em triagem, Aberta, Aguardando cliente e Encerrada. As transições são as de A.6; não é obrigatório percorrer todos os estados em sequência.

Adiar é um agendamento de retorno à fila, não um estado. Pendências internas (técnico, OS, confirmação de pagamento) usam Adiar com data e nunca são encerradas automaticamente.
Não lidas é um contador, independente do estado.
Controlador (menu, bot ou humano) é um campo separado do estado: define quem pode enviar mensagens. A troca é atômica e o bot para quando o humano assume.
Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Caixa de entrada compartilhada	Community	V1	Fila por setor e por número
Atualização em tempo real	Community	V1	Mensagens, atribuições e estados
Abas Minhas / Não atribuídas / Todas	Community	V1	Visão padrão
Atendente vê só os setores dele	Community (via equipe)	V1	
Histórico completo da conversa	Community	V1	
Histórico por contato	Community	V1	Conversas anteriores na lateral
Estado Aberta	Community	V1	
Estado Pendente (bot)	Community	V1 simples	Vira “Em triagem”
Estado Aguardando cliente	Não tem	V1	Separa quem deve a resposta; base das métricas de tempo
Estado Encerrada	Community (resolvida)	V1	Término do atendimento com data, ator, motivo e resultado; “sem resposta” não conta como solução confirmada
Adiar (snooze) com data e hora	Community	V1 simples	Agenda retorno, motivo e destino; nova mensagem atual do cliente retira o adiamento
Reabrir manualmente	Community	V1	Com registro na linha do tempo
Reabrir ao receber nova mensagem	Community	V1 simples	Regra por caixa: reabre dentro da janela X com novo ciclo; fora dela cria novo atendimento
Assumir conversa	Community	V1	Responsável explícito
Transferir para atendente	Community	V1	Preserva histórico e autoria
Transferir para setor	Community	V1	Volta para a fila do setor
Retirar responsável	Community	V1	Devolve à fila
Notas internas privadas	Community	V1	Nunca enviadas à Meta
Menções @atendente	Community	V2	Nota interna cobre o básico
Participantes adicionais	Community	Não fazer	Responsável + notas bastam
Etiquetas na conversa	Community	V1 simples	Lista curta de motivos por provedor, sem hierarquia
Prioridade	Community	V1 simples	Normal e Urgente
Busca por nome e telefone	Community	V1	
Busca por conteúdo das mensagens	Community	V1	Full-text isolado por tenant
Busca por protocolo	Não tem	V1	Cliente cita o protocolo
Busca global em todos os módulos	Community	Não fazer	Busca focada em contatos e conversas
Busca avançada	Comercial	Não fazer	
Filtro por estado, setor e atendente	Community	V1	
Filtro por caixa / número	Community	V1	
Filtro por etiqueta e período	Community	V1 simples	Sem linguagem de consulta
Filtros avançados AND/OR	Community	Não fazer	
Pastas e visualizações salvas	Community	Não fazer	Abas fixas
Ordenação por maior espera ou última mensagem	Community	V1	Padrão: maior espera primeiro
Ações em massa	Community	V2	Atribuir e encerrar em lote; útil em falha massiva
Marcar como lida / não lida	Community	V1	Estado por atendente
Detecção de colisão (quem está vendo/digitando)	Community	V1	Avisa, não bloqueia
Bloqueio rígido de segunda resposta	Não tem	Não fazer	Concorrência tratada nas ações (seção 14)
Silenciar conversa	Community	Não fazer	Preferências de notificação cobrem a V1; bloquear contato é ação distinta para abuso/spam
Idioma da conversa	Community	Não fazer	Tudo em português
Eventos do sistema na linha do tempo	Community	V1	“Fulano transferiu para Financeiro”
Protocolo visível na conversa	Não tem	V1	Ver seção 15
Transcrição por e-mail	Community	Não fazer	Sem canal e-mail
Exportar conversa individual (PDF)	Conforme recurso	V2	Contestações e Procon; exportação estruturada já na V1 (seção 12)
04. Mensageria e produtividade do atendente
O atendente precisa responder, consultar e navegar sem atrito. Sai o que serve a uma plataforma genérica: barra de comandos, variáveis arbitrárias, sugestões por IA nativa.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Compositor de mensagem	Community	V1	Texto, anexos, emoji, indicador da janela de 24h
Emojis	Community	V1	
Formatação de texto	Conforme canal	V1 simples	Só a sintaxe do WhatsApp (negrito, itálico)
Arrastar arquivo e colar imagem	Community	V1	
Pré-visualização de anexos	Community	V1	Imagem, PDF, áudio, vídeo
Download seguro de anexos	Community	V1	Sem URL pública permanente
Rascunho por conversa	Community	V1	Não perder texto ao trocar de conversa
Assinatura do atendente	Community	V1 simples	Prefixo “Nome:” configurável por provedor
Respostas prontas compartilhadas	Community	V1	Geridas por admin/supervisor
Respostas prontas privadas	Community	V2	
Atalho “/” para respostas prontas	Community	V1	Ex.: /reiniciar
Variáveis simples nas respostas	Community	V1 simples	Nome do cliente, nome do atendente, protocolo
Variáveis arbitrárias e lógica	Community	Não fazer	
Respostas prontas com anexo	Não tem	V2	Ex.: PDF de como reiniciar o roteador
Botão “Encerrar com motivo”	Via macro	V1	Etiqueta de motivo + resultado + síntese + despedida opcional + encerrar
Macros configuráveis	Community	Não fazer	
Atalhos de teclado essenciais	Community	V1 simples	Enviar, próxima conversa, nota, buscar
Atalhos avançados	Community	V2	
Barra de comandos (Cmd+K)	Community	Não fazer	
Sugestão de resposta por IA nativa	Comercial	Não fazer	IA externa pode sugerir via API (seção 08)
Notificações na interface	Community	V1	
Notificações sonoras	Community	V1	Som distinto para conversa nova e mensagem
Notificações do navegador	Community	V1	Com permissão explícita
Notificações por e-mail	Community	Não fazer	
Push de app nativo	Community	Não fazer	Sem app nativo
Preferências de notificação por usuário	Community	V1 simples	Som, navegador, silencioso
Presença: disponível / ocupado / offline	Community	V1	
05. Usuários, equipes e distribuição
Distribuição justa com limite simples por atendente, sem o motor de políticas do Assignment V2.

Como a distribuição funciona:

A conversa chega pelo número e passa pelo menu de triagem (ou vai direto ao setor padrão da caixa).
Entra na fila do setor.
O sistema pega os atendentes do setor que estão disponíveis e abaixo do limite de conversas.
Seleciona a fila por prioridade e tempo de espera e distribui por round-robin, reservando a vaga do atendente na mesma transação.
Ninguém disponível: a conversa fica na fila sem responsável, visível para todos do setor.
Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Cadastro e convite de atendentes	Community	V1	Convite por e-mail ou link
Ativar / desativar atendente	Community	V1	Desativado mantém histórico
Equipes / setores	Community	V1	O provedor cria os seus
Atendente em vários setores	Community	V1	
Acesso por caixa de entrada	Community	V1	Por tenant, setor e número
Responsável único por conversa	Community	V1	
Atribuição manual	Community	V1	Assumir, transferir, devolver
Atribuição automática	Community	V1	Ligada por setor
Round-robin	Community	V1	
Considerar disponibilidade	Community	V1	Offline e ocupado não recebem
Prioridade por tempo de espera	Community	V1	Urgentes antes de normais; FIFO dentro da prioridade; alerta identifica normais em espera excessiva
Limite de conversas por atendente	Premium	V1 simples	Abertas e aguardando cliente consomem uma vaga; adiadas e encerradas não; reserva atômica
Capacidade por caixa ou grupo	Premium	Não fazer	
Balanceamento por carga atual	Premium	Não fazer	Round-robin + limite basta
Limite de atribuições por janela de tempo	Community	Não fazer	
Exclusões por idade ou etiqueta	Community	Não fazer	
Políticas Assignment V2 configuráveis	Community/Premium	Não fazer	
Comportamento após transferência	Configurável	V1 simples	Para atendente: valida acesso e capacidade; para setor: remove responsável e volta à fila
Desatribuir quem fica offline	Community	V1 simples	Só após tolerância configurável (ex.: 5 min offline); queda rápida de conexão não tira conversas
Reatribuir conversas de atendente desativado	Operacional	V1	
Reatribuição por tempo sem resposta	Via automação	V2	Junto com alertas de espera
Supervisor vê filas dos setores	Community	V1	Todos os setores do tenant, conforme o papel fixo da seção 12
Supervisor altera responsável e prioridade	Community	V1	
Indicador de ocupação por atendente	Community	V1 simples	Vagas usadas/limite; abertas, aguardando e adiadas exibidas separadamente
Pausas com motivo (almoço, treinamento)	Não é núcleo	V2	Disponível/ocupado basta no início
Escalas e calendário de atendentes	Não é núcleo	Não fazer	Horário é por caixa
06. Automações, horários e bot
Nada de construtor de regras: entram comportamentos fixos e configuráveis que cobrem a rotina do provedor, um menu de triagem nativo e um gancho para bot ou IA externa.

Boas-vindas, protocolo e menu devem ser apresentados de forma concisa, preferencialmente em uma mensagem. Usar interação quando a capacidade do driver permitir e texto numerado como fallback. A decisão é de experiência de atendimento e não depende de uma premissa tarifária não confirmada.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Horário de atendimento por dia da semana	Community	V1	Por caixa
Fuso horário do provedor	Community	V1	
Feriados e exceções	Configurável	V2	
Resposta fora do expediente	Community	V1	Texto por caixa, com orientação de plantão
Setor de plantão fora do expediente	Não tem	V1 simples	Opcional: em vez de só responder, envia ao setor de plantão
Mensagem de boas-vindas	Community	V1 simples	Uma por caixa, com o número do protocolo
Resposta quando ninguém está disponível	Community	V1 simples	Mensagem configurável
Setor padrão por caixa	Community	V1	
Atribuição automática ao chegar	Community	V1	Seção 05
Menu de triagem (1 suporte, 2 financeiro…)	Via bot externo	V1	Interativo quando suportado; texto numerado como fallback; opções levam a setores e sempre há saída para humano
Gancho para bot ou IA externa (Agent Bot)	Community	V1 simples	Webhook entrega a conversa ao bot; API devolve ao humano com nota
Encerramento automático por inatividade	Via automação	V1	Só em espera iniciada por resposta pública humana confirmada: X min, aviso; mais Y min, encerra com resultado “sem resposta”. Revalida mensagem atual, posse e entrega do aviso conforme A.6
Retorno de conversa adiada à fila	Community	V1	Parte do snooze
Etiqueta automática por opção do menu	Via automação	V1 simples	Opção “financeiro” já marca o motivo
Autoetiquetar por palavra-chave	Community	Não fazer	
Gatilhos fixos (conversa criada, mensagem, mudança de estado)	Community	V1 simples	Internos ao núcleo, sem editor
Construtor visual de regras	Community	Não fazer	
Condições AND/OR arbitrárias	Community	Não fazer	
Sequências ilimitadas de ações	Community	Não fazer	
Ações com atraso configurável	Community	Não fazer	Snooze e inatividade cobrem
Condições por IA	Comercial	Não fazer	
Macros multiações	Community	Não fazer	“Encerrar com motivo” cobre o principal
Macros privadas / compartilhadas	Community	Não fazer	
Edição, clonagem e versionamento de regras	Community	Não fazer	
E-mail disparado por automação	Community	Não fazer	
Webhook disparado por regra	Community	Não fazer	Webhooks globais por evento (seção 11)
Integração Dialogflow	Community	Não fazer	O gancho genérico cobre
Integração Rasa	Via integração	Não fazer	O gancho genérico cobre
Plataforma de bots (vários bots, marketplace)	Community	Não fazer	Um gancho por caixa basta
07. Contatos
O contato é um diretório de pessoas com histórico, não um CRM. A única adição em relação ao Chatwoot é o vínculo opcional com o cliente no ERP, guardado só como referência (ID externo).

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Criar contato ao receber WhatsApp	Community	V1	Resolver identidade externa para ID interno; não presumir telefone disponível nem mesclar apenas por nome
Criar contato manualmente	Community	V1	Nome e telefone
Nome, telefone e identificador do canal	Community	V1	
E-mail opcional	Community	V1 simples	Só campo, sem canal
Avatar	Community	V1 simples	Só se o WhatsApp fornecer
Editar contato	Community	V1	Com registro de alteração
Histórico de conversas do contato	Community	V1	
Notas do contato	Community	V1 simples	Um campo de observações, separado das notas da conversa
Busca, filtro e ordenação	Community	V1 simples	Nome, telefone, CPF/CNPJ, últimos contatos
ID do cliente no ERP (referência externa)	Community (identifier)	V1	Referência opaca ao cadastro existente; não criar cadastro comercial no chat
Vínculo contato → um ou mais clientes/contratos do ERP	Não tem	V1	Mesmo número com casa e comércio; atendente escolhe o da conversa
CPF/CNPJ do titular	Atributo personalizado	V1 simples	Opcional, quando fornecido por fluxo autorizado do ERP; cifrado, mascarado e sujeito a retenção; não obrigatório para conversar
Etiquetas no contato	Community	V2	Etiquetas ficam na conversa
Associação a empresa	Community	Não fazer	Cliente PJ é cliente no ERP
Enriquecimento de empresas	Comercial	Não fazer	
Segmentos dinâmicos	Community	Não fazer	
Atributos personalizados arbitrários	Community	Não fazer	Campos fixos acima substituem
Atributos obrigatórios ao resolver	Premium	Não fazer	“Encerrar com motivo” cobre
Importação CSV	Community	V2	Na prática, a identificação vem do ERP via API
Exportação de contatos	Community	V2	Com permissão de admin
Mesclar duplicados	Community	V2	Mesmo titular com dois números
Bloquear contato	Community	V1	Spam e abuso
Iniciar conversa com contato (template)	Community	V1	
Mesmo número em vários provedores	Community (contas isoladas)	V1	Registros totalmente separados por tenant
Sincronização com CRM externo	Via API	Não fazer	O ERP é a fonte; o chat consulta
Formulário pré-chat	Community (widget)	Não fazer	Sem widget; o menu de triagem coleta o necessário
08. Integração fina com o ERP existente e a IA externa
O produto entregue é o webchat. Esta seção define pontos de conexão com sistemas existentes. Não autoriza desenvolver ERP, cobrança, OS, operação de rede ou um construtor de integrações.

O primeiro adaptador real será o do ERP já utilizado pela operação. SGP e IXC são exemplos; implementar ambos não é pré-requisito do piloto. Se o ERP existente já expõe uma API intermediária, reutilizá-la. Registrar versão, autenticação e capacidades documentadas em docs/integracoes/erp-existente.md.

O painel nativo mostra somente o contexto mínimo que o ERP fornecer. O iframe é um ponto de extensão para uma página já existente; não exige desenvolver uma segunda interface financeira. Uma ação indisponível fica claramente indisponível. Falha de consulta não impede responder no chat.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Referência ao cliente/contrato do ERP	Community (identifier)	V1	IDs externos vinculados ao contato e seleção por conversa
Contrato mínimo de capacidades	Não tem	V1 simples	Buscar cadastro, ler resumo e obter documento/código já suportado; resultado tipado e falhas controladas
Conector para o ERP existente	Não tem	V1 simples	Um adaptador documentado; fake somente para desenvolvimento e testes
Adaptadores adicionais SGP, IXC, Hubsoft e outros	Não tem	V2	Se um deles for o ERP existente, seu adaptador é o primeiro da V1; os demais não bloqueiam o chat
Identificar cadastro por referência autorizada	Não tem	V1 simples	Busca e confirmação do vínculo; não confundir identificação com autenticação
Verificação de autorização do cliente	Não tem	V1 simples	Reutilizar mecanismo seguro do ERP/provedor; registrar escopo, validade e referência da verificação
Resumo do cliente na lateral	Não tem	V1 simples	Somente os campos necessários e autorizados; consulta sob demanda
Faturas em aberto	Não tem	V1 simples	Exibição transitória quando a API existente suportar; sem cadastro financeiro local
Obter e enviar segunda via/Pix	Não tem	V1 simples	ERP fornece; chat entrega como mensagem/anexo e registra a ação
Abrir cadastro no ERP	Via integração	V1 simples	Link para ambiente existente e autorizado, sem credencial/CPF em URL
Painel lateral externo (iframe)	Community (Dashboard App)	V1 simples	Para página existente compatível com incorporação; origem permitida, sandbox e contexto mínimo
Contexto do painel externo	Community	V1	Escopo por usuário, tenant e conversa; troca segura e curta, sem segredo de ERP/WhatsApp
Registro de ação e falha na linha do tempo	Não tem	V1	Ator, referência externa, resultado e ID da operação; não duplicar detalhes financeiros
Permissão por papel e setor	Não tem	V1	Aplicada no backend; autorização é revalidada antes de executar ação pendente
Associar protocolo do ERP	Não tem	V1 simples	Referência externa; protocolo local comunicado não é substituído
Desbloqueio em confiança	Não tem	V2	Operação pertence ao ERP; chat apenas poderá solicitar quando expressamente incluída
Abrir/consultar OS	Não tem	V2	Operação pertence ao ERP; sem módulo de OS no chat
Consultar PPPoE, ONU, OLT ou rede	Não tem	V2	Eventual leitura externa; sem engine de rede
Webhook de evento do ERP	Não tem	V2	Não criar sincronização bidirecional na V1
Replicar cadastro financeiro ou contratos	Não tem	Não fazer	Sem espelho de ERP no banco do chat
Gerar boleto/Pix ou calcular juros	Não tem	Não fazer	Apenas o ERP realiza essas funções
Cadastro comercial, planos e pagamentos	Não tem	Não fazer	Já pertencem ao ERP
Cobrança do SaaS dentro do chat	Não tem	Não fazer	Operação comercial externa
Fronteira de autorização da IA: o chat só controla ações executadas por sua API. A IA pode ter ferramentas próprias de leitura autorizadas. Ações que exigem aprovação no Chat ISP precisam passar pelo serviço de integração do Chat ISP; credenciais externas independentes não ficam protegidas por uma aprovação que só existe no chat. A V1 não ganha desbloqueio, OS ou funções financeiras por causa do gancho de IA.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Entregar conversa à IA externa	Community (Agent Bot)	V1 simples	Um gancho por caixa, webhook assinado e prazo para resposta
API de histórico para o bot	Community	V1	Limitada à conversa e às categorias de conteúdo permitidas
Resposta como bot identificado	Community	V1	Token com escopo + versão vigente da posse; API idempotente
Handoff para humano	Community	V1	Motivo, nota e setor autorizado; transferência atômica
Interruptor por caixa e horário	Não tem	V1	Desligar invalida envios pendentes e encaminha conversas controladas pelo bot
Aviso de assistente virtual	Não tem	V1	Transparência e opção de atendimento humano
Humano assume a qualquer momento	Community	V1	Incrementa versão da posse e cancela mensagens automáticas ainda não despachadas
Timeout/falha do bot	Operacional	V1	Volta à fila humana sem perder histórico nem repetir boas-vindas
Autorização por ação	Não tem	V1	Tenant, caixa, conversa e operação verificados pelo servidor
Aprovação humana de ação sensível disponível	Não tem	V1 simples	Vinculada a operação, parâmetros e prazo; não cria ações de ERP fora do escopo
Sugestão de resposta ao atendente	Comercial (Copilot)	V2	Sem IA embutida
Resumo/transcrição por serviço externo	Comercial	V2	Sem RAG ou modelo hospedado no chat
Relatório de solução autônoma do bot	Comercial	V2	V1 registra os dados sem tratá-los como primeira resposta humana
Nenhum dado privado de outro setor ou notas internas é enviado a um bot ou iframe por simples associação ao contato. O contrato declara quais campos podem sair e registra a autorização correspondente.

09. Painel, relatórios, SLA e satisfação
A V1 responde a quatro perguntas: quanto entrou, quanto demorou, quem atendeu e por que o cliente chamou. As regras abaixo são únicas para API, painel, CSV e testes.

Primeira resposta humana: da primeira mensagem atual do cliente no ciclo até o envio público humano confirmado como aceito pelo conector. Não contam nota interna, mensagem que falhou, menu ou bot. Registrar entrega separadamente; ausência de humano é null/“não aplicável”, nunca zero. Resultado incerto só entra quando confirmado.
Tempo até encerramento: início do ciclo até fechamento local, qualquer que seja o resultado. Exibir o resultado junto; “sem resposta” não significa problema resolvido.
Tempo operacional: duração do ciclo menos a união dos intervalos aguardando cliente e adiada. Sobreposição é descontada uma só vez. Ex.: 8h–12h com 3h em espera = 4h total e 1h operacional. Na V1 os relógios são corridos, inclusive fora do expediente; o relatório informa isso.
Espera na fila: cada entrada na fila até atribuição, cancelamento ou fechamento. Transferência para setor gera novo intervalo. A atribuição automática não encerra o relógio da primeira resposta humana.
Reabertura: novo ciclo na mesma conversa dentro de X horas; manter dados de todos os ciclos e o protocolo local. Fora da janela ou para nova demanda explicitamente iniciada, criar nova conversa/protocolo.
Por atendente: contabilizar intervalos reais de responsabilidade; mensagens pelo seu autor; encerramento pelo seu ator. Não creditar todo o tempo da conversa ao último atendente.
Agregação: horários armazenados em UTC e agrupamento pelo fuso do provedor. Mostrar tamanho da amostra, média e mediana; excluir valores não aplicáveis do denominador. Filtros distinguem conversas, ciclos e mensagens.
Motivos: motivo principal único para os totais; etiquetas adicionais podem sobrepor contagens e devem ser identificadas como tal.
Métricas são derivadas de eventos/intervalos persistidos, não apenas de campos acumulados editáveis em conversations. Encerramento por inatividade fica separado de solução confirmada. Atendimento iniciado pela empresa só entra na amostra de primeira resposta humana quando houver uma mensagem elegível do cliente; ausência dessa mensagem não gera duração zero.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Painel em tempo real	Community	V1	Na fila agora, em atendimento, atendentes online, maior espera
Conversas sem responsável	Community	V1	Destaque no painel
Total de conversas por período	Community	V1	
Abertas, aguardando, encerradas	Community	V1	Separar encerradas por resultado; adiadas visíveis sem virar novo estado
Tempo da primeira resposta	Community	V1	Média e mediana, pela definição acima
Tempo até encerramento (total e operacional)	Community	V1	Resultado do atendimento visível; relógios e denominadores definidos acima
Tempo de espera na fila	Community	V1	
Volume de mensagens	Community	V1 simples	
Volume por setor	Community	V1	
Volume e tempos por atendente	Community	V1	Considerando transferências
Volume por caixa / número	Community	V1 simples	Filtro nos relatórios acima
Volume por etiqueta (motivo do contato)	Community	V1	“Quantos chamados de lentidão este mês”
Mapa de calor por hora e dia da semana	Community	V1	Dimensionar escala
Agrupamento por dia, semana, mês	Community	V1	
Taxa de transferência e de reabertura	Community	V2	
Reincidência (mesmo cliente em X dias)	Não tem	V2	Muito valioso em provedor
Quanto o menu/bot resolveu sozinho	Community	V2	
Exportação CSV	Community	V1 simples	Barato
Relatórios personalizados avançados	Community	Não fazer	Sem construtor de BI
Agrupamentos e filtros complexos	Community	Não fazer	
Alerta simples de espera (X min sem resposta)	Enterprise (SLA)	V1 simples	Destaque na fila e no painel, sem depender de reatribuição automática
Políticas de SLA contratuais	Enterprise	Não fazer	
Relatório de violação de SLA	Enterprise	Não fazer	
CSAT ao resolver (nota 1 a 5)	Community	V2	Template ou botões
CSAT com comentário	Community	V2	
Relatório de CSAT	Community	V2	
Regras de envio do CSAT por etiqueta	Community	Não fazer	
Notas de revisão de CSAT	Premium	Não fazer	
Relatórios de IA nativa (Captain)	Comercial	Não fazer	
Analytics de campanhas	Comercial	Não fazer	
Painel do dono do SaaS (todos os provedores)	Administração própria	V1	Métricas técnicas e de uso, sem ler conversas
10. IA nativa, base de conhecimento e marketing
Esta é a maior redução de escopo: nenhuma IA embutida, nenhum CMS, nenhuma plataforma de marketing. O que for IA entra pela integração externa da seção 08.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Captain Assistant (agente que responde clientes)	Comercial	Não fazer	IA externa via gancho
Assistentes múltiplos	Comercial	Não fazer	
Captain Copilot	Comercial	Não fazer	Sugestão via IA externa na V2
Captain Triage	Comercial	Não fazer	Menu de triagem nativo
Captain Monitors	Comercial	Não fazer	
Respostas geradas por IA nativa	Comercial	Não fazer	
Resumo por IA nativa	Comercial	Não fazer	Serviço externo na V2
Classificação por IA nativa	Comercial	Não fazer	
Reescrever / ajustar tom	Comercial	Não fazer	
Tradução automática	Comercial	Não fazer	Português apenas
Transcrição de áudio nativa	Comercial	Não fazer	Serviço externo na V2
Busca semântica	Comercial	Não fazer	Busca textual
Base RAG (PDF, sites, FAQ)	Comercial	Não fazer	Responsabilidade da IA externa
Memórias, ferramentas, cenários de IA	Comercial	Não fazer	
Playground e controle de créditos de IA	Comercial	Não fazer	
Help Center público	Community	Não fazer	Site do provedor, fora do chat
Editor e categorias de artigos	Community	Não fazer	
Vários portais, multilíngue, domínio e SEO	Community	Não fazer	
Artigos no widget	Community	Não fazer	Sem widget
Compartilhar artigo na conversa	Community	Não fazer	Resposta pronta com link cobre
Base de procedimentos interna (roteiros para atendentes)	Não tem	V2	Mais útil que FAQ público; pode ser página simples
Campanhas proativas no site	Community	Não fazer	
Campanhas WhatsApp segmentadas	Community	Não fazer	
Campanhas de SMS e e-mail	Community	Não fazer	
Agendamento de campanhas	Community	Não fazer	
Analytics de campanhas	Comercial	Não fazer	
Marketing (promoções, upgrade de plano)	Community	Não fazer	Fora do atendimento
Lembrete de vencimento de fatura	Não tem	Não fazer	Função do ERP; pode usar a API de envio do chat
11. Integrações, API e ecossistema
Todos os conectores prontos do Chatwoot saem. Ficam a API, os webhooks, o painel lateral e o gancho de bot: o suficiente para qualquer ERP ou IA se conectar sem mexer no núcleo.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
API REST: conversas e mensagens	Community	V1	Enviar mensagem, mudar estado, atribuir, ler histórico
API REST: contatos	Community	V1	Criar, atualizar, vincular ID do ERP
API REST: atendentes, setores, caixas	Community	V1 simples	API pública de integração: leitura; API autenticada do painel: escritas necessárias à administração
API REST: envio de template	Community	V1	Somente driver oficial; regras do canal, autorização e idempotência iguais às da interface
API REST: etiquetas e respostas prontas	Community	V2	
API REST: relatórios	Community	V2	
API de envio em lote (aviso operacional)	Community (campanhas)	V2	Ver seção 15
API de automações, macros, portais	Community	Não fazer	Módulos não existem
Platform API completa	Community	Não fazer	
API de provisionamento de provedor	Community (Platform API)	V2	Criar tenant automaticamente após venda
Tokens de API por provedor	Community	V1	Escopo, rotação, revogação
Rate limit da API pública	Controle da plataforma	V1	Por token e por tenant
Documentação OpenAPI	Community	V1	Gerada do código
Webhooks: conversa criada, atribuída, encerrada	Community	V1	Assinatura HMAC, ID estável do evento, retentativas e proteção contra replay no receptor
Webhooks: mensagem criada	Community	V1	
Webhooks: contato criado ou alterado	Community	V2	
Tempo real via WebSocket	Community	V1	
Painel lateral externo (Dashboard App)	Community	V1 simples	Página existente e contrato mínimo da seção 08; não criar nova interface ERP
Gancho de bot (Agent Bot)	Community	V1 simples	Seções 06 e 08
Conector com o ERP existente	Não tem	V1 simples	Contrato mínimo, integração com o ERP efetivamente utilizado; sem construir plataforma de ERPs
Canal API universal	Community	V2	Seção 01
Slack	Community	Não fazer	
Dialogflow	Community	Não fazer	
Rasa	Via integração	Não fazer	
OpenAI embutido	Via integração	Não fazer	IA externa via gancho
Google Translate	Community	Não fazer	
Shopify	Community	Não fazer	
Stripe	Community	Não fazer	
Linear	Community	Não fazer	
Notion	A verificar	Não fazer	
Cloudflare RealtimeKit	Via integração	Não fazer	
Dyte	Via integração	Não fazer	
Chatwoot CLI	Community	Não fazer	
SDKs de cliente (JS, React Native)	Community	Não fazer	Sem widget
12. Administração, identidade e segurança
Três papéis fixos, sem editor de permissões, mas com autorização checada no backend em toda operação. Simplificar permissões não é retirar segurança.

Papel	Pode
Administrador do provedor	Tudo do provedor: números, setores, usuários, integrações, configurações
Supervisor	Ver todos os setores, reatribuir, alterar prioridade, relatórios, respostas prontas; não configura números nem integrações
Atendente	Atender nos setores a que pertence; ações no ERP conforme o setor
Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Contas (tenants) independentes	Community	V1	Um provedor = um tenant
Isolamento de dados por tenant	Community	V1	RLS e FKs compostas no domínio; tabelas globais/ingresso restritos por finalidade (B.4.4)
Usuários e convites	Community	V1	
Papéis Administrador, Supervisor, Atendente	Community (só admin e agente)	V1	Matriz fixa acima
Acesso por setor e caixa	Community	V1	
Papéis personalizados	Premium	Não fazer	
Permissões granulares configuráveis	Premium	Não fazer	
Usuário em mais de um provedor	Community	V2	O modelo de dados da V1 já usa vínculo usuário ↔ tenant
Login por e-mail e senha	Community	V1	Hash Argon2id
Recuperação de senha	Community	V1	Token de uso único e curta validade
MFA (TOTP + códigos de recuperação)	Community	V1	Opcional para todas as contas; ativação voluntária nas configurações com QR Code (decisão do proprietário em 06/11/2026)
SSO / SAML	Enterprise	Não fazer	
Login social (Google etc.)	Conforme implantação	Não fazer	
Gestão de sessões e revogação	Community	V1	Admin derruba sessão de quem saiu
Bloqueio por tentativas de login	Proteção da plataforma	V1	
HTTPS / TLS	Community	V1	
Credenciais do WhatsApp e do ERP criptografadas	Community (WhatsApp)	V1	Cifra por tenant ou contexto de app compartilhado, conforme propriedade; segredos armazenados não são exibidos inteiros
Auditoria de eventos críticos	Comercial	V1	Login, transferência, exclusão, configuração, ações no ERP
Auditoria avançada com filtros e IP	Comercial	V2	
Mascaramento de CPF na tela	Não tem	V1	LGPD
Política de retenção de mensagens e anexos	Não tem	V1 simples	Prazo por provedor com piso regulatório; registros exigidos pelo RGC não são apagados antes do prazo
Exportação e exclusão de dados de um contato (LGPD)	Recursos de privacidade	V1 simples	Pedido do titular, executado pelo admin; a exclusão preserva o que a lei obriga guardar
Histórico de demandas do consumidor exportável (protocolo, datas, motivo, síntese, encaminhamento)	Não tem	V1 simples	Registro estruturado com campos próprios e autoria; pode referenciar demanda externa sem copiar seu fluxo de gestão
Fuso horário por provedor	Community	V1	
Idioma por conta	Community	Não fazer	Fixo pt-BR
Logo do provedor na interface	Premium (marca)	V1 simples	Só logo e nome
White label completo	Premium	Não fazer	
Domínio próprio por provedor	Conforme oferta	V2	
Superadmin do SaaS	Community	V1	Sem acesso livre às conversas; todo acesso auditado
Criar e suspender provedor (reversível)	Community	V1	Suspender não apaga dados
Limites por plano (atendentes, números, storage)	Community (via conta)	V1 simples	Base para cobrar o SaaS
Feature flags e licenças pela interface	Community	Não fazer	Planos simples no código
Cobrança do SaaS dentro do chat	Não tem	Não fazer	Resolver fora do chat
13. Interface e implantação
Uma interface web rápida, em português, para quem atende 8 horas por dia. Seis áreas: Painel, Conversas, Contatos, Equipes, Relatórios, Configurações. Respostas prontas, etiquetas, horários e integrações ficam dentro de Configurações.

Funcionalidade	Chatwoot	Chat ISP	Implementação / motivo
Painel web	Community	V1	Seis áreas
Interface responsiva	Community	V1	Desktop primeiro; supervisor usa no celular
Painel lateral do contato na conversa	Community	V1	Dados do contato, histórico, cliente no ERP
Português do Brasil	Community	V1	Único idioma
Mais de 50 idiomas	Community	Não fazer	
Idiomas RTL	Community	Não fazer	
Modo escuro do painel	Community	V2	Design já nasce com tokens de cor para facilitar
Modo escuro do widget	Community	Não fazer	Sem widget
Atalhos de teclado essenciais	Community	V1 simples	Seção 04
Barra de comandos	Community	Não fazer	
Cores da marca do provedor	Premium	Não fazer	Só logo (seção 12)
App Android nativo	Community	Não fazer	
App iOS nativo	Community	Não fazer	
Push de app nativo	Community	Não fazer	
PWA instalável com push	Não tem	V2	Substitui app nativo
Notificações do navegador	Community	V1	
Acessibilidade básica (teclado, contraste)	Community	V1	Requisito de qualidade
Instalação própria (self-hosted)	Community	V1	Provedor grande pode ter instância dedicada
Instalação SaaS compartilhada	Community	V1	Vários provedores isolados
Docker / Docker Compose	Community	V1	
Helm / Kubernetes	Community	Não fazer	Docker em VM (ex.: Proxmox) atende
Microsserviços obrigatórios	Não é recurso	Não fazer	Um binário, modos web e worker
14. Requisitos técnicos que não podem ser cortados
Nada disso aparece numa tela, mas é o que separa um chat confiável de um que perde mensagem, envia em duplicidade ou vaza dados entre provedores. O detalhamento de cada um está na Parte B.

Requisito	Chatwoot	Chat ISP	Comportamento esperado
Identificador único por mensagem	Arquitetural	V1	Mapeia ID interno e ID externo com escopo de conector
Recebimento durável do webhook	Arquitetural	V1	Grava o evento bruto e só então responde 200
Validação de assinatura do webhook da Meta	Arquitetural	V1	
Deduplicação de eventos separada da identidade da mensagem	Arquitetural	V1	Evento repetido não repete efeito; a mesma mensagem recebe vários status (B.4.1)
Outbox de envio com estado “resultado incerto”	Arquitetural	V1	Timeout não gera reenvio cego (B.4.2)
Fila de processamento assíncrono	Arquitetural (Sidekiq)	V1	River sobre PostgreSQL
Filas separadas por criticidade e limite por tenant	Arquitetural	V1	Um provedor movimentado não ocupa todos os workers (B.4.6)
Retentativas com backoff	Arquitetural	V1	
Fila de falhas (DLQ ou equivalente)	Arquitetural	V1	Nenhum evento descartado em silêncio
Ordenação consistente de mensagens	Arquitetural	V1	Timestamp confiável do driver com desempate estável; separar ordem de exibição, ingestão e commits
Conciliação de status de mensagens	Arquitetural	V1	Corrige pendências após falhas
Concorrência em atribuição, transferência e troca de controlador	Arquitetural	V1	Lock otimista por versão; duas pessoas não assumem a mesma conversa
Rate limit global de envio por número	Arquitetural	V1	Reserva compartilhada, posse versionada e limites por driver; memória é apenas otimização (B.4.3)
Timeout, limite de concorrência e circuit breaker nas chamadas ao ERP	Não tem	V1	ERP lento não trava o chat
Download e guarda da mídia recebida	Arquitetural	V1	URL da Meta expira
Anexos em storage S3 compatível, separados por tenant	Arquitetural	V1	S3 gerenciado no SaaS; instalação dedicada com destino validado e backup externo
Acesso a anexos com URL assinada e expirável	Arquitetural	V1	Sem link público permanente
Validação de upload (tipo real, tamanho, duração)	Arquitetural	V1	
Conversão de áudio em worker com limites	Arquitetural	V1	ffmpeg com limite de duração, tamanho, CPU e memória
Tempo real com reconexão e ressincronização	Arquitetural	V1	Ao voltar, busca o que mudou desde o último evento
Sinalização entre instâncias via LISTEN/NOTIFY	Não tem (usa Redis)	V1	NOTIFY só avisa; cursor transacional por tenant (nunca confiar apenas em BIGSERIAL)
Autorização em cada endpoint e evento	Arquitetural	V1	Tenant, setor, caixa e conversa; incluir busca, histórico do contato, anexos, exportações e WebSocket
Papéis do PostgreSQL sem BYPASSRLS, FORCE RLS, políticas de leitura e escrita	Arquitetural	V1	B.4.4
Proteção CSRF, validação de Origin e autenticação do WebSocket	Arquitetural	V1	B.4.5
Cache com chave por tenant, TTL e invalidação	Arquitetural	V1	B.4.7
Isolamento por tenant em banco, cache, filas e anexos	Arquitetural	V1	
Limites de recursos por tenant	Arquitetural	V1	Um provedor não derruba os outros
Logs estruturados com tenant_id	Arquitetural	V1	
Métricas e alertas (filas, falhas, latência)	Arquitetural	V1	
Alerta de token do WhatsApp inválido	Parcial	V1	Detectar falha, avisar admin e mostrar canal degradado; não prometer prever toda revogação
Contabilização de uso reportado pela Meta	Não tem	V1 simples	Categoria, indicador de cobrança quando disponível e estado desconhecido; sem estimar dinheiro na V1 (B.4.9)
Backup e restauração testada	Externo	V1	Banco, anexos, configuração e recuperação de chaves; consistência e reaplicação de exclusões
Retenção e descarte de dados	Não tem	V1 simples	Seção 12
Migrações de banco versionadas	Arquitetural	V1	
Jobs sobrevivem a reinício e deploy	Arquitetural	V1	
Web e workers separáveis	Arquitetural	V1	Mesmo binário, modos diferentes
Busca full-text por tenant	Arquitetural	V1	PostgreSQL
Redis / Valkey	Obrigatório no Chatwoot	V2	Não faz parte do núcleo inicial; exceção somente por ADR aprovada conforme B.6
Teste de carga multi-tenant	Não tem	V1	Medir p95, CPU, RAM, I/O antes de prometer números
Provisionamento de provedor sem mexer em código	Arquitetural	V1	
Observabilidade, isolamento e condições de uso por conector/tenant	Arquitetural	V1	Número/instância vinculados a um tenant, credenciais segregadas, monitoramento de desconexão, aceite explícito para Web/Evolution e desligamento por feature flag
Suspensão reversível e auditada do provedor	Arquitetural	V1	
15. Necessidades específicas do atendimento de provedor
Índice dos requisitos especializados desta proposta, sem afirmar ausência absoluta em todas as versões ou integrações do Chatwoot. São regras de atendimento e pontos de conexão com sistemas existentes. Quase todos já aparecem nas seções acima; só o último item é novo.

Funcionalidade	Chat ISP	Onde está	Por quê
Protocolo de atendimento	V1	03, 06, 08	Local imutável, informado no início; associar protocolo ERP quando disponível; formato e obrigações validados conforme enquadramento do provedor
Busca por protocolo	V1	03	Cliente liga citando o número
Estado “Aguardando cliente”	V1	03	Base das métricas de tempo
Encerrar com motivo	V1	04	Alimenta o relatório por motivo
Menu de triagem nativo	V1	06	Padrão do atendimento de provedor
Setor de plantão fora do expediente	V1 simples	06	Queda de internet não espera horário comercial
Vínculo contato → clientes no ERP	V1	07	Mesmo número, vários contratos
Confirmação de identidade antes de dados sensíveis	V1 simples	08	Só o número do WhatsApp não basta
2ª via de boleto e Pix pelo ERP	V1 simples	08	Maior volume de pedido no financeiro
Contrato de integração com o ERP existente	V1 simples	08	Consultar contexto e obter documentos já suportados pelo ERP utilizado
Histórico de demandas exportável	V1 simples	12	Pedido do consumidor previsto no RGC
Exportar conversa em PDF	V2	03	Contestações e Procon
Reincidência	V2	09	Cliente que volta pelo mesmo problema
Custo da Meta por provedor	V2	02	Painel de custo; a contagem já é V1
Aviso operacional em lote (rompimento, manutenção)	V2	11	Template para lista vinda do ERP ou NOC; sem agendador nem segmentação
Incidente de falha massiva	V2	Novo	Associar atendimentos a incidente externo vindo do ERP/NOC; eventual encerramento em lote exige resultado explícito, sem desenvolver gestão de rede no chat
A.5 Controle do escopo
O núcleo da V1 é o webchat. Integrações são entregues pelas capacidades do ERP existente e dos conectores validados. Nenhum recurso financeiro ou de rede passa a fazer parte do chat por estar disponível em uma API externa.

Seção	V1	V1 simples	V2	Não fazer	Total
01. Canais de comunicação	5	1	5	14	25
02. WhatsApp: mensagens, regras do canal e operação	22	6	6	3	37
03. Inbox, conversas e organização	25	6	3	9	43
04. Mensageria e produtividade do atendente	13	5	3	6	27
05. Usuários, equipes e distribuição	14	4	2	6	26
06. Automações, horários e bot	8	6	1	14	29
07. Contatos	10	5	4	7	26
08. Integração fina com o ERP existente e a IA externa	12	12	8	4	36
09. Painel, relatórios, SLA e satisfação	13	4	6	8	31
10. IA nativa, base de conhecimento e marketing	0	0	1	27	28
11. Integrações, API e ecossistema	9	4	6	15	34
12. Administração, identidade e segurança	17	5	3	8	33
13. Interface e implantação	9	1	2	10	22
14. Requisitos técnicos que não podem ser cortados	39	2	1	0	42
Total	196	61	51	131	439
As contagens representam linhas da matriz 01–14, com referências cruzadas. Não são contagem de módulos, estimativa de esforço ou critério de qualidade. A seção 15 é um índice.

A.6 Estados, filas e resultado do atendimento
Conceito	Valores / regra
Estado	triage (Em triagem), open (Aberta), waiting_customer (Aguardando cliente), closed (Encerrada)
Controlador	menu, bot, human; versão própria controller_epoch
Responsável	Membership do atendente, quando atribuído; não é sinônimo de controlador
Adiamento	snoozed_until, motivo e destino de retorno; só em controle humano e conversa aberta, sem encerramento automático
Resultado do encerramento	resolved, no_response, referred_external, spam; obrigatório no fechamento
Motivo principal	Classificação do contato (ex.: lentidão, financeiro), independente do resultado
Ciclo	Trecho entre abertura/reabertura e encerramento; preserva histórico e métricas
Leitura	Marcador por atendente e conversa, separado de recibos do WhatsApp
Uma conversa ativa por tenant + inbox + contato interno na V1. Criar e reabrir precisa de exclusão concorrente e restrição no banco. Encerrada não é ativa; adiada continua ativa. Não abrir outra conversa ativa silenciosamente para contornar uma transferência ou falha do bot.

Evento	Transição e efeito obrigatório
Primeira mensagem atual recebida	Resolver identidade; criar/obter conversa; gerar protocolo; triagem ou fila padrão
Nova mensagem em “Aguardando cliente”	Aberta; terminar espera; invalidar aviso e encerramento agendados
Nova mensagem atual em adiada	Retirar adiamento e voltar à fila; tentar reservar o antigo responsável se elegível, caso contrário deixar sem responsável
Nova mensagem em encerrada dentro da janela X	Reabrir atomicamente com novo ciclo; manter protocolo; reiniciar roteamento da caixa sem repetir o protocolo como se fosse novo
Reabertura manual autorizada	Criar ciclo e manter protocolo, desde que não exista outra conversa ativa para o mesmo tenant/inbox/contato; caso exista, mostrar conflito e apontar o atendimento ativo
Nova mensagem fora da janela X	Nova conversa e novo protocolo; histórico anterior acessível conforme permissão
Resposta pública humana aceita pelo driver	Pode iniciar espera do cliente se nenhuma mensagem posterior do cliente já estiver pendente; falha/incerteza não inicia espera
Nota interna, reação ou evento técnico	Não inicia espera nem conta como resposta humana
Humano assume bot/menu	Alterar posse, cancelar pendências automáticas da posse antiga e registrar handoff
Bot indisponível ou sem resposta dentro do prazo	Transferir para fila humana; emitir alerta; confirmação HTTP do webhook não significa atendimento concluído
Menu recebe conteúdo não previsto	Oferecer texto numerado ou humano; após limite curto de tentativas configurado, encaminhar para setor padrão
Cliente pede humano	Encaminhar diretamente à fila autorizada, inclusive quando o bot estiver ativo
Transferir a outro atendente	Validar setor/caixa, reservar vaga e trocar responsabilidade atomicamente
Transferir a setor	Trocar setor, remover responsável, abrir intervalo de fila e revogar acesso de quem perdeu permissão
Adiar	Exigir motivo e data; liberar vaga; preservar antigo responsável como preferência de retorno, não como vaga ocupada
Encerrar	Exigir versão atual, motivo, resultado e síntese; fechar ciclo; liberar vaga; despedida opcional usa a outbox
Encerramento por inatividade	Revalidar estado, última resposta elegível, ausência de nova mensagem, posse humana e ausência de adiamento/pendência; resultado no_response, síntese automática factual e encaminhamento “encerrado sem retorno”
Evento histórico/importado/duplicado	Registrar ou deduplicar; não reiniciar menu, fila, janela ou relógios usando a hora da importação
“Encerrar com motivo” é uma operação local idempotente. A falha da despedida é visível e não converte um envio falho em sucesso. Mensagem recebida em corrida com encerramento segue a regra de versão/reabertura; não pode desaparecer em conversa fechada sem sinalização.

Capacidade: abertas e aguardando cliente atribuídas consomem uma vaga; adiadas, encerradas e triagem do bot não. Atribuição manual e automática usam a mesma reserva. Sem vaga, transferência manual retorna conflito; supervisor pode desatribuir outra conversa explicitamente, sem sobrecarga silenciosa. Retorno de adiamento tenta o antigo responsável elegível, senão volta à fila. Presença agrega sessões ativas; fechar uma aba não torna offline quem segue conectado em outra.

Inatividade: medir a partir da última resposta pública humana elegível. Aviso automático não reinicia o relógio nem conta como resposta humana. Se o aviso configurado falhar ou ficar incerto, não executar o encerramento condicionado a ele; mostrar pendência. Fora da janela oficial, não improvisar mensagem livre ou template; a automação fica pendente para revisão.

A.7 Experiência do webchat e comportamento degradado
Conversas é a área principal: lista/filas, histórico e compositor, com contexto do contato recolhível. A operação do chat não depende de o painel ERP carregar.
Mostrar fila, responsável, controlador, estado, protocolo e saúde do canal sem exigir abrir Configurações.
Distinguir visualmente fila local, envio em andamento, aceitação pelo conector, entrega, falha e resultado incerto. Rascunho não é mensagem enviada.
Preservar posição de leitura ao carregar histórico; nova mensagem não arrasta quem está lendo mensagens antigas. Oferecer indicador de novas mensagens e retorno ao fim.
Navegação por teclado, foco previsível, rótulos acessíveis, contraste e estados de erro/carregamento/vazio em todas as seis áreas.
Rascunhos isolados por usuário, tenant e conversa. Limpar dados locais em logout/troca de conta; não deixar histórico sensível persistido indefinidamente no navegador.
Sem conexão com o servidor, manter rascunho e sinalizar indisponibilidade. Não apresentar envio offline como aceito; repetição após reconexão reutiliza a chave da operação.
Mensagem de tipo não suportado aparece como tal, com referência e orientação; não fica invisível.
Queda do ERP mostra “ERP indisponível”; queda da Evolution afeta apenas inboxes desse conector; queda da IA encaminha para humano.
Vídeo enviado, widget público, app nativo, editor de fluxos, campanhas e módulos ERP continuam fora da V1.
PARTE B — Stack e regras de implementação
B.1 Arquitetura
Monólito modular: API e workers Go compartilham os módulos de domínio e podem rodar no mesmo processo ou separadamente. PostgreSQL guarda o estado do núcleo; S3 guarda anexos. React é o painel web. A Evolution é um serviço externo opcional, com sua própria operação e dependências.

Painel web React

Chat ISP: API e workers Go

PostgreSQL: dados, jobs e eventos

Anexos S3

Contrato de conectores WhatsApp

Meta Cloud API

Evolution Web opcional

API do ERP existente

IA externa autorizada

O diagrama mostra dependências, não ordem de processamento. Eventos recebidos entram pelo endpoint específico do driver e são persistidos antes da confirmação. Nenhum serviço externo é chamado dentro de uma transação de domínio que permaneça aberta durante a chamada de rede.

B.2 Princípios
Webchat primeiro. Arquitetura e marcos devem produzir atendimento utilizável cedo. Não criar módulos de ERP, plataforma de bots ou infraestrutura distribuída sem necessidade comprovada.
Autoridade por domínio. Chat ISP governa conversas; ERP governa cadastro comercial e operações; conector governa evidências remotas de envio/recebimento. Evidência ausente não é sucesso.
SQL e limites explícitos. RLS, filtros por tenant e integridade referencial trabalham juntos. Autorização por setor/caixa é adicional ao isolamento por tenant.
Transações locais curtas. Estado de domínio, evento de tempo real e agendamento durável são consistentes no mesmo commit quando pertencem à mesma operação.
Infraestrutura substituível. Presença, eventos, cache, limitador e storage ficam atrás de interfaces pequenas. Não construir abstrações para dezenas de fornecedores imaginários.
Compatibilidade controlada. Fixar versões no repositório; validar upgrades de Graph API, Evolution e banco. “Mais recente” não é versão de produção.
Incerteza explícita. Não existe garantia de exatamente uma entrega remota apenas com código local. Repetição de comando local é tratada separadamente de confirmação do provedor externo.
Licenças e evidências. Consultar fontes primárias e registrar versão/data; não fundamentar decisão técnica em afirmação editorial não verificada.
B.3 Stack
B.3.1 Backend e identidade
Camada	Escolha	Limite / motivo
Linguagem	Go, versão estável suportada fixada no projeto, mínimo 1.25	Mesma base técnica do NexoISP; medir desempenho no produto
HTTP	chi + Huma v2	API tipada/OpenAPI; webhook bruto e WebSocket diretamente no chi
WebSocket	coder/websocket	Tempo real, autenticação e controle de origem
Configuração	caarlos0/env + validação de inicialização	Falhar com mensagem segura quando faltar configuração obrigatória
Logs	log/slog em JSON	IDs técnicos; sem conteúdo de mensagens ou credenciais por padrão
Sessão	Cookie __Host-session, estado no PostgreSQL	HttpOnly, Secure, SameSite=Lax, Path=/; revogação e expiração
CSRF	http.CrossOriginProtection + token de sessão	API do painel separada da API Bearer e dos webhooks autenticados
Senha	Argon2id, parâmetros calibrados e versionados	Não gravar senha reversível
MFA	pquerna/otp para TOTP + códigos de recuperação com hash	Opcional, com ativação pelo titular; fator confirmado continua exigido no login (decisão de 06/11/2026)
Tokens de integração	Aleatórios e opacos, hash no banco	Escopos, expiração configurada, rotação e revogação; emissão mostra o valor uma vez
Segredos recuperáveis	AES-GCM, chaves versionadas fora do banco	Contexto autenticado do tenant/entidade; detalhes em B.4.14
E-mail transacional	SMTP/serviço já disponível, por interface pequena	Convite e recuperação de senha; não cria canal de atendimento por e-mail
B.3.2 Dados, jobs e tempo real
Camada	Escolha	Regra
Banco	PostgreSQL 18, minor fixado	Backup, migrações, RLS e observabilidade
Acesso	pgx/v5 + pgxpool + sqlc	SQL explícito, parâmetros e contextos de tenant
Migrações	goose, SQL versionado	Papel de migração separado do runtime
Jobs	River open-source + River UI restrita à operação	Enfileirar na transação quando ligado a alteração de domínio; UI nunca pública nem disponível ao admin de um tenant
Coordenação de envio	Lease e reservas no PostgreSQL	Memória pode otimizar; não é autoridade entre processos
Eventos	realtime_events + contador transacional por tenant + LISTEN/NOTIFY	NOTIFY é sinal; recuperação vem da tabela
Cache	Ristretto para dados permitidos	Chave por tenant, TTL e invalidação; regras em B.4.7
Presença	Sessões de conexão e heartbeat no PostgreSQL + memória	Agregar múltiplas abas/sessões e aplicar tolerância de desconexão
Busca	Full-text PostgreSQL + unaccent + pg_trgm conforme campo	Português; índices e planos medidos, sem promessa de escala não testada
Conexões	Pools limitados por função	LISTEN em conexão dedicada; orçamento separado para requisições, workers e tarefas de operação
O núcleo não exige Redis/Valkey. Dependências próprias de uma versão da Evolution não são dependências do núcleo e ficam isoladas. Não colocar cache, sessões remotas da Evolution ou histórico financeiro dentro do banco de domínio por conveniência.

B.3.3 Frontend
Camada	Escolha	Uso
Aplicação	React 19 + TypeScript strict + Vite	SPA estática, desktop primeiro e uso responsivo
Rotas	React Router	Seis áreas e links diretos autorizados
Dados remotos	TanStack Query	Chaves incluem tenant; invalidar/limpar ao mudar sessão ou permissões
Estado local	Zustand	Seleção, filtros e rascunhos; servidor é autoridade de estados e permissões
Formulários	React Hook Form + Zod	Validação de UX; autorização e validação final no backend
Listas	TanStack Virtual	Histórico e filas longas, preservando rolagem e foco
Interface	Tailwind CSS + shadcn/ui, fonte Geist	Tokens de design, acessibilidade e densidade adequada ao atendimento
Cliente HTTP	openapi-typescript + openapi-fetch	Gerado a partir do OpenAPI; não duplicar DTOs manualmente
Tempo real	Cliente WebSocket com reconexão e cursor	Resposta HTTP contém o estado autoritativo após ações; evento não é único modo de atualizar a tela
Áudio	MediaRecorder com detecção de formatos	Converter no worker conforme contrato do driver; testar navegador e codec efetivos
Notificações	Interface, som e Notification API	Permissão explícita; sem promessa de push com navegador fechado na V1
B.3.4 Integrações, mídia e infraestrutura
Camada	Escolha	Regra
WhatsApp oficial	Driver próprio com net/http	Graph API fixada e documentada; sem intermediário obrigatório
WhatsApp Web	Driver HTTP para Evolution validada	Serviço externo opcional; não embutir Baileys no binário
ERP	Adaptador do ERP existente, net/http + sony/gobreaker	Timeout, limite por conexão/tenant e DTO mínimo
IA	Webhook HMAC + API Bearer	Um gancho por caixa e posse explícita
Storage	S3 gerenciado no SaaS; dedicado com destino validado	aws-sdk-go-v2; testar compatibilidade efetiva, URLs assinadas e backup
Desenvolvimento S3	Serviço local compatível, como Garage configurado para dev	Não pressupor que um nó local ofereça redundância de produção; MinIO não é dependência desta especificação
Conversão	ffmpeg em worker limitado	Entradas locais validadas; sem buscar URLs arbitrárias durante a conversão
Deploy	Docker Compose, Caddy, Debian 13	Instalação compartilhada/dedicada; Proxmox quando for o ambiente da operação
Banco backup	pgBackRest + WAL externo	Restauração em ambiente isolado e teste periódico
Anexos backup	Segundo destino independente	Política que não propague exclusão acidental imediatamente sem recuperação
Métricas	Prometheus client_golang	Baixa cardinalidade; não usar contato, telefone ou ID de mensagem como label
Painéis/alertas	Grafana + Alertmanager, centralizados quando possível	Filas paradas, erro/uncertain, canal desconectado, backup atrasado, recursos e ERP degradado
Testes	go test, testcontainers-go, Vitest, Testing Library, Playwright, k6	Banco real para isolamento e concorrência; carga com metas definidas
Qualidade	golangci-lint, govulncheck, ESLint, TypeScript	CI reproduzível; build e migrações antes de deploy por tag
B.4 Contratos de consistência e segurança
B.4.1 Recebimento: autenticar, persistir e rotear
Meta: identificar o aplicativo pelo endpoint/configuração e validar HMAC do corpo original com o App Secret desse aplicativo. O endpoint de verificação do webhook usa o token próprio de verificação, distinto do App Secret.
Evolution: endpoint e autenticação próprios, conforme a versão validada. Não presumir assinatura Meta nem inventar HMAC que a instalação não emita. Restringir a origem de rede quando aplicável.
Persistir envelope autenticado e agendamento durável antes do HTTP 200. Se o armazenamento falhar, não confirmar sucesso. Limitar tamanho do corpo e taxa antes de aceitar a carga.
Um envelope pode conter itens de vários números/tenants. ingress_deliveries guarda a entrega original em área restrita anterior ao tenant; não pertence a uma inbox arbitrariamente escolhida do primeiro item.
O roteador usa o registro confiável app–WABA–número ou endpoint–instância. Gera itens segregados em webhook_items, cada um com seu tenant e inbox, mais jobs tenantizados. Gravação do item e agendamento precisam ser atômicos; reprocessar o envelope não duplica itens.
Número/instância desconhecidos ou relação incompatível vão para quarentena operacional com alerta. Nunca adotar tenant enviado pelo remetente nem encaminhar para “tenant padrão”.
Mensagem: identidade externa com escopo de conexão, chave única (tenant_id, connector_id, external_message_id). Itens sem ID estável exigem regra documentada do driver; hash de conteúdo não deve eliminar mensagens legítimas iguais.
Status: entidade separada. Deduplicar por identidade de evento quando existir ou por chave documentada que inclua conteúdo relevante. Não perder metadados de cobrança ou corrigir estados com simples ordenação alfabética. Cada driver define sua máquina de evidências.
Status de mensagem ainda desconhecida fica pendente para conciliação. Correlacionar com envio interno quando o driver devolver a referência suportada. Não atribuir a outra mensagem só por proximidade temporal.
Timestamps externos são usados para exibição com desempate estável e para regras do canal quando confiáveis. A hora de ingestão é registrada separadamente. Histórico importado não é mensagem atual; eventos atrasados não podem retroceder relógios por simples sobrescrita.
Testar envelopes com múltiplos tenants, assinatura inválida, payload repetido, status antes da resposta HTTP e falha entre persistência e processamento. Corpo bruto tem acesso e retenção próprios; não é devolvido pela API do tenant nem por logs de erro.

B.4.2 Comandos idempotentes e outbox
Idempotência local: ações de envio e solicitações ao ERP recebem client_request_id/Idempotency-Key, gerado uma vez por intenção do usuário/integrador. A chave é escopada por tenant, principal autenticado e operação. Hash canônico do pedido identifica reutilização com conteúdo diferente, que retorna conflito. Repetição do mesmo pedido retorna o mesmo recurso/resultado; chamadas simultâneas são serializadas por restrição no banco.

Autenticação/autorização são verificadas inclusive em replay. Não devolver resultado antigo a quem perdeu acesso. A transação grava comando, mensagem, outbox, evento de tempo real e job necessários. O frontend reutiliza a chave ao recuperar uma requisição cujo resultado não recebeu.

Chaves de mensagens permanecem vinculadas ao recurso durante sua retenção; para operações temporárias, documentar horizonte de repetição e expiração. Nunca apagar uma chave enquanto sua operação estiver ativa/incerta. Reenvio humano intencional é uma nova operação, ligada à anterior.

Estado local do envio	Significado	Tratamento
queued	Persistido e aguardando despacho	Validar estado atual, permissão, posse, capacidade e canal antes da reserva
sending	Tentativa despachada/iniciada	Guardar ID da tentativa, versão de lease e momento de despacho
accepted	Conector confirmou aceitação com evidência documentada	Exibir aceitação separada de entrega/leitura
failed	Rejeição definitiva conhecida	Erro útil sem segredo; não inventar sucesso
retrying	Contrato/evidência comprova que repetir é seguro	Backoff, jitter e limite de tentativas
uncertain	Resultado remoto desconhecido	Conciliar; não reenviar automaticamente
cancelled	Intenção pendente invalidada antes do despacho	Registrar motivo; não enviar
Na Cloud API, usar biz_opaque_callback_data onde suportado na versão/endpoint, como correlação e não como garantia de idempotência. Na Evolution, usar o contrato documentado daquela versão. 429/5xx são classificados pelo contrato e pela evidência; código HTTP isolado não autoriza reenvio cego.

Se o processo cair em sending, uma tentativa sem prova de que não iniciou chamada externa é recuperada como uncertain. Jobs repetidos devem consultar a tentativa/outbox antes de qualquer rede.

Sem confirmação após prazo configurado, mostrar “envio não confirmado”. Reenvio manual exige decisão explícita, nova chave e vínculo com a tentativa anterior; recibo atrasado ainda pode confirmar a original. Automação incerta não se repete sozinha.

B.4.3 Coordenação e limites de envio
Um coordenador por conexão/número com lease owner_id, lease_version, expires_at e relógio do PostgreSQL. Reserva, renovação e recuperação são atômicas.
O worker antigo não pode reservar novos envios depois de perder o lease. Reservas contêm tentativa e versão; não reutilizar reserva antiga como nova tentativa.
Crédito/limites do driver são compartilhados no banco. Novo coordenador não ganha um bucket cheio. Memória é somente otimização.
Ordenar intenções locais por destinatário e serializar o despacho necessário. Isso não garante ordem física de entrega no WhatsApp. Definir se um envio incerto mantém o destinatário bloqueado até decisão; padrão conservador: pausar sequências automáticas dependentes e sinalizar ao humano.
Validar janela/template, consentimento aplicável, canal ativo, tenant ativo e posse imediatamente antes do despacho. Se a regra mudou durante a espera, cancelar/reclassificar, sem enviar algo antes permitido e agora inválido.
Fencing local não cancela requisição remota já iniciada. A fronteira de despacho é registrada e pedidos em voo têm tratamento explícito de incerteza.
Aplicar limites oficiais comprovados à Cloud API e limites operacionais próprios à Evolution. Não usar mecanismos de evasão das proteções do WhatsApp.
B.4.4 Isolamento por tenant e áreas anteriores ao tenant
Papel / área	Permissão
chatisp_owner	Migrações; dono das tabelas; credencial ausente do processo normal
chatisp_app	Dados de atendimento com NOBYPASSRLS, sem ser dono, sem alteração de políticas
chatisp_platform	Tenants, planos e metadados operacionais autorizados; sem leitura de conversas/segredos
chatisp_ingress	Validar/persistir envelopes, resolver vínculos autenticados e produzir itens tenantizados por operações restritas; sem leitura de conversas
Identidade/autenticação	Acesso mínimo a usuários, sessões, hashes e memberships por caminho restrito; não abrir essas tabelas globalmente para consultas do painel
Operação de jobs	Tabelas River e coordenação global; argumentos contêm IDs mínimos, não corpos de mensagens, tokens ou CPF
São papéis/pools no mesmo sistema, não novos microsserviços. Se funções privilegiadas forem necessárias ao roteamento ou autenticação, fixar search_path, revogar execução pública, limitar parâmetros e testar cada função. Nenhum atalho usa superusuário no runtime.

Todas as tabelas de domínio com tenant_id têm ENABLE/FORCE ROW LEVEL SECURITY, políticas USING e WITH CHECK, e contexto por transação via WithTenant/set_config(..., true). O tenant vem de autenticação/vínculo confiável, nunca é aceito do JSON sem validação. A entrada sem contexto é rejeitada pelo acesso de domínio; as políticas também falham fechadas. Não depender de uma consulta vazia necessariamente avaliar uma função SQL para provar o erro.

Consultas sqlc continuam filtrando tenant. Relações entre entidades tenantizadas usam FKs compostas (tenant_id, entity_id) e unicidade correspondente. As verificações referenciais do PostgreSQL não substituem esse isolamento S03. Chaves externas de conexão impedem registrar o mesmo recurso remoto ativo em dois tenants sem migração explícita.

Dados globais (users, registro de apps, ingresso bruto e River) possuem autorização específica, sem a falsa regra “tenant_id em tudo”. webhook_items é tenantizado; o app nunca precisa ler o envelope multitenant original. Dados de auditoria de plataforma ficam separados dos de tenant.

RLS por tenant não concede acesso a todos os setores. Aplicar a matriz de autorização em mensagens, busca, contatos/histórico, anexos, relatórios, exportações e eventos. O contato existir em um setor não libera automaticamente suas conversas em outro.

B.4.5 Sessão, API e revogação
Sessão revogável com expiração por inatividade e absoluta; IDs de sessão com hash no banco. Recuperação de senha/convites: tokens de uso único, prazo curto e limitação de tentativas.
TOTP: segredo cifrado; códigos de recuperação com hash, uso único e proteção contra força bruta. Mudança/reset de MFA exige fluxo auditado.
API do painel usa cookie + CSRF/Origin. API pública usa Bearer sem aceitar cookie como substituto. Webhooks usam autenticação do conector; não exigir token CSRF do painel nesses endpoints.
WebSocket exige sessão e Origin permitida; autorizar cada assinatura, consulta de recuperação e evento. Revogação/desativação envia sinal de fechamento e é revalidada periodicamente como defesa adicional.
Definir prazo máximo de revogação observável no sistema e testá-lo; não prometer revogação instantânea só porque a sessão está no banco. Operações de escrita sempre revalidam a autorização vigente.
Transferência de setor/remoção de membership revoga acesso futuro e instrui o cliente a remover dados da tela/cache. Não alegar apagar informação que o usuário já viu ou baixou.
Supervisor vê todos os setores do tenant conforme a matriz da seção 12. Atendente vê apenas interseção de setores e caixas permitidos; estar atribuído não contorna esse controle.
B.4.6 Filas River e recuperação
Fila	Trabalho	Regra
ingress	Autenticação já concluída; expansão/roteamento de envelopes	Área restrita e backlog monitorado
inbound	Itens e mensagens tenantizados	Alta prioridade; isolamento e idempotência
outbound	Despacho WhatsApp	Outbox, posse e reservas antes de rede
media	Download, validação e conversão	Limites de CPU, memória, bytes e tempo
integrations	ERP e webhooks de saída	Timeouts e isolamento de falhas por conexão
scheduled	Adiamento, inatividade, conciliação e retenção	Revalidar condição e versão na execução
maintenance	Limpeza e agregações	Não concorrer sem limite com atendimento
Separação de filas não prova justiça entre tenants. Limitar concorrência por tenant por reserva atômica com expiração e recuperação; nunca COUNT seguido de início independente. Medir também latência de tenants pequenos quando um grande enche a fila; limite de concorrência sozinho não garante seleção justa dos jobs.

Reservas vencidas impedem trabalho novo do antigo dono; a ocupação de chamada externa em voo deve respeitar timeout/limites. Retentativas têm limite, backoff e fila de falhas com motivo. Reprocessamento administrativo usa os mesmos controles e não duplica efeitos já concluídos. Alteração de regras/configuração invalida jobs obsoletos por versão.

B.4.7 Cache
Cache backend restrito a configuração, metadados de caixa e templates. Permissões só podem ser cacheadas com versão e revalidação suficiente para a revogação contratada; nunca confiar apenas em TTL para ação sensível. Chave inclui tenant e identidade relevante.

Não cachear mensagens/conversas no backend como segunda autoridade. O cache de visualização do frontend é permitido, segregado por tenant e limpo em logout/mudança de acesso. Valores de ERP são transitórios, com indicação de quando foram consultados; não persistir cópia financeira para preencher tela durante indisponibilidade.

B.4.8 Integração com o ERP existente
Contrato inicial pequeno: SearchCustomer, GetCustomerSummary, ListOpenInvoices, GetInvoiceDocument/GetPixCode, conforme capacidades reais. Nomes são internos; não implicam que o fornecedor tenha endpoints com esses nomes. Sem suporte, retornar “capacidade indisponível”.

Leituras rápidas: timeout curto e circuit breaker por conexão. O timeout do ERP não ocupa indefinidamente uma requisição do chat.
Ação com efeito externo: job com chave local e contrato explícito de idempotência remota/consulta de resultado. Timeout ambíguo segue conciliação ou revisão, não repetição cega.
“Obter segunda via” pode ser leitura ou geração conforme a API; classificar pelo efeito real. O chat nunca implementa cálculo, geração financeira ou compensação.
Documento retornado pode virar anexo da conversa, preservando evidência do que foi enviado e sua retenção. Dados financeiros consultados não viram tabelas de contas a receber.
Serviço de integração centraliza autorização, identidade verificada, rede, credenciais, auditoria e tratamento de falha. Handlers e bots não chamam o adaptador diretamente.
Aprovação, quando requerida para capacidade já incluída, guarda tenant, principal, conversa, cliente/contrato, parâmetros/hash, validade e aprovador. Consumir uma vez e revalidar antes da execução. Mudança de parâmetros invalida a aprovação.
Identificação por telefone/documento não equivale a autorização. Reutilizar validação segura do ERP/provedor e receber referência verificável da confirmação. Não criar coleta padrão de CPF completo pelo WhatsApp S01.
A falta de contrato real leva a fake e pendência documentada, nunca a endpoints supostos. O webchat continua implementável e testável sem o ERP; a capacidade integrada só é declarada entregue após teste real autorizado.
B.4.9 Uso e preços da Meta
V1 coleta evidências disponíveis: ID da mensagem, categoria reportada, indicador de cobrança, data e origem da informação. Guardar “desconhecido” quando faltar dado; ausência de recibo não significa gratuidade. Agregados precisam ser reconstruíveis a partir dos eventos e evitar dupla contagem.

Não fixar nesta especificação que todas as mensagens de serviço passaram a ser cobradas em 01/10/2026. Na revisão de 28/10/2026 a página pública oficial ainda descrevia gratuidade, enquanto referências secundárias da versão anterior apontavam mudança; a página técnica detalhada exigia autenticação ou não ficou acessível. A alteração permanece sem confirmação suficiente para virar regra do produto S02.

Valores monetários ficam na V2, com tarifas versionadas, vigência, moeda, mercado, fonte oficial e conciliação. Não inferir cobrança só pela categoria. Tráfego Web não é tarifado como Cloud API, mas tem custos de operação e condições próprias. A concisão do menu independe de preço.

B.4.10 Tempo real, cursor e carga inicial
Cada mudança visível grava evento durável na transação de domínio. Bloquear realtime_tenant_cursors, incrementar com UPDATE ... RETURNING e inserir evento com (tenant_id, seq) único. BIGSERIAL sozinho não garante ordem de commit.
Adquirir o contador perto do final da transação; seguir ordem global consistente de locks entre entidades e cursor para evitar deadlocks. Sem chamada externa enquanto o lock estiver aberto.
NOTIFY contém apenas sinal mínimo; listener dedicado busca eventos confirmados. Polling de recuperação evita depender de receber todos os sinais.
Resposta de recuperação contém eventos autorizados e next_cursor, que representa a faixa examinada pelo servidor, inclusive quando nela só existiam eventos invisíveis ao usuário. O cliente não deduz cursor pelo último item da lista.
Carga inicial e cursor devem formar fronteira consistente: usar snapshot transacional coerente ou assinatura + buffer/replay com deduplicação. Não carregar lista e depois obter um cursor novo que pule mudanças ocorridas no intervalo.
Eventos carregam IDs/tipo/versão com payload mínimo; o cliente refaz consultas autorizadas quando necessário. Configuração/permissão alterada invalida visualizações relevantes.
Histórico de eventos expurgado retorna instrução explícita de ressincronização. Nova carga recebe nova fronteira; não fingir recuperação parcial como completa.
Backpressure limita buffers de clientes lentos. Ao exceder limite, desconectar com recuperação por cursor em vez de consumir memória sem limite.
Testar commit concorrente, rollback, reconexão, snapshot inicial, lacunas por autorização, alteração de setor e expurgo. Medir contenção por tenant; arquitetura alternativa exige prova equivalente.

B.4.11 Posse, bot e mensagens em trânsito
conversations.version controla edição concorrente geral. controller_epoch muda somente quando a autoridade de envio muda ou é invalidada; não usar qualquer alteração cosmética para cancelar uma resposta válida.

Mensagem automática registra controlador, controller_epoch, origem e versão da regra que a criou. API do bot recebe a epoch concedida e verifica, sob exclusão transacional com a tomada humana, se o bot ainda é dono. Resposta com epoch antiga retorna conflito, inclusive após bot → humano → bot.

Assumir/desligar o bot incrementa epoch, transfere autoridade e cancela envios automáticos ainda não despachados na mesma transação. O worker confere epoch e condições no ato de reservar/despachar; uma simples leitura anterior à reserva não basta. Encerramento solicitado pelo bot exige escopo explícito, posse/epoch vigente e resultado/síntese, sem simular participação humana.

A serialização define uma fronteira: tomada humana anterior ao despacho impede o envio; tomada posterior encontra a tentativa já em voo. A UI registra esse caso, e não promete impedir a entrega remota de uma requisição já iniciada.

Handoff tem ID de evento, prazo e retentativas idempotentes. Indisponibilidade do receptor não impede a tomada humana. Receber HTTP 200 do bot significa que o webhook foi recebido, não que a conversa foi resolvida. bot_deadline e monitor de progresso devolvem a conversa ao humano ao vencer o prazo.

B.4.12 Protocolo e histórico de atendimento
Protocolo local criado de forma atômica por tenant/período e imutável após comunicação. Formato externo configurado segundo necessidade aplicável; não confundir contador local com obrigação regulatória automaticamente atendida.
Registrar tentativa e evidência de comunicação do protocolo ao cliente. Se o primeiro envio falhar, mostrar pendência; gerar o número no banco não comprova que ele foi informado.
external_protocol é referência adicional ao ERP; sua ausência/falha não bloqueia a criação do atendimento. Não substituir silenciosamente o que já foi comunicado.
Reabertura do mesmo atendimento conserva protocolo e cria ciclo. Nova demanda explícita gera novo protocolo e vínculo com histórico anterior quando apropriado.
Registrar tipo/classificação, síntese, encaminhamento, resultado, datas e autoria como dados de atendimento. Isso não é módulo de OS ou gestão de obrigações financeiras.
Se a demanda segue no ERP, resultado é referred_external e a referência é preservada. O chat não afirma que encerrar a conversa encerrou a demanda externa.
Exportação inclui atendimentos abertos e encerrados autorizados, com referências externas quando existentes. Não apresentar o extrato do chat como histórico completo de todos os canais do provedor sem integração que o comprove.
Retenção e fornecimento de histórico são configurados conforme enquadramento validado do provedor e classe de dado; os requisitos gerais de atendimento constam da orientação da Anatel S04.
B.4.13 Drivers, identidade e janela do canal
channel=whatsapp; connector_type=meta_cloud|evolution_web; connector_id identifica uma conexão estável e sua origem. Troca de driver cria nova conexão e preserva referências históricas. Não reutilizar o namespace antigo de IDs para um novo conector.

Contrato obrigatório: envio de texto/mídia, normalização de eventos, saúde, desconexão e capacidades. Templates, interação e dados de uso são capacidades específicas, não métodos obrigatórios que retornam sucesso falso.

Capacidade	Cloud API	Evolution/Web
Texto, mídia e recebimento	Conforme contrato oficial	Conforme testes da versão instalada
Entrega/leitura	Evidências efetivamente recebidas	Apenas eventos que a versão reporta
Templates aprovados	Sim, propriedade da WABA S07	Não simular templates oficiais
Janela de 24 horas da plataforma	Validada antes de enviar	Não apresentar como contrato oficial do Web
Interativos	Conforme tipos/limites oficiais	Só quando verificados; fallback texto numerado
Preço/uso Cloud	Dados oficiais disponíveis	Não inferir tarifa Cloud para tráfego Web
Mensagens fora do painel	Fluxos suportados na modalidade contratada	Classificar eco e origem; não atribuir automaticamente a um atendente
Identidade: contacts.id é interno. contact_channel_identities mapeia origem e identificador externo. Telefone é atributo opcional/normalizado; nomes, JIDs e LIDs não autorizam mescla automática sem vínculo confiável. Resolver mapeamentos documentados, preservar histórico e registrar reconciliação S06. Não presumir que um contato possua um único contrato ERP.

Janela oficial: channel_contact_state guarda a última mensagem elegível por tenant + número/conexão oficial + identidade externa. Usar timestamp confiável sem retrocesso, não a hora de reprocessamento. Criar/encerrar uma conversa local não abre janela. last_customer_message_at na conversa serve às regras locais e não é a única autoridade do canal. Validar também antes do envio efetivo, após espera em fila S01.

Eventos Web: distinguir inbound atual, eco de envio, envio por dispositivo externo, sincronização histórica, edição/revogação e evento não suportado. Histórico não dispara automações; eco não duplica mensagem já correlacionada. Eventos de grupos, canais e status fora do escopo não viram atendimentos individuais. Apresentar lacuna de sincronização quando recuperação não for comprovada; não prometer recuperar todo tráfego perdido S08.

Envio por dispositivo externo recebe autoria “dispositivo externo”, nunca o nome inventado de um atendente. Por padrão, ao detectar intervenção humana externa atual, invalidar posse automática e sinalizar revisão humana. O controle exclusivo de posse só pode ser garantido para envios iniciados pelo Chat ISP.

Conexão Web: QR temporário acessível só ao admin autorizado, sem log/cache persistente; estado de sessão, desconexão e reconexão visíveis. Validar release, licença, ativação, backup de sessão e dependências. Repositório e releases registram condições adicionais; aprovação da implantação não significa homologação da Meta S05.

B.4.14 Dados, consentimento, retenção e chaves
Segredos de tenant são cifrados com chave/contexto por tenant. Segredo de app Meta compartilhado usa contexto próprio do app e área restrita; não pertence artificialmente a uma inbox.
Chaves mestras ficam fora do banco, com identificador de versão, recuperação externa e rotação planejada. AES-GCM exige nonce único por chave; usar biblioteca adequada e AAD com tenant/entidade/campo para impedir transplante de ciphertext.
Senhas, códigos de recuperação e tokens que só precisam ser verificados ficam com hash adequado. Segredo de TOTP e credenciais externas precisam de cifra recuperável.
CPF/CNPJ opcional autorizado: cifra e máscara; busca exata por HMAC com chave protegida, escopada por tenant. Não usar hash simples nem índice de busca contendo o documento em claro.
Conteúdo de mensagens, mídia, envelope bruto, logs e resultados de jobs são classes diferentes. Uma informação pessoal recebida no corpo não fica protegida só porque o campo do contato é cifrado. Limitar coleta, acesso e retenção; não registrar corpos em log. Banco, volumes e backups devem ter proteção em repouso; campo cifrado e criptografia de volume têm finalidades diferentes.
contact_consents registra finalidade, canal/número ou escopo aplicável, origem, evidência mínima, data e revogação. A regra vigente determina quando opt-in é necessário; contato criado não implica consentimento. Opt-out cancela mensagens proativas pendentes aplicáveis. Distinguir isso de atendimento solicitado pelo próprio cliente S01.
Exclusão e exportação seguem autorização e retenção por classe de dado. Preservar o mínimo necessário quando houver obrigação de guarda; expurgar cópias temporárias, índices e anexos elegíveis. Não aplicar uma duração única a todos os dados por associação genérica à LGPD/RGC.
Restaurar backup pode trazer dados posteriormente excluídos: manter procedimento/registro mínimo de exclusões e retenção para reaplicar decisões antes de reabrir o serviço. Não prometer exclusão imediata de toda cópia de backup imutável.
B.4.15 URLs, iframe e mídia
URLs de ERP, bot, webhook e download são superfícies de saída do servidor. Restringir protocolos, destinos, portas, resolução DNS e redirecionamentos; bloquear acesso indevido a loopback, metadados da infraestrutura e redes de outros tenants. ERP privado autorizado usa allowlist/rede explicitamente configurada, não uma exceção global a IP privado. Validar destino efetivo no acesso e não apenas no cadastro; aplicar limites de bytes/tempo S09.

Iframe: CSP/origens autorizadas, sandbox mínimo, postMessage com origem e janela verificadas, token de contexto curto e escopado a usuário/tenant/conversa. Preferir troca por POST/mensagem validada, sem dados pessoais ou credenciais em URL. O backend reautoriza cada ação; posse do token não concede acesso amplo ao ERP. Se a página existente proibir incorporação, abrir externamente sem tentar contornar sua proteção.

Anexos: validar tipo real, tamanho, duração e nome seguro. Chaves de objeto incluem namespace opaco por tenant; autorização antes de URL assinada. Downloads e previews isolam conteúdo ativo; não executar HTML/SVG enviado pelo cliente no contexto do painel. URLs expiram e não têm acesso público permanente. ffmpeg roda com limites e arquivos locais, sem protocolos de rede arbitrários.

B.4.16 Suspensão, limites e recuperação operacional
Situação	Comportamento mínimo
Tenant suspenso	Bloquear acesso operacional/envios e novas ações externas; manter trilha e procedimento de exportação/admin autorizado
Webhook chega durante suspensão	Persistir em retenção transitória limitada enquanto o canal permanecer vinculado; sem bot/distribuição/envio; marcar suspensão e alertar operação
Limite transitório esgotado	Alertar e aplicar política de desligamento/admissão documentada; nunca confirmar 200 sem persistência nem prometer armazenamento ilimitado
Reativação	Conciliar backlog; não executar automaticamente despedidas, menus ou ações vencidas
Storage atingiu limite do plano	Preservar registros existentes; bloquear novos uploads de saída e avisar; mídia de entrada usa reserva operacional limitada e política de degradação explícita
Limite de usuários/números	Bloquear novos cadastros/ativações excedentes; não apagar usuários/histórico
Credencial revogada/canal desconectado	Mostrar canal degradado, suspender despacho dependente e orientar reconexão
ERP indisponível	Atendimento continua; consultas mostram falha controlada e ações não são confirmadas
Banco indisponível	Não confirmar webhook nem envio local não persistido; frontend conserva rascunho e informa falha
Suspensão, opt-out, desativação de usuário, perda de permissão e desconexão também são revalidados no despacho de jobs, não só no momento em que foram criados.

Backup cobre banco, anexos, configuração necessária e recuperação das chaves. Definir RPO/RTO do piloto antes de medir. Testar restauração isolada, consistência de referências, reaplicação de exclusões e recuperação de mensagens incertas. Frequência inicial de teste completo: antes do piloto e mensalmente na operação, ajustada pelo responsável.

B.4.17 Observabilidade e critérios mensuráveis
Medir idade do job mais antigo, vazão/erro por fila e conector, atraso de webhook, tempo da API, reservas concorrentes, envios incertos, conexões WebSocket, pool do banco, uso de storage e duração de restauração. Logs de auditoria registram ator, ação, alvo, data, resultado e correlação, com conteúdo mínimo.

Não colocar segredo/CPF/texto de mensagem em métricas. Labels por tenant somente quando cardinalidade e acesso forem controlados; estatísticas comerciais/atendimento consultáveis pelo tenant ficam em tabelas autorizadas, não em um endpoint público Prometheus.

Metas de carga, latência e recuperação são decididas com o perfil do piloto e registradas antes dos testes. Relatório identifica dados sintéticos, ambiente e limitações; não publicar números estimados como benchmark executado.

B.5 Modelo de dados inicial
Esquema orientador, sujeito a migrações pequenas e ADR quando necessário. Entidades do tenant exigem RLS, relações compostas e índices coerentes com consultas reais; tabelas globais têm acesso restrito conforme B.4.4. Não criar tabelas comerciais de ERP.

Entidade	Campos / responsabilidade
tenants	Identidade, fuso, status, limites e perfil de retenção validado
users, memberships	Identidade global; vínculo tenant/usuário, papel, status e capacidade
sessions, auth_tokens, mfa_recovery_codes	Sessão, convite/reset e recuperação com hashes/expiração
teams, team_members, inbox_memberships	Setores e acesso a caixas; mesma regra usada no painel/API/eventos
inboxes	Identidade estável da caixa, número apresentado, setor padrão, horários e conexão ativa
channel_connectors	Tenant, tipo, origem estável, versão, estado e referência de credencial
meta_apps, meta_wabas, meta_phone_numbers	Registro app–WABA–número; segredo do app em área restrita; vínculo remoto ativo inequívoco
evolution_connections	Endpoint/instância autorizados, versão, estado, aceite e referência de segredo; sem persistência de QR
ingress_deliveries	Envelope bruto autenticado em área restrita, origem, hash, recebido em, estado e retenção
webhook_items	Itens segregados por tenant/inbox, identidade de origem, estado e referência de conciliação
contacts	ID interno, nome, telefone/e-mail opcionais, observações, bloqueio e documento opcional protegido
contact_channel_identities	Identidade externa por conexão, contato interno e evidência de associação
channel_contact_state	Última mensagem elegível/janela oficial por relacionamento no canal; não depende do fechamento da conversa
contact_external_refs	ERP, IDs externos de cliente/contrato e rótulo mínimo
customer_verifications, contact_consents	Referências de autorização/consentimento, escopo, origem, validade e revogação
conversations	Tenant/inbox/contato, estado, controlador, epoch, versão, setor, responsável, protocolo, referência ERP e adiamento
conversation_cycles	Abertura/fechamento de cada ciclo, motivo, resultado, síntese e encaminhamento
conversation_events, assignment_intervals, state_intervals	Histórico de mudanças e base reconstruível das métricas
conversation_read_markers	Usuário/conversa e posição de leitura; separado do status remoto
messages	Autor/origem, corpo, tipo, citação, identidade externa, timestamps, estado local e evidências de entrega
outbound_attempts	Outbox/tentativas, epoch, versão de conexão/lease, despacho, resposta e resultado incerto
idempotency_records	Principal, operação, chave, hash do pedido e referência do resultado; retenção consistente
message_status_events	Evidências e metadados remotos, deduplicação e conciliação
attachments	Objeto, tenant, mensagem, hash, MIME, tamanho, duração e retenção
labels, conversation_labels, canned_responses	Classificação e produtividade; motivo principal separado de etiquetas adicionais
wa_templates	WABA, ID externo, nome, idioma, categoria, estado e componentes; caixas acessam templates autorizados daquela WABA
protocol_counters	Tenant/período e contador transacional
inbox_send_leases, send_reservations	Coordenador, fencing e limites compartilhados
tenant_job_reservations, agent_capacity_reservations	Reservas atômicas distintas de trabalho e de vaga de atendimento
realtime_tenant_cursors, realtime_events	Sequência de commit por tenant, eventos e retenção
presence_connections	Sessão/usuário/tenant, heartbeat e estado; agregado por atendente
api_tokens, webhook_subscriptions, webhook_deliveries_out	Integração pública, escopos e entrega de eventos com IDs estáveis
bot_integrations, bot_sessions	Gancho por caixa, epoch concedida, prazo/progresso e handoff
dashboard_apps	Página externa existente, origem, permissões e configuração mínima
erp_connections, erp_actions	Configuração do ERP existente, capacidades, referências e resultados de ações; sem cadastro financeiro
action_approvals	Operação/parâmetros autorizados, aprovador, validade e consumo
meta_usage_daily	Agregado reconstruível, incluindo contagem com informação desconhecida
audit_logs, platform_audit_logs	Auditoria tenantizada e auditoria de plataforma separadas
retention_tasks, deletion_records	Execução de retenção/exclusão e reaplicação controlada após restore
Não é obrigatório criar todas as tabelas em M0. Cada marco introduz apenas as entidades necessárias, mantendo as invariantes. Jobs River usam seu próprio esquema gerenciado pela biblioteca.

B.6 Evolução por necessidade comprovada
Componente / mudança	Gatilho
Valkey	Medição de presença/eventos ou simplificação operacional demonstrada; ADR aprovada antes de adicionar ao núcleo
PgBouncer	Orçamento de conexões próximo do limite, preservando conexões adequadas para LISTEN/jobs
Réplica de leitura	Relatórios interferem na latência do atendimento após otimização
ClickHouse	Volume histórico medido justifica outro armazenamento analítico
River Pro	Custo comprovado de manter coordenação necessária supera a alternativa licenciada
Adaptador ERP adicional	Provedor real exige outro ERP e existe documentação e escopo aprovado
Serviço separado	Escala/isolamento medidos exigem separação de processo; não transformar módulos em microsserviços por antecipação
PWA/push e modo escuro	Evolução de experiência na V2
Kubernetes, Kafka, RabbitMQ, NATS, Next.js, ORM	Sem necessidade prevista na V1
ADR deve explicar problema, alternativas, custo, operação e reversibilidade. A regra inicial é não adicionar componentes; a exceção explícita desta seção requer aprovação de arquitetura e não é uma proibição contraditória escondida no prompt.

B.7 Estrutura do repositório
Caminho	Conteúdo
docs/especificacao.md	Esta revisão, fonte normativa
docs/plano.md, docs/perguntas.md	Marcos e decisões dependentes de informação externa
docs/adr/	Decisões de arquitetura e exceções aprovadas
docs/integracoes/	Contratos do ERP existente, Meta e Evolution; versões e evidências
docs/legal/	Condições aplicáveis, aceite Web e perfil de retenção por implantação
docs/chatwoot-referencia.md	Comportamentos estudados, versão/commit e diferenças intencionais
backend/cmd/chatisp/	Modos api, worker, migrate, all
backend/internal/platform/	Configuração, banco, WithTenant, identidade, HTTP, logs, métricas e crypto
backend/internal/ingress/, channel/whatsapp/	Entrada restrita, drivers, outbox e coordenação
backend/internal/conversation/, contact/, team/, assignment/	Domínio do atendimento
backend/internal/automation/, bot/, realtime/	Regras fixas, handoff e eventos
backend/internal/integration/erp/	Contrato fino, fake e adaptador do ERP existente
backend/internal/report/, storage/, audit/, usage/, api/	Serviços de apoio e rotas
backend/db/migrations/, backend/db/queries/, backend/sqlc.yaml	SQL, migrações e geração
backend/testdata/	Payloads reais anonimizados e cenários controlados
web/	Painel React/Vite
deploy/	Compose, Caddyfile e .env.example sem segredos
load/, .github/workflows/	Carga e CI
Referência Chatwoot é checkout opcional separado, fixado por commit e fora do build. O código próprio não depende de clonar ou executar o Chatwoot.

B.8 Testes de aceite que não podem faltar
Cenário	Evidência esperada
Tenant A tenta vincular mensagem a conversa B	FK/regras rejeitam; nenhum vazamento em busca, erro ou evento
Envelope Meta contém itens de A e B	Expansão correta sem acesso do tenant ao envelope completo
Consulta/operação sem contexto	Acesso de domínio rejeita antes de executar; banco permanece fail-closed
Dois envios com mesma chave em paralelo	Uma mensagem/outbox; mesmo resultado; payload diferente gera conflito
Processo cai após chamada remota e antes de gravar resposta	uncertain; nenhuma repetição automática sem evidência
Bot enfileira; humano assume; worker executa	Pendente cancelada; bot com epoch antiga recebe conflito
Tomada humana após despacho remoto	Estado em voo reconhecido; não prometer cancelamento externo
Cliente responde enquanto job de inatividade inicia	Resposta preservada, job obsoleto não encerra
Duas primeiras mensagens concorrentes	Uma conversa ativa e protocolo consistente
Dois workers reservam última vaga do atendente	Uma atribuição aceita, outra permanece na fila
Adiamento vence ou recebe resposta antecipada	Retorna conforme capacidade, sem encerramento automático indevido
Bot confirma webhook e depois trava	Prazo vence e conversa chega ao humano
Janela oficial vence durante a fila	Revalidar e impedir envio livre não permitido
Sincronização histórica/eco da Evolution	Não duplica mensagens nem cria automações atuais
Snapshot inicial e evento concorrente	Nenhuma mudança é pulada; deduplicação trata repetição
Evento invisível entre dois visíveis	Cursor avança corretamente sem vazar payload ou entrar em loop
Transferência de setor e sessão aberta	API/eventos futuros negados e cache visual invalidado
ERP/IA/Evolution ficam indisponíveis	Chat oficial e ações independentes continuam disponíveis
ERP conclui ação e a resposta se perde	Conciliar ou marcar incerto, sem executar duas vezes cegamente
Relatório 8h–12h com 3h de espera	4h total e 1h operacional; sobreposição não duplica desconto
Bot resolve sem humano	Primeira resposta humana não aplicável e resultado explicitamente classificado
Restore de banco/anexos/chaves	Conteúdo acessível, referências consistentes e exclusões reaplicadas
Esses testes validam a implementação local. Contratos simulados não comprovam funcionamento de Meta, Evolution ou ERP reais; testes reais autorizados são registrados separadamente.

PARTE C — Plano e prompt de implementação
C.1 Ordem de entrega
Os marcos são incrementais. A interface começa em M1 e um fluxo completo de texto funciona em M2; não esperar todos os módulos de backend para descobrir se o atendimento é utilizável. Recursos complementares continuam sendo completados até a V1.

Marco	Entrega	Critério de conclusão
M0 — Preparação	Ler especificação; registrar escopo, contratos externos conhecidos, fluxo de telas e plano; fixar versões	docs/plano.md relaciona requisitos a marcos/testes; lacunas de ERP/Meta/Evolution explícitas, sem inventar APIs
M1 — Fundação e entrada no painel	Monorepo, Compose, PostgreSQL, storage dev, Makefile, CI, goose/sqlc/Huma, RLS, identidade, sessões, MFA, CSRF, tenants e navegação básica	Dois tenants isolados em banco real; login/convite/reset/revogação; papéis testados; painel navegável e sem dados mock apresentados como reais
M2 — Atendimento vertical oficial	Caixa Meta, texto recebido, conversa/protocolo, assumir, responder, encerrar, outbox/idempotência, cursor e histórico na interface	Fluxo inteiro no painel com servidor Meta falso; teste real quando credenciais/autorização existirem; duplicação, corrida, incerteza e isolamento cobertos
M3 — Webchat do atendente	Filas/abas/filtros/busca, contato, transferências, notas, etiquetas, prioridade, leitura por usuário, rascunho, atalhos e colisão	Playwright cobre receber, assumir, transferir, responder e encerrar; teclado, falhas e reconexão verificadas
M4 — Canal e produtividade completos	Mídias V1, gravação de áudio, templates/WABA, janela oficial, consentimento, respostas prontas, saúde do canal e uso reportado	Uploads/URLs seguros, conversão limitada, janela revalidada ao enviar e estados remotos corretos
M5 — Distribuição, automações e bot	Round-robin, vagas atômicas, presença, horários, plantão, triagem, snooze/inatividade e gancho de IA com handoff	Cliente não fica preso no menu/bot; tomada humana invalida envios pendentes; jobs antigos não fecham conversa atual
M6 — Integração existente e modalidade Web	API pública/webhooks completos, contexto externo/iframe, adaptador fino do ERP utilizado; Evolution condicionada	ERP não bloqueia atendimento; operações não repetem efeitos cegamente; contratos por driver e teste de queda isolada; Web segue desligado sem seus requisitos
M7 — Gestão do atendimento	Painel, relatórios V1/CSV, histórico estruturado, retenção, exportação/exclusão, limites e superadmin	Métricas com cenários calculados; acesso correto por papel; sem transformar exportação do chat em histórico completo do ERP
M8 — Validação do piloto	Carga, falhas, restore, alertas, revisão de segurança e guia de instalação	Evidências reais e limitações registradas; fluxo de atendimento validado por operador; critérios abaixo cumpridos
O contrato do ERP existente é investigado em M0 e pode receber uma integração mínima assim que disponível. Isso não autoriza atrasar o fluxo M2 para construir uma integração financeira extensa. O piloto básico do chat pode operar sem essa conexão; uma oferta que anuncie funções integradas exige sua validação real.

C.2 Critérios de piloto e de abertura comercial
Piloto controlado do webchat
Um provedor piloto e perfis de atendente/supervisor/admin definidos; número e tráfego usados com autorização.
Fluxo completo de mensagens/histórico sem depender do ERP, com identificação de falha/incerteza.
Testes de isolamento, autorização, idempotência, troca de posse, reconexão e recuperação aprovados.
Metas e perfil de carga definidos antes da medição: tenants, atendentes simultâneos, números, mensagens, mídia e retenção esperados. Valores não são inventados nesta especificação.
RPO/RTO acordados para o piloto; restore executado, incluindo chaves e anexos; alertas com destino operacional definido.
Operador percorre os fluxos principais no navegador e registra problemas de uso; testes automatizados não substituem essa observação.
Abertura comercial
Todos os recursos V1 aplicáveis à modalidade oferecida têm evidência de aceite; pendências externas são descritas honestamente na oferta.
Cloud API configurada e validada em ambiente real. Configuração manual assistida é permitida na V1; Embedded Signup permanece V2.
Definir propriedade do app/WABA/número, responsabilidade pelas credenciais, permissões e reconexão; “token preenchido” não prova onboarding concluído.
ERP existente testado quando a oferta incluir suas capacidades; nenhum adaptador adicional é pré-requisito genérico do webchat.
Para Evolution: versão fixa, licença/condições verificadas, evidências reais, isolamento de falha, suporte de reconexão e aceite por provedor/inbox. Sem isso, modalidade Web indisponível e identificada como pendente.
Política de retenção e procedimento de atendimento/exportação compatíveis com o provedor, sem alegação genérica de conformidade garantida pelo software.
Não usar teste mock, contador de funcionalidades ou estimativa de desempenho como prova de prontidão comercial.
C.3 Prompt para a IA de implementação
Copiar o bloco abaixo para o agente de desenvolvimento, com este arquivo disponível em docs/especificacao.md. Esse prompt orienta desenvolvimento futuro; a elaboração desta especificação não executou os marcos.

# Missão
 
Implemente o Chat ISP: webchat de atendimento no navegador para provedores de internet, com WhatsApp oficial e modalidade Web opcional. O ERP JÁ EXISTE. Nosso trabalho é entregar mensagens, conversas, filas, equipes, histórico, produtividade e relatórios de atendimento.
 
# Fonte e escopo
 
Leia `docs/especificacao.md` inteira antes de escrever código. Ela é a referência de produto e engenharia. A.6 define estados/transições; B.4 define os contratos críticos; C.1 define os marcos. Não mantenha uma segunda interpretação contraditória em outro arquivo.
 
Implemente somente V1/V1 simples. V2 só recebe os pontos de extensão expressamente pedidos. Não criar ERP, cadastro financeiro, gestão de planos/contratos/OS, geração de boleto/Pix, motor de rede, IA nativa, RAG, campanhas, construtor visual de automações, widget público ou app nativo.
 
ERP é integração fina com o sistema efetivamente utilizado. Não pressupor SGP e IXC simultâneos. Sem documentação, implementar contrato/fake e registrar a pendência, continuando o webchat. Não inventar endpoints ou declarar um fake como integração entregue.
 
# Referências
 
Código Community do Chatwoot pode ser estudado em checkout opcional separado, fixado por commit, apenas para comportamento e casos de teste. Nunca compilar/importar/empacotar essa referência no produto. Não copiar nem adaptar componentes sob licença comercial; não usar diretórios enterprise como base de implementação. Registrar origem/commit/arquivo e diferenças em `docs/chatwoot-referencia.md`.
 
Documentação real de API, condições de licença e normas aplicáveis prevalecem sobre suposições. Registrar divergência em ADR e tratar a parte afetada. Preservar a arquitetura do chat enquanto integrações ainda dependem de informação.
 
# Stack
 
Backend: Go mínimo 1.25, versão suportada fixada; chi, Huma v2, coder/websocket, PostgreSQL 18, pgx/v5, sqlc, goose, River open-source, Ristretto, slog, Prometheus, Argon2id, TOTP, sony/gobreaker, aws-sdk-go-v2 e ffmpeg.
 
Frontend: React 19, TypeScript strict, Vite, React Router, TanStack Query/Virtual, Zustand, React Hook Form/Zod, Tailwind/shadcn, Geist, openapi-typescript/openapi-fetch.
 
Infra: Compose, Caddy, storage S3 e ferramentas de backup previstas. Evolution é externa e opcional. Testes/qualidade conforme B.3. Não adicionar componentes/libraries relevantes fora da stack sem ADR aprovada. Exceções são as de B.6; nenhuma é autorização automática para aumentar a infraestrutura.
 
# Contratos inegociáveis
 
1. Webhook autenticado é persistido antes do ACK; envelopes multitenant ficam na área de ingresso restrita e são divididos em itens corretamente roteados.
2. RLS forçada, papel de runtime sem BYPASSRLS/ownership, contexto transacional e FKs compostas de tenant. Autorização por setor/caixa também em histórico, anexos, busca, exportação e eventos.
3. Comando de envio/ação idempotente desde a API; mensagem, outbox, evento e job consistentes. Retentativa de HTTP do cliente não cria outra mensagem.
4. Resultado remoto incerto não é reenviado automaticamente; recuperar processo morto durante envio como incerto quando não houver prova de não despacho.
5. Lease/fencing e reservas compartilhadas controlam envio e vagas; não usar memória ou COUNT como garantia de exclusão distribuída.
6. Posse com controller_epoch; assumir/desligar bot cancela pendências antigas e bloqueia respostas de epochs obsoletas. A fronteira de despacho define o limite de cancelamento remoto.
7. Estados e resultado separados. Inatividade encerra com no_
response, sem contabilizar como solução confirmada. Jobs verificam condições atuais.
8. Uma conversa ativa por tenant/inbox/contato. Reabertura cria ciclo e conserva protocolo; nova demanda gera novo protocolo.
9. Janela oficial pertence ao relacionamento no canal, não ao encerramento local. Templates pertencem à WABA. Identidades externas não se reduzem a telefone.
10. WebSocket recupera eventos com cursor compatível com commit e carga inicial consistente; gaps por permissão não quebram avanço de cursor.
11. ERP indisponível não bloqueia o chat. Idempotência/uncertain também se aplicam a efeito externo do adaptador. Nenhuma ação de ERP fora do escopo entra pela integração de IA.
12. Dados de identificação não são autenticação. Reutilizar verificação do ERP/provedor; evitar coleta de identificadores completos pelo WhatsApp. Credenciais não saem do backend.
13. URLs externas, mídia e iframe têm limites e autorização. Proteção de saída inclui SSRF; proteção de arquivo inclui conteúdo ativo e recursos de conversão.
14. Consentimento/opt-out, suspensão e perda de permissão são reavaliados antes de despachar pendências. Canal degradado é visível.
15. Retenção e restore incluem banco, anexos e chaves. Métricas seguem a seção 09, inclusive null para resposta humana inexistente e união de intervalos descontados.
16. Cloud e Web têm capacidades próprias. Sem teste real/licença/condições aplicáveis, Web segue desabilitado e nunca é anunciado como funcional/homologado.
 
# Forma de trabalhar
 
- Código e identificadores em inglês; UI, mensagens ao cliente e documentos em pt-BR.
- OpenAPI gerado do backend; cliente TypeScript gerado. Erros RFC 9457; sem stack trace/segredo para o usuário.
- `.env` fora do git; `.env.example` atualizado; segredos reais não entram em fixtures/logs.
- Começar com `docs/plano.md`, relacionando requisitos, marcos, riscos e testes. Registrar pendências em `docs/perguntas.md`, com impacto e alternativa reversível.
- Executar M0–M8 em ordem, com interface desde M1 e fluxo completo desde M2. Não encerrar o trabalho só porque o esqueleto compila.
- Em cada marco: código, testes pertinentes, documentação, `make test` e `make lint`. Executar testes com banco real para RLS/concorrência e Playwright para os fluxos entregues.
- Não fazer chamadas reais a ERP/WhatsApp com efeitos externos sem ambiente/credenciais e autorização correspondentes. A falta deles não impede implementar/testar módulos independentes com doubles claramente identificados.
- Pedir decisão apenas para bloqueio real de escopo, informação externa indispensável, mudança relevante de arquitetura ou ação irreversível. Não pedir confirmação para escolhas reversíveis já autorizadas pela especificação.
- Não executar migração destrutiva nem substituir dados reais para facilitar teste. Mudança de contrato público/versionamento exige análise explícita.
- Não declarar V1 pronta, conectores reais funcionando ou capacidade de carga sem evidência executada.
 
# Relatório de cada marco
 
Informar: comportamento entregue; arquivos principais; comandos de execução/teste; testes rodados e resultados; ADRs; limitações e dependências externas ainda pendentes. Diferenciar simulação de validação real. Manter o plano com o estado verdadeiro de cada marco.
 
Comece pela leitura integral e pelo M0. A prioridade constante é o webchat; o ERP existente é uma integração.
 
PARTE D — Fontes, verificação e decisões externas
D.1 Fontes primárias
Referências consultadas na revisão em 28/10/2026, incluindo a análise que a precedeu. Conteúdo externo muda; a implementação deve fixar a versão efetivamente utilizada. Links para documentação de APIs não são autorização para acessar sistemas reais do provedor.

ID	Fonte	Uso nesta especificação
S01	Política de Mensagens do WhatsApp Business	Regras do canal, janela, encaminhamento humano, consentimento e proteção de identificadores
S02	Página pública de preços WhatsApp e documentação técnica de preços	Separar observação pública de mudança tarifária não confirmada; página técnica teve acesso limitado
S03	PostgreSQL 18 — Row Security Policies	RLS, papéis e diferença entre política de linha e integridade referencial
S04	Anatel — Atendimento	Orientação geral de protocolo e histórico; validar aplicação ao provedor e manual operacional vigente
S05	Evolution — releases e LICENSE	Versão, ativação e condições publicadas; fixar release antes de habilitar
S06	Baileys — migração v7	Identificadores LID/PN e contratos de identidade do modo Web
S07	Meta — templates por WABA	Propriedade e consulta de templates no contexto da WABA
S08	Evolution — configurações e eventos	Sincronização histórica e eventos distintos; confirmar contrato na versão escolhida
S09	OWASP — SSRF Prevention	Restrições de destino, rede e redirecionamentos
S10	River — transactional enqueueing	Agendar job na mesma transação da mudança local
S11	OWASP — Choosing and Using Security Questions	Dados pessoais conhecidos não substituem autenticação adequada
As condições da Evolution foram encontradas em fontes do próprio projeto; isso não certifica conformidade de qualquer implantação. A matriz Chatwoot foi preservada como referência funcional aproximada e não foi revalidada linha a linha contra uma versão do código. Não usar alegações sobre abandono/licença de outras tecnologias como justificativa sem fonte primária específica.

D.2 Decisões pendentes para implantação, sem bloquear esta especificação
Decisão	Necessária antes de	Caminho enquanto pendente
ERP existente, versão, API e autenticação	Adaptador real	Contrato mínimo e fake; continuar webchat
Mecanismo existente de autorização do cliente	Mostrar dados sensíveis/ações ERP ao cliente	Não expor essa capacidade; manter atendimento geral
Propriedade/configuração Meta, permissões e número	Teste real e abertura comercial oficial	Servidor falso e configuração documentada
Evolution exata, licença e condições aplicáveis	Habilitação Web	Feature flag desligada; Cloud independente
Regras vigentes de cobrança	Estimativas monetárias/oferta de preço	Registrar uso observado/desconhecido; não prometer franquia/tarifa
Enquadramento do provedor e retenção	Produção com dados reais e expurgo automático	Definir perfil antes de ativar expurgo; minimizar coleta
Perfil de carga, metas e RPO/RTO	Aceite do piloto	Testes funcionais; nenhum número de capacidade anunciado
Política de suspensão e limite transitório de entrada	Operação comercial	Configuração explícita e teste de degradação
D.3 Critério para futuras mudanças
Uma solicitação nova deve responder: melhora o trabalho de atender no webchat ou recria algo que já pertence ao ERP? No primeiro caso, avaliar valor e esforço. No segundo, procurar integração com a capacidade existente antes de ampliar o produto.

A implementação está pronta quando os fluxos e invariantes têm evidência, não quando todas as pastas existem. Funcionalidades condicionadas permanecem identificadas como pendentes até serem validadas.
