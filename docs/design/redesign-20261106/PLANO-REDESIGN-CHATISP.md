# ChatISP — planejamento da reformulação visual

Versão 1.0 · 06/11/2026 · proposta para avaliação do proprietário.

## 1. Decisão proposta

Transformar o frontend em uma estação de atendimento profissional: fila legível, conversa como área principal, compositor consistente e contexto do cliente acessível sem abandonar o atendimento.

Direção recomendada: **Atendimento claro**. Tema claro por padrão, superfícies neutras, texto grafite e verde-petróleo como cor de ação. Interface compacta, mas legível durante um turno inteiro. O celular recebe uma navegação própria, preservando a mesma conversa e suas regras.

Esta entrega contém pesquisa, especificação e imagem conceitual. Não autoriza por si só publicação ou mudanças de comportamento. Backend, bancos, sessões, dados e serviços não foram alterados pelo planejamento.

## 2. Problema observado

A avaliação anterior abriu a aplicação publicada em desktop 1440×1000 e celular 390×844. As capturas estão neste diretório.

- O painel apresenta números e rótulos como linhas de texto, com pouca hierarquia.
- Há muito espaço vazio sem composição útil.
- Presença/capacidade aparecem soltas na navegação.
- O cabeçalho apresenta a mensagem de configuração “O nome desta aba ainda não foi definido”.
- O estado sem caixas não orienta o usuário conforme seu papel.
- No celular, a navegação horizontal esconde parte dos destinos e o cabeçalho ocupa altura relevante.
- Não havia caixa disponível para essa conta. Portanto, ainda não existe avaliação visual de uma conversa real preenchida nessa instalação.

Antes de implementar, conferir se a versão publicada corresponde ao commit, CSS, fontes e assets esperados. Uma falha de entrega de estilos deve ser corrigida na origem, não escondida com outra camada visual. A necessidade de conferir isso não equivale a diagnóstico de CSS quebrado: a causa ainda não foi comprovada.

## 3. Referências e o que aproveitar

| Referência | Evidência consultada | Princípio a aproveitar | Limite |
|---|---|---|---|
| Chatwoot | Captura oficial local e documentação de dashboard | Separar navegação, fila, conversa e contexto; painel de contato recolhível | Não copiar indiscriminadamente código/assets; edição Enterprise tem licença própria |
| Linear | Artigos oficiais de redesign e atualização de interface | Hierarquia, alinhamento, densidade e navegação que não competem com o conteúdo | Não transformar atendimento em gerenciador de tarefas ou copiar a marca |
| Intercom | Documentação oficial de Inbox e personalização | Atalhos, ações contextuais, contexto do cliente e redução de esforço repetitivo | Referência de interação; nenhum código proprietário será reutilizado |

Fontes:

- https://app.chatwoot.com/hc/user-guide/articles/1677231493-lesson-2-dashboard-basics
- https://linear.app/now/behind-the-latest-design-refresh
- https://linear.app/now/how-we-redesigned-the-linear-ui
- https://www.intercom.com/help/en/articles/6258745-the-inbox-explained
- https://www.intercom.com/help/en/articles/7911926-customize-the-inbox-to-suit-you-and-how-you-work-best

As fontes sustentam os princípios citados, não uma alegação de que as implementações ou planos comerciais são equivalentes. A organização proposta abaixo é uma decisão para o ChatISP.

## 4. Alternativas consideradas

| Direção | Aspecto | Ganho | Problema |
|---|---|---|---|
| Atendimento claro — recomendada | Cinzas neutros, branco, grafite e verde-petróleo discreto | Foco, leitura, identidade própria e boa diferenciação de estados | Exige disciplina para não deixar controles e textos excessivamente pálidos |
| Operação escura | Grafite, superfícies escuras, acentos de cor | Pode ser confortável para parte dos operadores e uso noturno | Adiciona outra matriz de contraste; não resolve sozinho a organização |
| Comercial com grandes cards | Cores fortes, blocos grandes e muitos indicadores | Impacto inicial em demonstração | Ocupa espaço, prejudica a densidade e compete com mensagens |

Escolha: Atendimento claro primeiro. Preparar tokens para tema escuro, mas homologá-lo depois que a experiência clara estiver consistente. Não entregar duas interfaces medianas ao mesmo tempo.

## 5. Princípios do produto

1. Em até poucos segundos, entender quem está aguardando, o que precisa de resposta e qual conversa está em foco.
2. Manter os principais controles no mesmo lugar em todas as conversas.
3. Informações secundárias ficam recolhíveis; alertas relevantes permanecem visíveis.
4. Cor sempre acompanha texto ou ícone; não é a única forma de distinguir um estado.
5. Ausência de informação deve parecer indisponibilidade ou falta de configuração, nunca zero confirmado.
6. Sem indicador inventado de saúde de conexão, débito, latência ou status de equipamento.
7. O frontend não concede permissões nem muda critérios de distribuição, janela WhatsApp ou autorização ERP.
8. O usuário deve continuar reconhecendo sua conversa ao alternar entre computador e celular.

## 6. Arquitetura de informação

### Navegação global

Preservar inicialmente os destinos e URLs existentes: Painel, Conversas, Contatos, Equipes, Relatórios e Configurações. Na apresentação, Conversas pode aparecer como Atendimento somente se rótulo, título e ajuda forem harmonizados; não mudar nomes silenciosamente em toda a aplicação.

O atendimento será a área principal para quem atende; Painel é a visão resumida, especialmente útil ao supervisor. Não obrigar todos os papéis a percorrer um dashboard antes de trabalhar.

Identificação do provedor no topo da navegação. Identificação do operador, presença e capacidade num bloco compacto inferior. Configurações e rotas continuam respeitando os papéis existentes.

### Desktop: quatro regiões, três colunas de trabalho

| Região | Largura inicial orientativa | Conteúdo |
|---|---:|---|
| Navegação | 176–208 px | Provedor, áreas e operador |
| Fila | 288–336 px | Busca, recorte, filtros e conversas |
| Conversa | Flexível; alvo mínimo 440 px | Cabeçalho, mensagens, eventos e compositor |
| Contexto | 280–320 px, recolhível | Contato, responsáveis, etiquetas, histórico e ERP |

As quatro regiões só ficam abertas quando a largura útil comportar seus mínimos. Em 1366 px, a navegação pode recolher e o contexto abre sob demanda; em telas menores, não reduzir mensagens a uma coluna estreita só para preservar todos os painéis.

Fila e histórico têm rolagem própria. O compositor permanece acessível. Somente a área ativa controla a rolagem; evitar uma cadeia de pequenos painéis aninhados.

### Celular

- Lista de atendimentos em tela própria, com busca e filtros acessíveis.
- Selecionar um item abre a conversa em largura total.
- Cabeçalho com Voltar, nome do contato e acesso aos detalhes.
- Dados do contato e ações secundárias em drawer/tela de detalhes.
- Compositor considera teclado virtual e safe area; não encobre a última mensagem.
- Voltar restaura seleção, filtro e posição da fila quando apropriado.
- Evitar menu global horizontal comprido. Usar navegação compacta com poucos destinos e Mais, ou drawer claramente identificável.
- Gestos podem complementar botões; nenhuma ação essencial depende somente de gesto.

## 7. Tela de atendimento

### Fila

Cada linha informa contato, prévia da mensagem, tempo relevante, estado e indicador de não lida. Etiquetas são poucas e subordinadas à mensagem. Nome e tempo usam posições previsíveis para varredura rápida.

Filtros iniciais sugeridos: Minhas, Na fila e Todas, respeitando a autorização. Estados aguardando/adiadas/encerradas continuam disponíveis. A contagem precisa corresponder ao escopo do filtro; não misturar totais de outra caixa ou setor.

Conversas selecionadas usam fundo discreto e uma indicação adicional, sem transformar a lista num bloco colorido. Foco por teclado é diferente de seleção.

Não acrescentar prioridade funcional ou políticas de SLA apenas porque a imagem tem um marcador de espera. Usar somente estados e tempos disponíveis; recursos novos entram em backlog próprio.

### Histórico

Mensagens do cliente e do atendente distinguíveis por alinhamento e superfície, com contraste suficiente. Horário e estado de envio são secundários, mas legíveis. Notas internas usam superfície âmbar e rótulo explícito, sem depender só da cor.

Eventos administrativos são compactos. Falhas e envio incerto são visíveis e oferecem apenas ações já suportadas pelo contrato. Uma nova mensagem não deve arrastar o usuário ao fim enquanto ele lê conteúdo antigo; apresentar indicador para retornar às novas mensagens.

Áudios, imagens, documentos, respostas citadas, mensagens longas e caracteres Unicode precisam caber sem desorganizar o layout. Evitar avatares externos automáticos: iniciais são fallback previsível.

### Compositor

Uma ação primária Enviar. Responder e Nota interna têm separação clara. Anexos, áudio, emoji e respostas prontas são controles secundários, com nomes acessíveis.

Preservar os mecanismos existentes de rascunho, escopo, anexos, recibos, janela WhatsApp, templates, colisão e idempotência. Revisar a tecla Enter conforme a preferência existente; atalhos não podem disparar durante composição IME. Não implementar atalhos globais quando o foco está num campo editável.

### Contexto e ERP

Ordem: contato, atendimento/responsáveis, etiquetas, histórico e integração ERP. Conteúdo útil em vez de formulário permanente com todos os campos.

ERP não conectado: mostrar Não conectado e explicar a indisponibilidade. Falha temporária: mostrar falha e horário da última leitura quando existir. Não trocar falha por dados fictícios. Não apresentar ações de segunda via, desbloqueio ou cobrança como operacionais antes de implementação e homologação.

A imagem usa contato e conversa sintéticos, com identificação de demonstração. Qualquer visualização ilustrativa de ERP continua identificada; nenhum dado de demonstração será inserido na instalação operacional para melhorar capturas.

## 8. Sistema visual inicial

Valores iniciais, sujeitos à conferência de contraste nos pares efetivamente utilizados:

| Token | Proposta | Uso |
|---|---|---|
| Fundo de trabalho | #F5F7F8 | Regiões secundárias |
| Superfície | #FFFFFF | Conversa, popovers e formulários |
| Texto principal | #172329 | Conteúdo e títulos |
| Texto secundário | #5B6971 | Metadados legíveis |
| Ação | #0B6B65 | Enviar, seleção e foco contextual |
| Seleção discreta | #E8F3F1 | Item selecionado |
| Borda | #DCE3E6 | Separadores e inputs |
| Aviso | #86570B sobre #FFF4D6 | Atenção/nota, com rótulo |
| Erro | #B42318 sobre #FEF3F2 | Falhas, sem substituir ícone/texto |

- Aproveitar Geist e Geist Mono já presentes; fonte mono somente onde ajuda, como protocolo.
- Corpo 14–16 px; títulos de página aproximadamente 20–24 px, sem cabeçalhos gigantes.
- Escala espacial 4/8/12/16/24/32 px.
- Ícones Lucide existentes, tamanhos e alinhamentos consistentes.
- Raios 6–10 px. Bordas discretas. Sombras restritas a camadas flutuantes.
- Campos e botões usam componentes comuns e estados hover/foco/disabled/loading.
- Meta de contraste WCAG AA; foco visível. Alvos de toque preferencialmente 44×44 px, respeitando os critérios/exceções aplicáveis.
- Sem gradientes, glassmorphism, contornos excessivos ou animação contínua. Respeitar prefers-reduced-motion.
- Densidade compacta poderá ser opção futura; primeiro homologar uma densidade confortável que caiba nas telas operacionais.

## 9. Demais páginas

| Área | Organização proposta |
|---|---|
| Painel | Poucos indicadores acionáveis: fila, atendimento, aguardando, maior espera e disponíveis; lista de atenção abaixo |
| Contatos | Busca evidente, tabela compacta, identificação consistente e detalhe em painel |
| Equipes | Pessoas, setor, presença e capacidade; cadastro separado da visão operacional |
| Relatórios | Filtros consistentes, definições de métricas, tabela e exportação; gráficos somente quando explicam uma pergunta |
| Configurações | Navegação local por assunto, seções curtas, descrições claras e ações de salvar previsíveis |
| Login/MFA | Identidade consistente, formulário legível, erros úteis e sem anunciar integrações indisponíveis |

O papel do superadministrador não deve ser confundido com o atendente. As telas e orientações de ausência de configuração precisam refletir essa distinção.

## 10. Estados obrigatórios

| Situação | Tratamento de produto |
|---|---|
| Sem caixas configuradas | Explicar e oferecer configurar somente ao administrador autorizado |
| Sem vínculo/permissão | Orientar a procurar o administrador; não sugerir que o usuário crie uma caixa |
| Sem conversas no filtro | Explicar o recorte e permitir limpar filtros |
| Carregando | Skeleton do componente; nada de tela inteira piscando |
| Erro de rede | Mensagem localizada, tentativa novamente e preservação do rascunho |
| Reconectando | Estado real da conexão, sem afirmar que a mensagem foi entregue |
| Acesso revogado | Limpar conteúdo protegido conforme o contrato atual e apresentar próximo passo seguro |
| Envio incerto | Diferenciar de erro definitivo e não oferecer reenvio duplicado automaticamente |
| ERP indisponível | Estado explícito, sem dados fictícios de fallback |
| Sessão expirada | Recuperar a navegação dentro dos limites de segurança; não prometer preservar conteúdo que deva ser apagado |

## 11. Reaproveitamento técnico

Manter React, TypeScript, Vite, Tailwind, componentes UI/Radix, Lucide, TanStack Query e os contratos existentes. Não trocar a stack para aproximar visualmente do Chatwoot.

| Código existente | Trabalho previsto |
|---|---|
| web/src/index.css | Consolidar tokens, tipografia e bases; conferir entrega de assets |
| components/layout/app-shell.tsx e area-nav.tsx | Shell e navegação responsiva |
| components/ui/ | Componentes compartilhados e estados |
| features/presence/ e branding/ | Bloco do operador e identificação do provedor |
| features/queue/ e assignment/ | Apresentação da fila e ações, mantendo regras |
| features/conversation/conversation-workspace.tsx | Composição das regiões, preservando estado e callbacks |
| features/conversation/message-composer.tsx | Apresentação do compositor, mantendo envio e rascunhos |
| features/conversation/conversation-sidebar.tsx | Contexto recolhível |
| features/reports/ | Apresentação dos indicadores existentes, sem recalcular métricas no frontend |
| pages/ e components/settings/ | Aplicação progressiva do padrão |

Mudanças na estrutura React podem remontar componentes e perder rascunhos/seleção. Evitar renderizações desktop/mobile duplicadas que iniciem dois WebSockets ou dois efeitos de envio. O layout deve compartilhar uma única fonte de estado por conversa.

O documento vigente docs/chatwoot-referencia.md permite estudar a edição Community e proíbe importar código/assets do checkout para o produto. Esta proposta usa princípios, não copia componentes de outra stack. Qualquer mudança nessa política precisa ser registrada explicitamente.

## 12. Plano de execução proposto

| Etapa | Entrega | Critério para avançar |
|---|---|---|
| D0 — esta entrega | Referências, direção, plano e imagem conceitual | Avaliação da direção pelo proprietário |
| D1 — protótipo de interface | Uma tela de atendimento com dados sintéticos, desktop/celular, num ambiente isolado | Comparar hierarquia e tarefas; separar ilustração de funcionalidade |
| D2 — shell e componentes | Tokens, navegação, cabeçalho, inputs, status e estados vazios | Fontes/CSS corretos, foco/contraste, papéis preservados |
| D3 — atendimento integrado | Fila, histórico, compositor, anexos e contexto ligados aos contratos reais existentes | Jornada do atendimento e rascunhos sem regressão |
| D4 — restante do produto | Painel, contatos, equipes, relatórios, configurações e autenticação | Consistência entre páginas e papéis |
| D5 — validação consolidada | Navegador, permissões, acessibilidade e comparação antes/depois | Correções dos achados e evidências do mesmo commit |
| D6 — publicação | Build identificado, backup, alteração apenas dos serviços necessários e rollback | Autorização de publicação, acesso conferido e serviços preservados |

Não estimar prazo confiável sem avaliar a integração do protótipo aos componentes de atendimento. Evitar grandes ciclos de aprovação por detalhe: a aprovação principal é da direção da tela e do comportamento; decisões menores de espaçamento seguem o sistema visual.

## 13. Validação proporcional

Durante implementação, executar build/typecheck e verificações focadas quando houver mudança de comportamento. Concentrar a rodada visual e de integração mais ampla no fechamento de cada marco significativo. Não repetir todas as suítes depois de ajustar um raio ou espaçamento.

Critérios de aceite:

- Conferir 1440×900, 1366×768, 1024×768, 390×844 e 320 px de largura.
- Sem rolagem horizontal global nas jornadas principais.
- Conversa e compositor acessíveis com teclado virtual aberto.
- Busca, seleção, resposta, nota interna, transferência, adiamento e encerramento funcionam conforme as permissões e contratos existentes.
- Trocar de conversa, navegar/voltar, reconectar e mudar layout não envia duas vezes nem expõe dados de outro escopo.
- Rascunho é preservado ou removido conforme a regra de segurança atual, nunca por acidente de layout.
- Conteúdo longo, mídia, anexos, falha e envio incerto cabem e têm estados distintos.
- Sem mensagens provisórias de implementação no cabeçalho e sem funcionalidade fictícia apresentada como operacional.
- Estados vazios são úteis e distinguem ausência de dados, configuração e autorização.
- Foco visível, nomes acessíveis, contraste e uso sem mouse conferidos; testes automatizados de acessibilidade não substituem inspeção manual.
- Registrar as capturas do mesmo commit e identificar ambiente/dados sintéticos.

## 14. Entregáveis desta proposta

- PLANO-REDESIGN-CHATISP.md — este documento.
- PROMPT-IMAGEM-CONCEITUAL.txt — briefing enviado à ferramenta de imagem.
- CONCEITO-CHATISP-DESKTOP-MOBILE.png — imagem de direção, quando a geração terminar.
- Capturas atuais desktop/mobile e referência Chatwoot já presentes neste diretório.

A imagem não é tela implementada, prova de responsividade ou evidência de que o ERP foi conectado. Textos pequenos e proporções podem exigir ajustes no protótipo real. Na implementação, critérios de acessibilidade e contratos do produto prevalecem sobre decisões meramente ilustrativas.

## 15. Recomendação final

Aprovar ou ajustar a direção visual de uma tela completa de atendimento antes de alterar o sistema inteiro. Depois construir o protótipo isolado e integrá-lo progressivamente. O objetivo é um produto consistente e útil em cada turno, com identidade própria, sem outra reescrita de backend.
