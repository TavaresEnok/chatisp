# Demonstração de atendimento e revisão de cobertura — ChatISP / Chatwoot

Data: 07/11/2026. Ambiente autorizado pelo proprietário para dados fictícios.

## Demonstrações disponíveis

| Sistema | Conversas | Mensagens de conversa | Notas privadas | Abas conferidas |
|---|---:|---:|---:|---|
| Chatwoot | 48 | 261, incluindo notas e excluindo eventos do sistema | 40 | Minhas: 13; Não atribuídas: 10; Todos: 41 no filtro Abertas |
| ChatISP | 36 | 180, além das notas separadas | 36 | Minhas: 9; Não atribuídas: 9; Todas: 36 |

Os totais das abas não devem ser somados: Todas inclui as demais. No Chatwoot o filtro de estado exibido é Abertas; os 48 atendimentos incluem os encerrados.

Cenários: falta de conexão, luz LOS, cobertura Wi-Fi, quedas intermitentes, roteador reiniciando, pagamento em análise, segunda via, alteração de vencimento, planos, instalação, mudança de endereço e visita técnica. Existem responsáveis distintos, casos aguardando o cliente, encerrados, prioridades, etiquetas, notas internas e mensagens com citação.

ChatISP: cinco contas demonstrativas de equipe, mais o administrador existente; três setores; oito etiquetas e sete respostas rápidas. Não foram fabricadas presenças online: o estado de conexão dos atendentes continua sendo o real.

Foram acrescentados três PDFs no ChatISP, registrados, enviados, validados e baixados pelas APIs reais de anexos/armazenamento, e um PDF no Chatwoot, além dos documentos demonstrativos em texto. Não são comprovantes ou ordens reais.

Acesso:
- ChatISP: https://chatisp.tokstiloesporte.com/conversas
- Chatwoot: http://168.194.13.18:8098/app/accounts/1/dashboard

Os administradores mantêm os logins anteriores. As credenciais dos novos atendentes ficam somente no arquivo privado `/home/enok/.local/share/chatisp/demonstracao-20261107/acesso-atendentes.txt`. Senhas, hashes, ambiente, dump e SQL contendo hashes não acompanham este relatório nem o Git.

## Correções de interface encontradas e aplicadas

1. A API da fila já fornecia contagens, estado, setor, responsável e horário, mas a apresentação não aproveitava todos esses campos. Agora são exibidos sem inventar prévias de mensagem.
2. A busca e as abas podiam sair da tela ao selecionar um atendimento mais abaixo. Os resultados agora rolam dentro da fila, mantendo os controles acessíveis.
3. A seleção inicial deixava a área vazia mesmo quando havia apenas uma caixa autorizada. Nesse caso a única caixa é selecionada como valor inicial derivado; a lista autorizada continua vindo da API. Não há identificação por UUID arbitrário nem alteração de permissão.
4. O seletor principal passou a se chamar “Caixa de atendimento”, distinguindo-o do filtro “Caixa”. Os testes conservam os mesmos cenários e foram ajustados ao nome acessível distinto.
5. Configurações ainda usava o resumo genérico de protótipo e cartões estáticos dizendo “Nada configurado” / “Ainda sem registros”. Para sessões autenticadas foram substituídos por atalhos reais, respeitando o papel. Os editores existentes continuam montados; não foram substituídos por cards fictícios.
6. O nome local da aba permanece disponível, inclusive sem sessão, e seu isolamento entre usuários continua verificado. O cabeçalho não o apresenta como identidade oficial de outro atendente.
7. As notas privadas exibiam o UUID do autor. Agora usam o diretório autorizado; na ausência do registro não adivinham o nome.
8. O estado “não lida” contaminava as iniciais do avatar na fila. O avatar agora usa o nome original do contato.
9. O nome longo do provedor recebeu ajuste de quebra e tamanho. A interface continua usando a identidade cadastrada.
10. O painel de retenção chamava de “Plano” os números de uso atual. Uso e limites configurados agora aparecem separados.

## Cobertura do frontend existente

Inspeção de rotas, componentes e pontos de entrada; não equivale a homologar novamente cada integração externa.

| Grupo | Onde continua disponível | Resultado desta revisão |
|---|---|---|
| Painel e indicadores | Painel | API real preservada; não há indicadores aleatórios |
| Minhas, Não atribuídas, Todas | Conversas | Conferidas com dados persistentes nas três abas |
| Busca, filtros e ordenação | Fila | Preservados; estado, responsável, setor e horário agora visíveis |
| Paginação | Fila | Controles e cursor existentes preservados |
| Protocolo, ciclos e histórico | Cabeçalho / mensagens / contexto | Preservados |
| Assumir, transferir atendente/setor, devolver | Detalhes / responsável e setor | Mesmos componentes e operações versionadas |
| Encerrar com motivo e reabrir | Cabeçalho / atendimento encerrado | Mesmos componentes e validações |
| Presença, ocupação e capacidade | Barra lateral / Configurações | Estados reais; sem simular operadores online |
| Colisão e digitação simultânea | Contexto do atendimento | Assinatura existente preservada |
| Prioridade, etiquetas, notas e não lidas | Detalhes | Preservados e preenchidos com exemplos |
| Rascunho, respostas rápidas e formatação | Compositor / seção expansível | Preservados; sete respostas rápidas de demonstração |
| Citação e recibos | Mensagens / compositor | Preservados; exemplos históricos não afirmam entrega real |
| Mídia, áudio, anexos e download | Compositor / Contato, canal e anexos | Componentes preservados; PDF registrado e baixado de verdade |
| Janela do canal, templates e consentimento | Contato, canal e anexos | Preservados; não há Meta real conectado nesta demo |
| Documento e histórico do contato | Contexto / Contatos | Componentes e permissões preservados |
| ERP e painel externo | Cadastro e integrações | Entradas preservadas; indisponibilidade não foi escondida |
| Usuários, papéis, convites e sessões | Configurações / Usuários | Preservados; SMTP real permanece uma dependência |
| Setores e vínculos | Equipes | Preservados; três setores demonstrativos |
| Distribuição e automação | Configurações | Preservados, sem ligar regras para movimentar a demonstração artificialmente |
| Horário, plantão e bot externo | Configurações / automação | Preservados; nenhum bot externo foi acionado |
| Sons e Notification API | Configurações | Preferências preservadas; permissão depende do navegador |
| Relatórios, CSV e mapa de calor | Relatórios | Preservados; não é uma prova de capacidade de produção |
| Identidade, auditoria e alertas | Configurações | Preservados; o conector de demo desconectado pode gerar alerta verdadeiro |
| API pública e webhooks | Configurações / Integrações | Preservados; nenhum token externo foi criado |
| Retenção, exportação e privacidade | Configurações | Preservados; expurgo automático permanece desligado |
| Administração de provedores | Configurações / papel de plataforma | Rota e autorização existentes preservadas |
| Segurança, MFA e recuperação | Conta / autenticação | Preservadas; não foram relaxadas para montar a demonstração |

Não encontrei um módulo inteiro existente que tenha sido removido pelo redesign. Encontrei, e corrigi, perdas de apresentação e informação, além dos textos antigos de protótipo descritos acima.

## O que ainda falta ou não foi homologado

A matriz vigente do repositório contém 257 linhas: 206 marcadas como implementadas e verificadas, 32 aguardando homologação externa e 19 parciais. Essa é a classificação existente nos documentos; esta rodada não reexecutou a homologação dos 257 itens nem os promoveu de estado.

- **ERP real:** adaptador de fornecedor ainda incompleto/stub, incluindo as rotinas dependentes da API real, verificação do cliente e aprovação de ação sensível. A existência do painel/fake não comprova essas capacidades.
- **Evolution:** contrato/entrada de interface permanecem; não há conector operacional homologado nem QR funcional. Não foi ligado.
- **Retenção automática:** há política e recusa segura; o executor de expurgo não está implementado. Não é correto declarar concluída a limpeza automática por haver um formulário.
- **Capacidade:** existe ensaio sintético, mas faltam perfil e metas do piloto. Não foi declarada capacidade de produção.
- **Homologações:** Meta, SMTP/convites reais, ERP e serviços definitivos de produção dependem das decisões, credenciais e ambiente correspondentes. Backups locais existentes não substituem o exercício no ambiente definitivo.
- **Prévia da última mensagem na fila:** o DTO atual fornece horário, estado e dados da conversa, mas não o texto da última mensagem. Diferentemente do Chatwoot, esse resumo ainda não é exibido. Não foi inventado um texto de prévia.
- **Nome individual persistente do atendente:** o diretório atual fornece o e-mail como rótulo; o nome local da aba não é um cadastro central de nome completo. Esta limitação de apresentação permanece explícita.

## Verificação e publicação

- As três abas foram abertas no navegador nos dois produtos. No ChatISP: 9 / 9 / 36, sem erros JavaScript na jornada.
- Três PDFs foram baixados no navegador e os tamanhos conferidos.
- Conferidos navegação de Configurações, seleção de atendimento, preservação das abas visíveis e largura móvel de 390px sem rolagem horizontal.
- A verificação ampliada detectou seletores ambíguos da caixa e expectativas antigas de texto; foram corrigidos mantendo as verificações de isolamento, revogação, histórico e envio. Os 53 testes dos arquivos afetados passaram; os oito testes finais de fila/anotações também passaram.
- TypeScript e build Vite finais aprovados. Não foi afirmado que a primeira execução ampliada passou sem falhas.
- Publicação exclusiva do frontend Caddy; API, worker, PostgreSQL, Garage e demais projetos permaneceram ativos.
- Backup privado do banco do ChatISP anterior à carga preservado no servidor. Nenhum registro preexistente foi apagado. A carga usa identificadores determinísticos e evita duplicação.
- Todos os registros e conectores desta demonstração são fictícios. Conector ChatISP desativado e desconectado; Chatwoot usa canais API sem destino externo. Nenhum envio real, cobrança, ação de rede ou ERP foi feito.
