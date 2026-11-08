# ChatISP — inventário funcional completo para o design

**Versão:** 1.0 · **Data de referência:** 07/11/2026, America/Fortaleza.
**Código examinado:** `2730364a4aa547245c111c3c6f5ba310d5058157`, branch `redesign/atendimento-20261107`.
**Fonte:** `/home/enok/Documentos/projetos/chatisp`.
**Objetivo deste arquivo:** permitir desenhar a experiência inteira sem esquecer recursos existentes e sem prometer funções que ainda não existem.

Este inventário foi confrontado com rotas, páginas, componentes, modelos de apresentação, regras de acesso e contratos do repositório. Não é uma nova homologação de produção. Documentos antigos que anunciam fases concluídas não substituem a situação aqui discriminada.

Acompanha o inventário a matriz original dos **257 requisitos**, sua exportação CSV e uma lista de telas/estados para conferência do design. A matriz contém também requisitos de infraestrutura: eles não devem virar botões ou módulos visuais fictícios.

## 1. O produto, em uma descrição completa

O ChatISP é uma central web de atendimento de provedores de internet, utilizada por vários atendentes, supervisores e administradores. Reúne mensagens, filas, responsáveis, setores, protocolos, histórico, informações do contato, notas privadas e indicadores de atendimento.

O cliente conversa pelo WhatsApp; a equipe atende pelo navegador. **“Webchat” aqui significa o painel da equipe, não um widget de chat instalado no site do provedor.** O foco da V1 é WhatsApp. A existência de um painel web não implica receber e-mail, Instagram, Telegram ou chat de visitantes.

Há isolamento entre provedores. Cada provedor administra suas caixas/números, setores, membros, políticas e integrações. A interface mostra somente o recorte autorizado para a sessão.

O sistema não é um ERP como SGP ou IXC. Não administra por conta própria contratos comerciais, mensalidades, contabilidade, estoque, rede ou ordens de serviço técnicas. Pode apresentar contexto e solicitar operações de um ERP existente quando o adaptador estiver implementado, autorizado e disponível. Atualmente esse adaptador real continua incompleto.

O atendimento precisa funcionar em computador e celular. No computador, a operação principal combina **fila, conversa e contexto**. No celular, essas áreas alternam sem perder a conversa e o rascunho. Não há aplicativo Android/iOS nativo próprio entregue nesta versão.

## 2. Como interpretar a disponibilidade

| Marcação | Significado para o design |
|---|---|
| **Implementado** | Existe implementação no código atual. Ainda pode depender de configuração, permissão e ambiente. Não significa que todas as integrações estejam homologadas. |
| **Condicionado** | Existe fluxo, componente ou contrato, mas sua operação depende de canal, armazenamento, autorização, configuração ou integração externa. Desenhar também indisponibilidade. |
| **Parcial** | Há somente parte da capacidade: não apresentar o fluxo inteiro como concluído. |
| **Futuro / fora do escopo** | Não pertence à entrega atual ou não tem implementação identificada. Não integrar silenciosamente ao produto existente. |

**Instalação demonstrativa atual:** há dados fictícios para comparação visual; os canais externos permanecem desligados. Uma mensagem histórica de exemplo não comprova entrega real ao WhatsApp.

## 3. Conceitos que não podem ser confundidos

| Conceito | Definição |
|---|---|
| Provedor / conta | Organização isolada que usa a central. Não é um assinante final. |
| Caixa | Entrada associada a um número/conector WhatsApp. Um provedor pode ter mais de uma, conforme limites e configuração. |
| Setor / equipe | Área de atendimento, por exemplo Suporte, Financeiro ou Comercial; reúne vínculos de atendentes e caixas. |
| Contato | Pessoa atendida. Não é automaticamente um cliente identificado ou autorizado no ERP. |
| Conversa | Registro persistente das interações e do atendimento. |
| Protocolo local | Identificador do atendimento no ChatISP. Não deve ser substituído pelo protocolo do ERP. |
| Ciclo | Uma abertura/reabertura dentro da conversa. O mesmo protocolo pode ter novos ciclos. |
| Responsável | Atendente atribuído. Não equivale ao autor de todas as mensagens anteriores. |
| Controle / posse | Quem pode conduzir/envie mensagens naquele momento: menu, bot ou humano, com validação no servidor. |
| Estado | Etapa operacional da conversa. Não é a prioridade nem o resultado do encerramento. |
| Resultado | Como terminou um ciclo: resolvido, sem resposta, encaminhado ou spam. |
| Adiamento | Suspensão temporária do atendimento com retorno; não é um quinto estado nem um encerramento. |
| Não lida | Marcador de leitura do usuário. Não é um estado da conversa. |
| Prioridade | **Normal ou Urgente** no ChatISP atual. Não copiar os quatro níveis do Chatwoot como se existissem aqui. |
| Etiqueta | Classificação adicional; uma conversa pode ter várias. |
| Nota privada | Registro interno, distinto da mensagem pública enviada ao cliente. |
| Enfileirado / aceito / entregue / lido | Etapas diferentes. Não representar aceitação local como entrega ao cliente. |

## 4. Papéis e recortes de acesso

### 4.1 Atendente

Opera conversas, contatos e recursos autorizados pelos vínculos de setor e caixa. Pode consultar e usar respostas prontas, manter notas e etiquetas no contexto permitido e controlar sua própria disponibilidade. Ter uma conversa atribuída não deve, sozinho, liberar acesso a um setor não autorizado.

### 4.2 Supervisor

Acompanha o atendimento e tem alcance ampliado no provedor, conforme as regras do servidor. Pode consultar distribuição, ocupação, horários e automação, mas várias configurações continuam sendo de gravação exclusiva do administrador. Pode administrar respostas compartilhadas e documento do contato nos casos autorizados.

### 4.3 Administrador do provedor

Administra pessoas, vínculos, setores, canais, integrações, identidade, auditoria, capacidade e configurações do próprio provedor. Continua sujeito ao isolamento do provedor e às validações operacionais. Não é autorização para ignorar posse, versão da conversa ou segurança dos envios.

### 4.4 Administração da plataforma

Existe área de administração dos provedores. Ela não deve ser misturada ao posto de atendimento. O perfil de plataforma não concede automaticamente acesso ao conteúdo das conversas. Uma pessoa que acumule funções precisa estar no contexto operacional autorizado do provedor.

### 4.5 Matriz resumida de comportamento

| Ação | Atendente | Supervisor | Administrador |
|---|---|---|---|
| Ver e operar conversas | Recorte autorizado | Recorte autorizado, alcance ampliado | Recorte do provedor conforme servidor |
| Assumir, transferir, encerrar e reabrir | Conforme autorização, posse e capacidade | Conforme autorização operacional | Conforme autorização operacional |
| Notas, etiquetas e leitura | Conversas autorizadas | Conversas autorizadas | Conversas autorizadas |
| Alterar prioridade | Não há botão de alteração para esse papel | Sim | Sim |
| Usar respostas prontas | Sim | Sim | Sim |
| Criar/editar/excluir respostas e assinatura | Não | Sim | Sim |
| Consultar CPF/CNPJ mascarado e busca exata | Contatos autorizados | Contatos autorizados | Contatos autorizados |
| Gravar, remover e revelar CPF/CNPJ | Não | Sim, com autorização/auditoria | Sim, com autorização/auditoria |
| Editar setores e vínculos | Não | Não | Sim |
| Consultar distribuição, automação e horários | Não é o painel de gestão desse papel | Sim | Sim |
| Alterar automação, horário e distribuição automática | Não | Somente o que a API permitir; os editores examinados restringem as gravações principais ao admin | Sim |
| Configurar canais, tokens e webhooks | Não | Não | Sim |
| Usuários, sessões, identidade e auditoria administrativa | Não | Não como administrador do provedor | Sim |
| Relatórios de atendimento | Recorte autorizado pela API; não presumir acesso global | Recorte autorizado | Recorte autorizado |
| Uso reportado pela Meta | Não exibido nessa página | Exibido, conforme API | Exibido, conforme API |

A matriz visual é um resumo. A resposta do servidor é a autoridade; esconder um botão não substitui autorização.

## 5. Mapa completo das páginas atuais

| Rota | Tela | Conteúdo principal |
|---|---|---|
| `/entrar` | Acesso | E-mail, senha e resultado do login |
| `/mfa` | Autenticação em duas etapas | Cadastro/verificação, QR local e recuperação, conforme etapa |
| `/convite` | Aceitar convite | Validação do convite e criação das credenciais |
| `/recuperar-senha` | Solicitar recuperação | Solicitação pelo e-mail |
| `/recuperar-senha/confirmar` | Concluir recuperação | Token e nova senha |
| `/painel` | Visão do atendimento | Oito indicadores operacionais reais |
| `/conversas` | Central de atendimento | Caixa, fila, conversa, compositor e contexto |
| `/contatos` | Diretório | Busca, cadastro, edição, bloqueio e documento |
| `/equipes` | Equipes/setores | Setores, membros e caixas vinculadas |
| `/relatorios` | Relatórios | Período, tempos, dimensões, mapa de calor, CSV e uso Meta |
| `/configuracoes` | Administração do provedor | Atalhos e editores de operação, segurança e integrações |
| `/configuracoes/canais` | Caixas Meta | Configuração manual, estado, saúde e limites dos números |
| `/configuracoes/usuarios` | Usuários | Pessoas, convites, papéis, vínculos e sessões conforme componente |
| `/configuracoes/provedores` | Plataforma | Administração dos provedores, restrita ao papel correspondente |
| Demais caminhos | Página não encontrada | Explicação e retorno ao painel |

A entrada `/` direciona ao painel. O design pode reorganizar a navegação, mas não deve apagar os destinos administrativos existentes.

## 6. Jornada ponta a ponta

1. A pessoa aceita um convite ou entra com sua conta. Cumpre MFA quando a sessão exigir; o cadastro é opcional na política atual, não obrigatório para todo login.
2. O sistema resolve o provedor, papel e permissões. A equipe vê a identidade do provedor e sua própria presença.
3. O atendente acessa Conversas e escolhe a caixa autorizada. Se só houver uma caixa, ela é adotada como seleção inicial.
4. A fila mostra Minhas, Não atribuídas e Todas, com contagens e filtros do recorte autorizado.
5. Uma nova mensagem pode criar ou reabrir atendimento, conforme as regras. No canal habilitado, horário, triagem e distribuição podem intervir antes da tomada humana.
6. O atendente seleciona uma conversa, consulta protocolo, histórico, estado, responsável, etiquetas e contexto permitido.
7. Quando precisa conduzir a conversa, assume ou recebe a atribuição. O servidor verifica capacidade, acesso e versão; a interface não toma posse por apenas abrir o histórico.
8. Responde com texto, resposta pronta ou mídia. Pode citar mensagem, gravar áudio, deixar nota privada, marcar leitura ou consultar contexto externo disponível.
9. Se necessário, transfere para outro atendente/setor, devolve à fila ou adia. Mudanças podem retirar o acesso atual à conversa.
10. Quando aguarda o cliente, o estado e os relógios de inatividade seguem as confirmações do canal, não uma contagem inventada pelo frontend.
11. Encerra com motivo, resultado e síntese; a despedida é opcional. O protocolo e o histórico permanecem.
12. Uma mensagem posterior pode iniciar novo ciclo no mesmo protocolo dentro da janela de reabertura configurada, ou um novo atendimento fora dela.
13. Supervisor/administrador acompanham fila, presença, tempos, resultados, relatórios, auditoria e alertas.

Na demonstração atual, passos que exigem canal/ERP real não estão homologados. O desenho precisa representar esses bloqueios, em vez de pressupor sucesso.

## 7. Estrutura global da interface

**Implementado:** marca do provedor, áreas principais, página corrente, saída da sessão, presença do operador, acesso por teclado e layout responsivo.

Devem existir estados para menu móvel aberto/fechado, sessão ausente/expirada, acesso negado, carregamento da identidade, logo indisponível e provedor sem nome definido. O nome local de exibição da aba é uma preferência local; não é nome centralizado oficial do atendente nem troca de conta.

O usuário precisa distinguir onde está: provedor, caixa, setor e conversa. Informações técnicas não precisam dominar a operação, mas os controles reais não podem ser retirados sem uma alternativa acessível.

## 8. Painel operacional

Oito indicadores atuais, fornecidos pela API:

1. Na fila.
2. Em atendimento.
3. Aguardando o cliente.
4. Adiadas.
5. Sem responsável.
6. Atendentes online.
7. Maior espera.
8. Alertas de espera.

As contagens podem se sobrepor. “Sem responsável” não deve ser somado automaticamente à fila como se fosse um grupo independente. Projetar também zero válido, ausência de amostra, carregamento, erro e acesso negado. Não acrescentar faturamento, receita, velocidade de internet, uptime de assinantes ou satisfação como indicadores existentes do webchat.

## 9. Caixa, fila e pesquisa

### 9.1 Seleção e abas

- Caixa de atendimento com nome e estado do canal.
- Minhas: conversas atribuídas ao usuário, dentro do recorte permitido.
- Não atribuídas: conversas sem responsável.
- Todas: todas as conversas autorizadas no recorte; não significa todos os provedores.
- Contagens fornecidas pela API. Total de resultados não é necessariamente o número da página atual.
- Paginação: primeira/próxima página conforme cursor.

### 9.2 Informações do cartão

Nome do contato; avatar por iniciais; protocolo; estado; setor; responsável; horário da última mensagem; prioridade quando urgente; contador/marcador de não lidas; alerta de espera; seleção atual.

**Limite atual:** não há campo com o texto da última mensagem no DTO da fila. Não desenhar uma prévia de texto como se já estivesse disponível. Avatares por iniciais não equivalem a sincronização de foto do WhatsApp.

### 9.3 Busca, filtros e ordem

- Busca por nome, telefone, protocolo ou conteúdo, conforme a consulta implementada.
- Filtros: estado, setor, atendente, caixa, etiqueta e datas inicial/final.
- Limpar busca separadamente de limpar filtros.
- Ordenar por espera ou última mensagem.
- Navegação por teclado nas abas e listas.

Preparar fila vazia, nenhum resultado da pesquisa, diretório de atendentes indisponível, caixa sem acesso, busca carregando, filtros ativos e erro de consulta. Não apresentar uma pessoa/setor não autorizado para completar o layout.

## 10. Conversa e contexto do atendimento

### 10.1 Cabeçalho

Contato, iniciais, protocolo local, estado, responsável, ação Assumir e Encerrar com motivo. No celular, Voltar à fila e Detalhes. Mostrar feedback quando uma ação está em andamento ou foi recusada.

### 10.2 Histórico

- Mensagens recebidas e enviadas em posições distinguíveis.
- Autor, data/hora, conteúdo e estados aplicáveis.
- Eventos operacionais no meio do histórico: tomada, encerramento, reabertura, espera, transferência e outros tipos conhecidos.
- Responder/citar mensagem e localizar a mensagem citada quando disponível.
- Reações recebidas, quando presentes nos dados. **Não há neste painel um comando geral de adicionar reação equivalente ao Chatwoot.** Emojis no texto são outra função.
- Mensagem cujo tipo não é interpretado: exibir indicação segura e referência, não uma visualização inventada.
- Carregamento, falha, vazio e retorno às mensagens recentes.
- Histórico virtualizado; o desenho não pode exigir carregar toda a conversa para operar.

### 10.3 Estados da conversa

`triage` → Em triagem; `open` → Aberta; `waiting_customer` → Aguardando cliente; `closed` → Encerrada.

Essa sequência não autoriza transições arbitrárias. O backend define quando cada mudança é possível. Adiada, urgente, não lida, atribuída e resolvida são outras dimensões, não novos valores dessa lista.

### 10.4 Contexto lateral / detalhes

Hoje agrupado em: responsável/setor/notas; contato/canal/anexos; cadastro/integrações. O design pode redistribuir esses grupos, mantendo os recursos descritos nos módulos seguintes. No celular, esse contexto deve ser acessível sem esmagar a área de mensagens.

## 11. Compositor e produtividade

**Texto:** escrever, editar rascunho, enviar por botão ou Ctrl/Cmd+Enter, conservar o rascunho no dispositivo, lidar com falha e resposta incerta. Rascunho e intenções não devem atravessar indevidamente a troca de usuário/provedor.

**Formatação atual:** negrito, itálico e atalhos de alguns emojis. Não existe um editor de documento rico completo equivalente a todos os recursos do Chatwoot.

**Respostas prontas:** digitar `/`, pesquisar/escolher um atalho e inserir o texto renderizado no rascunho; não enviar automaticamente só por selecionar uma resposta.

**Citação:** selecionar a mensagem respondida, mostrar sua prévia e permitir cancelar a resposta. A referência técnica ainda aparece em parte da UI de áudio; não inventar seleção automática por uma integração inexistente.

**Arquivo:** selecionar, arrastar ou colar; visualizar que há um anexo pronto; remover; enviar com legenda quando o tipo permitir.

**Áudio:** solicitar microfone, iniciar/parar gravação, enviar ou descartar a captura; tratar permissão negada, formato, conversão e falha de envio.

**Estados de envio a representar:** em fila, enviando, aceito, falhou, tentando novamente, incerto e cancelado; entrega e leitura remotas quando confirmadas. “Enviar como nova mensagem” é uma decisão explícita distinta da repetição da mesma tentativa incerta.

Após encerrar, o compositor deixa de aceitar novos envios até uma reabertura válida. Contato bloqueado, perda de posse, janela do canal, revogação de consentimento, limite de armazenamento e canal indisponível também podem impedir o envio.

**Atalhos existentes:** Ctrl/Cmd+Enter no envio; Ctrl/Cmd+Alt+J para próxima conversa, +F para foco da busca na fila e +N para nota no contexto; setas/Home/End nas listas e sugestões; Escape para fechar/cancelar onde implementado. Não criar atalhos que capturem indevidamente a digitação em campos e diálogos.

## 12. Mídia e anexos — duas funções diferentes

### 12.1 Mídia na conversa

Há representação para imagens, documentos, áudio/nota de voz, vídeo, figurinhas, localização, contatos e respostas interativas/botões recebidos, conforme conteúdo e capacidade do canal. Há ações de abrir/fechar imagem, reprodução quando o navegador/tipo permitir e download autorizado.

- Localização: nome/endereço/coordenadas e link para mapa; não pressupor um mapa interativo embutido já entregue.
- Contatos recebidos: cartão com dados disponíveis; não confundir com cadastro comercial do ERP.
- Botões/listas recebidos: apresentação da resposta/conteúdo; não equivale a um construtor completo de campanhas interativas.
- Mídia pendente, falha de download, tipo não permitido e objeto expirado precisam ter estados próprios.

O envio implementado inclui caminhos de imagem, documento, áudio e vídeo. Não é aceitação irrestrita de extensões: o arquivo passa por inspeção, limites e, quando aplicável, conversão. Há suporte de código para formatos usuais como JPEG, PNG, GIF, WebP, PDF, MP3/OGG/WAV, M4A e MP4, com tratamento específico de WebM/áudio gravado. O conjunto exato depende da direção, detecção dos bytes e contrato do canal.

Áudio gravado pode passar por conversão local para OGG/Opus. A implementação limita duração/tamanho e pode recusar se a ferramenta ou codec estiver indisponível. A gravação atual não entrega um player separado de pré-escuta antes do envio; não confundir o player de áudio recebido com essa função. Não oferecer transcrição por IA, tradução ou resumo automático como funções atuais.

### 12.2 Área técnica de anexos da conversa

Permite registrar, subir e validar arquivo no armazenamento privado; acompanhar progresso/estado; tentar de novo; obter download autorizado; consultar registro por identificador e limpar registros incompletos vencidos.

**Limites importantes:** essa área não é uma biblioteca global de arquivos. A lista de uploads da instância e a consulta por ID não equivalem a um explorador completo persistente de todos os anexos. Registrar um anexo nesse painel não o despacha automaticamente como mensagem. O envio de mídia acontece pelo fluxo do compositor.

Não projetar URL pública permanente para objetos privados. Downloads expiram e podem exigir nova autorização. Upload novo fica indisponível para conversa encerrada ou sem acesso. Arquivo rejeitado não deve aparecer como enviado.

## 13. Notas, prioridade, etiquetas e leitura

- Notas privadas: texto, autor autorizado e registro no atendimento; não enviadas ao contato.
- Nova nota e feedback do salvamento; repetição segura quando a resposta se perde.
- Prioridade Normal/Urgente; administrador e supervisor têm o controle de alteração examinado.
- Adicionar/remover etiquetas existentes na conversa.
- Marcar como lida/não lida no contexto do usuário.
- Não lida manual e quantidade de novas mensagens são informações diferentes.

**Limite do catálogo:** o frontend examinado seleciona etiquetas já cadastradas. Não há um editor dedicado completo de catálogo de etiquetas nessa área. Não desenhar uma administração completa de cores/hierarquias de etiquetas como já existente.

## 14. Atribuição, transferência e colaboração

- Assumir atendimento com validação de versão/posse/capacidade.
- Transferir para outro atendente, escolhendo candidato autorizado e observando ocupação/capacidade.
- Transferir para setor permitido.
- Devolver à fila.
- Ver mensagens de outro atendente visualizando/digitando, quando informadas pelo serviço.
- Reconhecer que a transferência pode retirar imediatamente o acesso atual à conversa.
- Tratar conflito: outra pessoa agiu antes, capacidade mudou, responsável/setor já não é válido.

Não há autorização para dois atendentes enviarem como donos simultâneos porque os dois abriram o chat. O design deve distinguir **observar a conversa** de **ter o controle do atendimento**.

## 15. Encerramento, reabertura e adiamento

### Encerrar

Diálogo com protocolo, motivo obrigatório, resultado obrigatório, síntese obrigatória e despedida opcional. Resultados: Resolvido, Sem resposta, Encaminhado ou Spam. Encaminhado a sistema externo não significa que a demanda externa foi concluída.

Prever validação de campos, operação em andamento, conflito, resposta incerta, encerramento confirmado e falha de despedida. O encerramento pode permanecer válido mesmo se a despedida falhar; não desfazer silenciosamente uma ação confirmada.

### Reabrir

Ação explícita no atendimento encerrado, quando permitida. Na entrada de nova mensagem, a janela X configurada pode reabrir o mesmo atendimento/protocolo com novo ciclo. Fora da janela ou com ela desligada, o fluxo pode gerar novo atendimento/protocolo. O frontend não deve decidir isso sozinho.

### Adiar

Motivo e instante futuro; atalhos atuais de 1 hora, 4 horas e Amanhã 9h; opção de informar data/hora. Mostrar o adiamento ativo e seu retorno. O recurso libera a vaga e pode retornar à fila/atendente elegível no prazo ou quando o cliente responde. Não é equivalente a fechar o atendimento ou guardar apenas um lembrete local.

## 16. Contatos e proteção do documento

### Diretório

Busca, paginação, lista com nome/iniciais e indicador de bloqueio, novo contato, editar contato, nome, telefone internacional, e-mail e observações. O formulário atual exige telefone ao criar contato manual. Bloquear/desbloquear com motivo é uma ação separada.

A visibilidade depende das conversas/setores autorizados ou da regra de criação manual. Não é uma agenda global compartilhada por todos os provedores.

### Histórico do contato

Consulta de atendimentos/ciclos autorizados com protocolo, estado e dados disponíveis. Não é o histórico completo do ERP. Eventos do atendimento podem atualizar essa consulta, e perda de acesso deve retirar conteúdo que não pode continuar sendo exibido.

### CPF/CNPJ opcional

- Mostrar ausência ou máscara; não exibir o número completo por padrão.
- Busca exata por documento, sujeita à autorização.
- Administrador/supervisor: definir, limpar e revelar temporariamente quando autorizados.
- Revelação atual por 15 segundos, com ação Ocultar e ocultação ao perder visibilidade da aba.
- Atendente comum não ganha edição/revelação apenas por ver o contato.
- Versão obsoleta, conflito de documento e acesso negado devem ter tratamento.

Documento não é obrigatório para conversar nem prova de identidade/autorização no ERP. Não criar coleta automática de CPF no WhatsApp como parte do design padrão.

## 17. Equipes, setores, pessoas e sessões

### Equipes/setores

Listar setores ativos/inativos; selecionar e consultar vínculos; administrador cria, renomeia, ativa/desativa e vincula/remove membros ou caixas; define setor padrão de uma caixa.

Não há hierarquia complexa de departamentos ou exclusão arbitrária de setores entregue nessa tela. Parte dos vínculos ainda é manipulada por identificadores técnicos no formulário atual. Trocar isso por seletores amigáveis exige usar um catálogo autorizado existente ou implementar a consulta ausente — não apenas desenhar opções fictícias.

### Pessoas do provedor

Listar membros e seus papéis/estados; convidar pelo e-mail; aceitar convite; desativar/reativar vínculo; consultar/revogar sessões, conforme autorização administrativa. Convite e recuperação reais dependem do serviço de e-mail configurado.

Papéis do provedor: Administrador, Supervisor, Atendente. Não há um editor geral de papéis personalizados com permissões arbitrárias.

O diretório operacional atual usa e-mail como rótulo. O nome local da aba não substitui um cadastro persistente de nome completo. Não pressupor foto de perfil, cargo, jornada e ficha de RH já disponíveis.

## 18. Presença, capacidade e distribuição

- Minha presença: Disponível ou Ocupado; estado de conexão confirmado, verificando ou indisponível.
- Conexão considerada por sessão/aba; não fabricar presença a partir de uma atribuição.
- Ocupação e capacidade de cada atendente; separação de abertas, aguardando e adiadas quando fornecida.
- Capacidade por membro, com alteração autorizada na gestão.
- Visão de distribuição por setor e caixa, fila e alertas de espera.
- Estado da atribuição automática por setor e comando de ligar/desligar para administrador.
- Decisão de distribuir sujeita a vínculo, disponibilidade, capacidade, horário e configuração.
- Sem elegível: manter fila/mostrar indisponibilidade; não prometer distribuição instantânea.

O frontend não decide que um atendente está online só porque tem uma conversa atribuída. Desligar uma aba também não torna offline alguém que continua conectado por outra.

## 19. Horário, plantão, triagem, inatividade e bot

### 19.1 Horário e plantão

Por caixa: intervalos por dia da semana; adicionar/remover intervalos; mensagens de boas-vindas, fora do expediente e ninguém disponível; setor de plantão; salvar/substituir ou retirar configuração. Consultar decisão para uma data/hora, mostrando expediente, próxima abertura e destino quando autorizado.

Fuso é o do provedor. Lista vazia significa fechado, não 24 horas. Administrador grava; supervisor consulta. A tela de configuração não envia automaticamente uma mensagem de teste só por abrir ou salvar.

### 19.2 Inatividade

Prazos para aviso e para encerrar após o aviso. A UI atual informa padrão de 15 + 15 minutos quando não configurado. O aviso precisa ser aceito pelo canal antes de armar o encerramento; não é um cronômetro livre que começa ao abrir a tela.

### 19.3 Tolerância offline

Configurar tolerância para o atendente sem abas conectadas; a regra participa da elegibilidade e da devolução de atendimentos conforme ciclo operacional. Aguardar cliente e conversar ativamente não são a mesma condição.

### 19.4 Janela de reabertura

Consultar/alterar janela em horas por caixa, incluindo desativada. A mensagem posterior é processada pelo servidor para decidir mesmo protocolo/novo ciclo ou novo atendimento.

### 19.5 Menu de triagem

Editor de opções com chave numérica, rótulo, destino humano ou setor; setor e motivo quando aplicáveis; adicionar/remover opções; tentativas antes do setor padrão. O editor atual exige opção para falar com atendente e limita o conjunto de opções/tentativas.

**Condicionado:** a execução do menu depende também da habilitação da caixa no runtime. Salvar um menu não significa que o serviço o ativou. Não existe editor visual geral de fluxos como Cezar/React Flow entregue no ChatISP.

### 19.6 Bot/IA externa

Há estrutura de posse, prazo, handoff, cancelamento de envio antigo e avisos de pendência de gancho de bot. O código examinado contém integração simulada/em processo, não uma plataforma completa de bots conectada a fornecedores de IA.

A interface atual consulta avisos do gancho. Não há painel completo entregue para cadastrar modelos, prompts, bases de conhecimento e assinaturas de IA. Não desenhar “bot GPT funcionando”, treinamento por documentos, conselho de agentes ou automação por IA como recursos operacionais atuais.

## 20. Respostas compartilhadas e assinatura

Catálogo do provedor com busca/paginação; atalho, título e conteúdo; criar, editar e excluir conforme papel. Uso no compositor por `/`.

Variáveis atuais: `nome_cliente`, `nome_atendente`, `protocolo`. São variáveis fechadas; não existe acesso arbitrário a todos os campos do ERP.

Assinatura: habilitar/desabilitar e editar prefixo. A variável aceita na assinatura é `nome_atendente`. O servidor acrescenta o prefixo na aceitação, sem duplicá-lo em uma repetição. O nome renderizado segue a fonte de identidade disponível; não presumir um nome completo ainda não cadastrado.

Atendente consulta/usa; administrador e supervisor editam o catálogo/assinatura conforme as regras examinadas. Prever resposta removida, variável inválida, catálogo indisponível e conflito de versão.

## 21. Notificações

Preferências de som, notificação do navegador e modo silencioso; estado da permissão e ação de solicitar autorização do navegador. Avisos de nova conversa/nova mensagem respeitam recorte, preferências e deduplicação; replay histórico não deve produzir uma enxurrada de avisos novos.

Notificação atual depende do painel/navegador aberto e da permissão concedida. Não é push nativo garantido com o aplicativo fechado. Prever permissão ainda não solicitada, negada, indisponível e som bloqueado pelo navegador.

## 22. Canais oficiais, saúde e templates

### 22.1 Configuração manual Meta

Lista de caixas e estado; número exibido; indicação de desativação/degradação; formulário com nome da caixa, número exibido, App ID, App Secret, WABA ID, Phone Number ID e token de acesso. Campos secretos não devem ser preenchidos com dados fictícios como se fossem credenciais válidas.

A versão da Graph API é configuração do runtime e depende da decisão/homologação; o formulário atual não entrega um seletor universal de versões. Embedded Signup e coexistência não são fluxos entregues nessa tela.

### 22.2 Saúde dos números

Estado/ativação; alerta de credencial; limite informado pela Meta, incluindo anterior quando recebido; nota de qualidade, data e identificação de simulação quando houver; falhas por código; envios incertos; quarentena; última mensagem recebida; ação Ler nota de qualidade.

Ausência de leitura não significa qualidade boa. Ausência de evento de limite não significa envio ilimitado. Não calcular um nível/tarifa local como se tivesse sido confirmado pela Meta.

### 22.3 Janela oficial de 24 horas

O backend revalida a janela de envio. **A interface atual informa que o prazo está indisponível no painel; não há contagem regressiva confiável fornecida ao usuário.** Uma barra com minutos exatos exige informação nova/contrato confirmado. Não confundir janela WhatsApp com janela X de reabertura do atendimento.

### 22.4 Templates

Consultar catálogo armazenado, mostrar estados/idioma e permitir o envio admitido pela política. Administrador sincroniza o catálogo. O pedido de envio pode exigir finalidade; há estados de confirmação, recusa, cancelamento e incerteza.

Não existe um editor completo de criação e aprovação de templates pela Meta entregue nessa interface. Não transformar “enfileirado” em “entregue”.

### 22.5 Consentimento

Formulário para registrar finalidade, origem, evidência e concessão/revogação. **A tela atual registra eventos e não consulta o estado consolidado atual.** Um switch mostrando “Consentimento ativo” ao carregar exige consulta adicional; não deve nascer ligado por suposição.

### 22.6 Evolution

Existe entrada/contrato, mas o conector operacional e QR não estão entregues/homologados. Mantido desligado. Desenhar como integração pendente/indisponível, não com QR falso ou sessão conectada.

## 23. ERP existente e painel externo

### 23.1 ERP — parcial

Há UI/contratos para vínculos de cliente/contrato, selecionar/remover vínculo, referência autorizada, identificação, resumo do cliente, consulta de faturas, envio confirmado de documento/Pix e associação do protocolo ERP ao protocolo local.

O painel trata sem permissão, sem vínculo/seleção, cadastro não encontrado, faturas ocultas, sem faturas, falha, circuito aberto e resposta incerta. Campos de verificação e chaves locais ainda aparecem em partes técnicas do fluxo.

**O adaptador real do fornecedor continua incompleto.** Esses controles, o fake e seus testes não comprovam consulta a SGP/IXC nem envio de boleto/Pix real. Verificação do cliente e aprovação de ação sensível têm lacunas documentadas. Não apresentar essas ações como prontas em produção.

Há configuração do endereço HTTPS de “Abrir cadastro no ERP”, com `{cliente}` como referência permitida. O link não deve incorporar senha, token, CPF/CNPJ ou transformar qualquer referência em documento pessoal.

Não incluir cadastro comercial completo, gerar cobrança, negociar dívida, bloquear/desbloquear internet, provisionar ONU, acessar MikroTik, abrir OS ou agenda de campo como módulos nativos existentes do chat.

### 23.2 Painel externo incorporado — condicionado

Configurar origem permitida e URL do painel; salvar/remover; exibir iframe autorizado com contexto; abrir fora do chat. Depende de autorização, origem/URL, conteúdo do sistema externo e suas restrições de incorporação.

Não presumir que qualquer site pode ser embutido, que o iframe compartilha sessão livremente ou que recebe CPF e credenciais. Sem configuração/aceite do contexto, ele pode não aparecer.

## 24. Relatórios e uso do canal

### Relatório de atendimento

Período inicial/final, agrupamento por dia/semana/mês e filtros de setor/caixa autorizados. Mostrar fuso, conversas, ciclos e mensagens; primeira resposta humana; tempo total até encerramento; tempo operacional; espera na fila; médias, medianas, amostras e não aplicáveis onde disponíveis.

Dimensões: resultados e autoria; setores; atendentes; caixas; motivos principais; etiquetas adicionais; mapa de calor por dia da semana e hora; séries do agrupamento. Exportação CSV.

Não confundir tempo corrido e tempo operacional. Etiquetas podem se sobrepor e não fechar o total. Sem amostra não significa zero segundos. O recorte por usuário/papel continua vindo da API.

### Uso reportado pela Meta

Consulta por período e contagens por categoria/indicador de cobrança, conforme eventos recebidos. Há indicador desconhecido. **Não é uma fatura financeira nem cálculo confirmado em reais.** Não criar gráfico de faturamento do provedor ou saldo de assinatura de IA.

## 25. Configurações, segurança e administração

### Identidade

Nome exibido do provedor e logo; salvar/remover logo. O editor atual admite PNG/JPEG, até 256 KiB e 1024 × 1024 pixels. Não há editor de imagem completo, personalização de todos os temas nem white-label irrestrito entregue.

### Usuários e segurança

O login tem e-mail/senha e opções avançadas de identificador do provedor e código do aplicativo/recuperação. Esse campo de identificador não equivale a um seletor visual de todas as empresas já implementado.

Convites, papéis, estado dos membros, sessões/revogação e autenticação em duas etapas. Login e recuperação precisam de estados de erro, bloqueio, token inválido/expirado e serviço de e-mail indisponível. MFA tem QR/código local e códigos de recuperação conforme etapa; não confundir com QR do WhatsApp.

### Administração de provedores

Área restrita de plataforma para cadastro/estado/configuração administrativa dos provedores e convite do primeiro administrador, conforme componentes e APIs existentes. Suspensão do provedor precisa retirar acesso operacional. Não é uma central para ler livremente todas as mensagens de todos os clientes.

### Preferência local de aba

Definir nome de exibição local, validar e guardar no dispositivo/aba conforme o mecanismo atual. Não é edição do nome central do usuário nem sincronização automática entre dispositivos.

## 26. Integrações técnicas, API pública e webhooks

### Tokens

Gerar token selecionando escopos e limite de requisições por minuto; mostrar o segredo uma única vez, copiar/ocultar; listar identificador, escopos, limite e revogação; revogar.

Escopos atuais: leitura geral; contatos (leitura/escrita); conversas (leitura/escrita); mensagens (leitura); envio de template; leitura de cadastros. O catálogo é fechado; não é um campo de permissões arbitrárias.

### Webhooks de saída

Cadastrar URL e eventos: conversa criada, atribuída, encerrada e mensagem criada. Mostrar segredo de assinatura uma vez. Listar assinatura, eventos e estado; desativar; consultar entrega e seus estados/tentativas disponíveis.

Não é um construtor geral de integrações sem código. Não incluir eventos adicionais somente porque o Chatwoot os possui.

### API pública

Há contratos para contatos, consultas, mensagens e operações de conversa nos escopos correspondentes. A UI oferece acesso à documentação OpenAPI. Não transformar todos os endpoints em novos módulos visuais redundantes.

## 27. Auditoria, alertas, retenção e infraestrutura

### Auditoria

Consulta administrativa por período com ação, alvo, resultado e data, conforme o recorte autorizado. Não exibir segredos, documento completo ou conteúdo desnecessário em logs visuais.

### Alertas operacionais

Painel de job antigo, conector desconectado e webhook sem conclusão; limites de idade de job/webhook configuráveis. Não há envio externo automático de e-mail/alerta garantido por esse painel.

Métricas técnicas adicionais existem em listener privado, mas não há dashboard completo de Prometheus/Grafana embutido entregue. O design não deve inventar valores de CPU, RPO ou filas internas sem uma API de produto correspondente.

### Retenção e demandas

Política por classe: conteúdo, mídia, envelope, registro/log e tarefa/job; prazos podem não estar definidos. Nome do provedor; guardar política; uso atual e limites configurados de pessoas/números/armazenamento.

Exportar demandas/histórico estruturado; exportar dados de um titular; excluir dados do titular com confirmação explícita e resultado de remoção/anonimização, respeitando a implementação. Não é um botão genérico para apagar todo o histórico comercial no ERP.

**Expurgo automático:** permanece desligado, e seu executor não está implementado. A exclusão individual autorizada não equivale a ter um motor de expurgo automático pronto.

### Operação fora do frontend

PostgreSQL, armazenamento privado, workers, backup/restauração, HTTPS, migrações, logs, isolamento e limites dão suporte ao produto. Existem scripts/provas operacionais no repositório. Isso não implica haver botões de restaurar banco, editar container ou executar terminal na interface do atendente.

## 28. Estados e regras transversais obrigatórios no desenho

Para cada tela e ação, conferir quando aplicável:

- Carregando e operação em andamento, com proteção contra duplo clique.
- Lista vazia real; sem resultado por filtro; sem permissão; recurso não configurado.
- Erro recuperável; tentar novamente; conflito de versão e necessidade de recarregar.
- Sessão expirada, MFA pendente, vínculo desativado e provedor suspenso.
- WebSocket desconectado, reconectando, ressincronizando e acesso revogado.
- Diretório parcial/negado: não preencher com nomes antigos nem IDs apresentados como nome.
- Canal desativado/desconectado/degradado, credencial inválida, qualidade/limite desconhecido.
- Conversa encerrada, acesso perdido após transferência e controle humano/bot/menu alterado.
- Capacidade cheia, ninguém elegível e espera excedida.
- Falha no envio, resposta incerta, repetição da mesma intenção e nova decisão explícita.
- Upload em progresso, quota/tamanho/formato recusados, conversão indisponível e download expirado.
- Dados demonstrativos identificados; conteúdo externo indisponível sem falsa confirmação.
- Estado sem amostra/sem dados históricos sem fabricar uma métrica.

O layout não pode conservar o conteúdo de outro usuário após a troca de conta nem mostrar um chat antigo como se ainda houvesse acesso. Repetir uma ação incerta não pode ganhar automaticamente aparência de uma nova mensagem.

## 29. Desktop, celular e acessibilidade

**Desktop:** navegação global; fila com controles estáveis e rolagem própria; conversa/histórico; compositor acessível; contexto lateral organizado em grupos. Selecionar um atendimento mais abaixo não deve fazer sumirem busca e abas.

**Celular:** menu recolhido; fila e conversa como vistas; voltar à fila sem perder rascunho; detalhes em área própria; diálogos e formulários que caibam na largura; compositor utilizável. Não exigir três colunas estreitas simultâneas.

**Teclado/acessibilidade:** foco visível, rótulos distintos para caixa principal e filtro, estados não dependentes só de cor, nomes de botões, navegação por teclado, leitura coerente de contadores, confirmação de ação destrutiva, contraste e texto legível.

Não presumir modo offline completo. A existência de rascunho/fila local não autoriza dizer que o canal enviou uma mensagem sem rede. A notificação do navegador não substitui um aplicativo nativo.

## 30. Capacidades que não devem ser copiadas automaticamente do Chatwoot

Não foram identificadas como produto entregue atual: widget de site/SDK móvel de chat; caixa multicanal de e-mail/Instagram/Telegram; telefonia/videochamada; campanhas em massa; central de ajuda/base de conhecimento pública; CSAT/NPS completo; transcrição/resumo/tradução por IA; copiloto treinado nos documentos; editor gráfico genérico de bots; CRM com funil comercial; editor arbitrário de campos e papéis; reação de saída por clique; galeria global completa de arquivos; prévia textual da última mensagem na fila; nome completo centralizado do atendente com edição própria; sincronização de foto do WhatsApp.

Algumas constam como V2 ou possibilidade no planejamento; outras estão explicitamente fora do escopo. Se aparecerem no novo conceito visual, devem ser marcadas como proposta nova, nunca como função atual omitida neste inventário.

## 31. Pendências reais e limites da avaliação

A matriz vigente contém **206 requisitos marcados como implementados e verificados, 32 aguardando homologação externa e 19 parciais**, totalizando 257. Essa é a classificação do repositório; este inventário não reexecutou todos os testes e não promove requisitos por existência de tela.

As maiores pendências são adaptador real do ERP, verificação/aprovação de ações sensíveis, Evolution operacional, executor de expurgo automático e capacidade do piloto. Integrações Meta, e-mail e ambiente definitivo requerem configuração e homologação correspondente.

Há também limitações de produto/UX já apontadas: ausência de prévia textual na fila; rótulos de atendente por e-mail; catálogo de etiquetas sem editor dedicado completo; área técnica de anexos sem biblioteca geral; janela oficial sem countdown consultável e consentimento sem consulta consolidada na tela.

O backend já ter determinado contrato não significa haver editor visual completo para ele. Da mesma forma, um controle aparecer em uma tela não comprova que o fornecedor externo está integrado.

## 32. O que o trabalho de design precisa entregar

1. Arquitetura de informação cobrindo as seis áreas principais e as áreas administrativas por papel.
2. Conversas completas em desktop e celular, não somente uma tela bonita preenchida.
3. Variações de fila, responsável, estado, prioridade, leitura, espera, adiamento e perda de acesso.
4. Compositor e mensagens cobrindo texto, citação, resposta pronta, áudio, mídia e falhas/incerteza.
5. Contexto com notas, etiquetas, contato, documento protegido, canal, anexos e integrações condicionadas.
6. Formulários/diálogos de assumir, transferir, adiar, encerrar, reabrir, bloquear e privacidade.
7. Telas de contatos, equipes, usuários, relatórios, canais e todas as famílias de configurações.
8. Estados vazios, carregamento, erro, sem acesso, não configurado e indisponível para cada família.
9. Separação explícita entre função atual, dependência de integração e proposta nova.
10. Conferência contra a checklist de telas/estados e contra os 257 requisitos anexos.

Pode reorganizar grupos, rótulos e navegação para tornar a operação mais clara. Não pode apagar regras obrigatórias para simplificar uma imagem, colocar informações inventadas em lugar de dados ausentes ou apresentar módulos de um ERP como se fossem funcionalidades nativas do chat.

## 33. Fontes e rastreabilidade

- `web/src/routes.tsx` e `web/src/lib/areas.ts`: rotas e áreas.
- `web/src/pages/`: telas e composição dos módulos.
- `web/src/features/queue/`, `conversation/`, `annotations/`, `assignment/`: operação central.
- `web/src/features/media/`, `attachments/`, `cannedreplies/`, `channelpolicy/`: mensagens e políticas.
- `web/src/features/contacts/`, `contactdocs/`, `erp/`, `embed/`: contexto e integrações.
- `web/src/features/automation/`, `businesshours/`, `distribution/`, `presence/`, `bothook/`: operação e automação.
- `web/src/features/reports/`, `channelhealth/`, `audit/`, `opsalert/`, `retention/`, `branding/`, `integrations/`: gestão.
- `web/src/components/auth/`, `components/settings/`, `stores/session-store.ts`: conta e administração.
- `backend/internal/sectors/policy.go`: recortes de acesso atuais.
- `backend/internal/attachments/validation.go` e contratos de mídia: validação e capacidades.
- `docs/especificacao.md`: fronteira entre webchat, ERP, V1, V2 e excluídos; não é prova isolada de implementação.
- `docs/matriz-requisitos.md`: matriz integral vigente, copiada como anexo.
- `docs/contracts/`: contratos por módulo; alguns são históricos e suas declarações de “a integrar” precisam ser confrontadas com a composição atual.

Não foram alterados dados, políticas, canais ou serviços para produzir este inventário.
