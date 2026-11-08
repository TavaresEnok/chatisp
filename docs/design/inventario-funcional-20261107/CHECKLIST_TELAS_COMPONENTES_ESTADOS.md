# Checklist de telas, componentes e estados para o design

Referência: `CHATISP_INVENTARIO_COMPLETO_PARA_DESIGN.md`. Cada linha descreve uma unidade de conferência; não é uma nova tela obrigatória independente. Pode agrupar itens, mantendo os recursos e estados.

Quantidade: **142 itens**. Não confundir esta checklist de UX com os 257 requisitos técnicos/funcionais da matriz original.

| ID | Área | Tela/componente | Cobertura mínima | Situação | Papel/condição | Capítulo |
|---|---|---|---|---|---|---|
| UX-001 | Conta | Login | E-mail/senha; enviando; erro genérico; bloqueio; acesso aceito | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-002 | Conta | Opções avançadas de acesso | Identificador do provedor; código MFA ou de recuperação; vazio/inválido | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-003 | Conta | Aceite de convite | Válido; inválido; usado; expirado; salvar credenciais | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-004 | Conta | Recuperação de senha | Solicitar; envio indisponível; token inválido/usado; confirmação | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-005 | Conta | Cadastro MFA | QR local; segredo; códigos de recuperação; confirmar; erro; já ativo | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-006 | Conta | Fim de sessão | Sair; expirou; sessão revogada; MFA pendente | Implementado; e-mail real depende de SMTP | Conforme etapa de autenticação | 25 |
| UX-007 | Estrutura global | Navegação desktop | Página atual; provedor; usuário/aba; sair | Implementado | Todos os papéis no recorte permitido | 7 |
| UX-008 | Estrutura global | Menu móvel | Abrir; fechar; foco; destino acessível | Implementado | Todos os papéis no recorte permitido | 7 |
| UX-009 | Estrutura global | Identidade do provedor | Nome; logo; fallback; nome longo | Implementado | Todos os papéis no recorte permitido | 7 |
| UX-010 | Estrutura global | Página não encontrada | Explicação; voltar ao painel | Implementado | Todos os papéis no recorte permitido | 7 |
| UX-011 | Estrutura global | Troca de contexto | Conta/provedor/papel alterado; conteúdo antigo removido | Implementado | Todos os papéis no recorte permitido | 7 |
| UX-012 | Painel | Indicadores de atendimento | Fila; atendimento; aguardando; adiadas; sem responsável; online; maior espera; alertas | Implementado; métricas fornecidas pelo servidor | Recorte autorizado pela API | 8 |
| UX-013 | Painel | Estado dos indicadores | Zero válido; sem amostra; carregando; erro; acesso negado | Implementado; métricas fornecidas pelo servidor | Recorte autorizado pela API | 8 |
| UX-014 | Fila | Caixa de atendimento | Uma caixa; várias; nenhuma; desconectada/degradada; acesso retirado | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-015 | Fila | Abas da fila | Minhas; Não atribuídas; Todas; contagens reais; sobreposição dos totais | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-016 | Fila | Cartão do atendimento | Contato; iniciais; protocolo; estado; setor; responsável; horário; prioridade; não lida; espera | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-017 | Fila | Pesquisa | Texto; limpar; carregando; resultado vazio; erro | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-018 | Fila | Filtros | Estado; setor; atendente; caixa; etiqueta; período; limpar | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-019 | Fila | Ordenação e paginação | Espera; última mensagem; primeira/próxima página; cursor | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-020 | Fila | Navegação da fila | Seleção; teclado; rolagem de resultados sem perder controles | Implementado | Recorte autorizado por caixa/setor | 9 |
| UX-021 | Conversa | Cabeçalho | Contato; protocolo; estado; responsável; assumir/encerrar | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-022 | Conversa | Histórico de texto | Entrada; saída; autor; data/hora; mensagem longa; histórico vazio | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-023 | Conversa | Eventos no histórico | Assumida; encerrada; reaberta; espera; transferência; evento não interpretado | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-024 | Conversa | Citação | Responder; prévia citada; cancelar; origem não disponível | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-025 | Conversa | Reações recebidas | Lista de reações existente; sem comando geral de reação de saída | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-026 | Conversa | Carregamento do histórico | Pendente; falha; retornar às recentes; histórico virtualizado | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-027 | Conversa | Estado da conversa | Triagem; aberta; aguardando cliente; encerrada | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-028 | Conversa | Visão móvel | Fila; conversa; voltar preservando rascunho; abrir/fechar detalhes | Implementado | Acesso à conversa revalidado pelo servidor | 10 |
| UX-029 | Compositor | Mensagem e rascunho | Vazio; digitando; guardado no dispositivo; troca de conversa/usuário | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-030 | Compositor | Envio | Disponível; em andamento; bloqueado; conflito; duplicidade impedida | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-031 | Compositor | Resultado de envio | Em fila; enviando; aceito; falhou; repetindo; incerto; cancelado | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-032 | Compositor | Resposta incerta | Repetir mesma intenção; nova mensagem como decisão explícita | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-033 | Compositor | Formatação | Negrito; itálico; emojis de texto; sem editor rico arbitrário | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-034 | Compositor | Respostas por barra | Abrir /; buscar; escolher; teclado; variável ausente; remover prefixo repetido | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-035 | Compositor | Arquivo para envio | Selecionar; arrastar; colar; remover; legenda; tipo recusado | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-036 | Compositor | Gravação de áudio | Permissão; gravando; parar; enviar; descartar; erro/conversão; sem pré-escuta separada atual | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-037 | Compositor | Atendimento encerrado | Compositor indisponível; reabrir quando permitido | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-038 | Compositor | Recibos | Aceite local/conector distintos de entregue e lido | Implementado; despacho depende de canal e política | Autor autorizado e posse válida | 11 |
| UX-039 | Mídia e anexos | Imagem/figurinha | Miniatura; abrir; fechar; falha; indisponível | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-040 | Mídia e anexos | Documento | Nome; tipo; download autorizado; falha/expiração | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-041 | Mídia e anexos | Áudio/nota de voz recebido | Player; carregando; erro; mídia ausente | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-042 | Mídia e anexos | Vídeo | Player quando o tipo e a política permitirem; bloqueio/falha | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-043 | Mídia e anexos | Localização recebida | Coordenadas; endereço; link de mapa; sem mapa embutido presumido | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-044 | Mídia e anexos | Contato recebido | Dados recebidos; identificação de cartão; não cadastro ERP automático | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-045 | Mídia e anexos | Resposta interativa recebida | Título/identificador/resposta conforme payload; conteúdo não interpretado | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-046 | Mídia e anexos | Registro de anexo | Selecionar; subir; validar; progresso; tentar de novo; erro/quota | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-047 | Mídia e anexos | Consulta técnica de anexo | Identificador; encontrado; inexistente; download temporário | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-048 | Mídia e anexos | Registros incompletos | Limpeza autorizada de vencidos; não exclusão geral de documentos | Condicionado a arquivo validado, armazenamento e capacidade do canal | Acesso atual à conversa | 12 |
| UX-049 | Notas e classificação | Notas internas | Ler; escrever; autor autorizado; salvar; erro; resposta incerta | Implementado | Prioridade: admin/supervisor; demais ações conforme conversa | 13 |
| UX-050 | Notas e classificação | Prioridade | Normal; urgente; sem editor para atendente comum | Implementado | Prioridade: admin/supervisor; demais ações conforme conversa | 13 |
| UX-051 | Notas e classificação | Etiquetas | Adicionar existente; remover; sem catálogo completo de criação na UI | Implementado | Prioridade: admin/supervisor; demais ações conforme conversa | 13 |
| UX-052 | Notas e classificação | Leitura | Marcar lida; não lida manual; quantidade de novas mensagens | Implementado | Prioridade: admin/supervisor; demais ações conforme conversa | 13 |
| UX-053 | Gestão do atendimento | Assumir | Disponível; assumindo; já alterada; capacidade cheia; sem permissão | Implementado | Conforme posse, vínculos e capacidade | 14 |
| UX-054 | Gestão do atendimento | Transferir atendente | Candidatos; ocupação; sem vaga; confirmar; cancelar; resposta incerta | Implementado | Conforme posse, vínculos e capacidade | 14 |
| UX-055 | Gestão do atendimento | Transferir setor | Destinos autorizados; confirmar; acesso retirado após mover | Implementado | Conforme posse, vínculos e capacidade | 14 |
| UX-056 | Gestão do atendimento | Devolver à fila | Confirmar; cancelar; erro; nova versão | Implementado | Conforme posse, vínculos e capacidade | 14 |
| UX-057 | Gestão do atendimento | Colaboração | Outro atendente visualizando; digitando; ausência de dados | Implementado | Conforme posse, vínculos e capacidade | 14 |
| UX-058 | Ciclo do atendimento | Encerrar | Motivo; resultado; síntese; despedida opcional; validação | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-059 | Ciclo do atendimento | Resultado de encerramento | Resolvido; sem resposta; encaminhado; spam | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-060 | Ciclo do atendimento | Encerramento em andamento | Confirmado; incerto; falha de despedida sem desfazer fechamento | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-061 | Ciclo do atendimento | Reabrir | Encerrada; reabertura manual autorizada; conflito | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-062 | Ciclo do atendimento | Reabertura por mensagem | Mesmo protocolo/novo ciclo na janela; atendimento novo fora dela | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-063 | Ciclo do atendimento | Adiar | Motivo; data/hora futura; 1h; 4h; amanhã 9h | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-064 | Ciclo do atendimento | Adiamento ativo | Prazo; liberação de vaga; retorno; resposta do cliente | Implementado; automações dependem do runtime e canal | Conforme autorização operacional | 15 |
| UX-065 | Contatos | Diretório | Lista; pesquisa; paginação; contato bloqueado; vazio/erro | Implementado | Visibilidade de contato autorizada | 16 |
| UX-066 | Contatos | Cadastro e edição | Nome; telefone internacional; e-mail; observações; validação | Implementado | Visibilidade de contato autorizada | 16 |
| UX-067 | Contatos | Bloquear/desbloquear | Motivo; confirmar operação; falha | Implementado | Visibilidade de contato autorizada | 16 |
| UX-068 | Contatos | Histórico do contato | Protocolos/ciclos autorizados; vazio; erro; acesso perdido | Implementado | Visibilidade de contato autorizada | 16 |
| UX-069 | Contatos | Documento mascarado | CPF/CNPJ presente; ausente; falha; sem permissão | Implementado | Visibilidade de contato autorizada | 16 |
| UX-070 | Contatos | Documento protegido | Definir; limpar; revelar 15s; ocultar; validação/conflito; admin/supervisor | Implementado | Visibilidade de contato autorizada | 16 |
| UX-071 | Contatos | Busca exata por documento | Campo protegido; encontrado; não encontrado ou sem acesso | Implementado | Visibilidade de contato autorizada | 16 |
| UX-072 | Equipes e usuários | Lista de setores | Ativos; inativos; seleção; erro/vazio | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-073 | Equipes e usuários | Editar setor | Criar; renomear; ativar/desativar | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-074 | Equipes e usuários | Vínculos | Membros; caixas; adicionar/remover; caixa/setor padrão | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-075 | Equipes e usuários | Membros do provedor | Papel; convidado/ativo/desativado; desativar/reativar | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-076 | Equipes e usuários | Convite | E-mail; papel; pedido aceito/recusado; serviço indisponível | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-077 | Equipes e usuários | Sessões | Listar; revogar; própria sessão; sessão inválida | Implementado; convites reais exigem e-mail configurado | Admin edita; demais papéis no alcance permitido | 17 |
| UX-078 | Presença e distribuição | Minha presença | Disponível; ocupado; conectado; verificando; indisponível | Implementado | Operador controla sua presença; gestão conforme papel | 18 |
| UX-079 | Presença e distribuição | Minha ocupação | Vagas usadas/capacidade; abertas; aguardando; adiadas | Implementado | Operador controla sua presença; gestão conforme papel | 18 |
| UX-080 | Presença e distribuição | Capacidade por membro | Caixa; membro; valor; salvar; erro/conflito | Implementado | Operador controla sua presença; gestão conforme papel | 18 |
| UX-081 | Presença e distribuição | Distribuição por setor | Caixa/setor; fila; espera excedida; ocupação e presença | Implementado | Operador controla sua presença; gestão conforme papel | 18 |
| UX-082 | Presença e distribuição | Atribuição automática | Ligada/desligada; comando admin; sem elegível; erro | Implementado | Operador controla sua presença; gestão conforme papel | 18 |
| UX-083 | Automação e horários | Expediente | Caixa; fuso; dia; início/fim; adicionar/remover intervalo | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-084 | Automação e horários | Mensagens de horário | Boas-vindas; fora do expediente; ninguém disponível | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-085 | Automação e horários | Plantão | Setor permitido; sem plantão; salvar/retirar configuração | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-086 | Automação e horários | Consultar decisão de horário | Instante; aberto/fechado; próxima abertura; destino autorizado | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-087 | Automação e horários | Inatividade | Aviso; prazo após aviso; padrão; confirmação do canal; salvar/conflito | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-088 | Automação e horários | Tolerância offline | Minutos; salvar; erro/conflito | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-089 | Automação e horários | Janela X | Caixa; horas/desligada; ler/salvar; erro | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-090 | Automação e horários | Triagem | Opções; chave; rótulo; destino humano/setor; motivo; tentativas | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-091 | Automação e horários | Habilitação do menu | Salvo não implica runtime habilitado; vazio/indisponível | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-092 | Automação e horários | Avisos de bot | Sem pendências; aguardando; incerto; sem plataforma de IA completa | Implementado/condicionado; bot permanece parcial | Admin grava; supervisor consulta nos editores examinados | 19 |
| UX-093 | Respostas e avisos | Catálogo de respostas | Busca; paginação; vazio/erro | Implementado | Atendente usa; admin/supervisor editam catálogo | 20 |
| UX-094 | Respostas e avisos | Resposta compartilhada | Atalho; título; conteúdo; criar/editar/excluir | Implementado | Atendente usa; admin/supervisor editam catálogo | 20 |
| UX-095 | Respostas e avisos | Variáveis | nome_cliente; nome_atendente; protocolo; inválida/ausente | Implementado | Atendente usa; admin/supervisor editam catálogo | 20 |
| UX-096 | Respostas e avisos | Assinatura | Ativar; prefixo; nome_atendente; salvar/conflito | Implementado | Atendente usa; admin/supervisor editam catálogo | 20 |
| UX-097 | Notificações | Preferências | Som; navegador; silencioso; salvar/erro | Implementado; depende do navegador aberto e permissão | Preferência do usuário | 21 |
| UX-098 | Notificações | Permissão | Não solicitada; concedida; negada; indisponível | Implementado; depende do navegador aberto e permissão | Preferência do usuário | 21 |
| UX-099 | Notificações | Novo evento | Nova conversa/mensagem; sem replay histórico como evento novo | Implementado; depende do navegador aberto e permissão | Preferência do usuário | 21 |
| UX-100 | Canais e política | Caixas Meta | Lista; nome/número; ativação; estado; alerta; vazio/erro | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-101 | Canais e política | Configuração manual | Nome; número; App ID/Secret; WABA; Phone ID; token; salvar/recusa | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-102 | Canais e política | Saúde de número | Limite informado; qualidade; datas; falhas por código; incertos; quarentena | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-103 | Canais e política | Ler qualidade | Pedindo; real/simulação explícita; falha; sem leitura | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-104 | Canais e política | Janela oficial | Indisponível na UI atual; erro no envio; sem countdown presumido | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-105 | Canais e política | Templates | Catálogo; idioma/estado; finalidade; enviar; erro/incerto | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-106 | Canais e política | Sincronizar templates | Comando admin; em andamento; falha; catálogo retornado | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-107 | Canais e política | Consentimento | Finalidade; origem; evidência; conceder/revogar evento; sem estado consolidado consultado | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-108 | Canais e política | Evolution | Desligado/pendente; sem QR operacional | Condicionado; Evolution parcial; integrações reais não homologadas nesta rodada | Configuração de canais: admin; envio conforme conversa | 22 |
| UX-109 | ERP e contexto externo | Vínculo ERP | Cliente/contrato; selecionar; remover; sem vínculo/permissão | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-110 | ERP e contexto externo | Identificação ERP | Referência; chave local; encontrado; não encontrado; indisponível | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-111 | ERP e contexto externo | Resumo ERP | Carregando; vazio; oculto; indisponível; simulação explícita | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-112 | ERP e contexto externo | Faturas e segunda via | Verificação; lista; Pix/documento; confirmação; oculto/incerto; adaptação real pendente | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-113 | ERP e contexto externo | Protocolos ERP | Local preservado; referência externa associada; erro/conflito | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-114 | ERP e contexto externo | Abrir cadastro externo | URL autorizada com referência; ausente/recusada | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-115 | ERP e contexto externo | Configuração de embed | Origem; URL; salvar/remover; não incorporável | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-116 | ERP e contexto externo | Painel externo | Iframe autorizado; abrir fora; indisponível; sem credenciais expostas | ERP real parcial; embed condicionado | Permissão e vínculo autorizados | 23 |
| UX-117 | Relatórios | Filtros do relatório | Início/fim; dia/semana/mês; setor; caixa; sem permissão/erro | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-118 | Relatórios | Tempos | Primeira resposta; total; operacional; fila; média/mediana/amostra; não aplicável | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-119 | Relatórios | Dimensões | Resultados; setor; atendente; caixa; motivos; etiquetas sobrepostas | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-120 | Relatórios | Mapa de calor | Dias/horas; fuso; sem dados | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-121 | Relatórios | Séries e CSV | Agrupamento; baixar; erro; recorte autorizado | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-122 | Relatórios | Uso Meta | Período; categoria; indicador de cobrança; desconhecido; sem total financeiro inventado | Implementado; valores dependem do recorte e eventos existentes | Recorte autorizado; uso Meta não exibido ao agent | 24 |
| UX-123 | Administração e identidade | Identidade do provedor | Nome; logo PNG/JPEG; salvar/remover; fallback; erro | Implementado | Conforme papel administrativo | 25 |
| UX-124 | Administração e identidade | Administração de provedores | Lista; cadastro; estado; limites/convite conforme API; suspensão | Implementado | Conforme papel administrativo | 25 |
| UX-125 | Administração e identidade | Nome local da aba | Editar; validar; salvar localmente; troca de usuário limpa preferência | Implementado | Conforme papel administrativo | 25 |
| UX-126 | Integrações técnicas | Gerar token | Escopos; limite/minuto; gerar; segredo mostrado uma vez; copiar/ocultar | Implementado; conectores externos continuam condicionados | Admin | 26 |
| UX-127 | Integrações técnicas | Lista de tokens | Identificador; escopos; limite; ativo/revogado; revogar | Implementado; conectores externos continuam condicionados | Admin | 26 |
| UX-128 | Integrações técnicas | Cadastrar webhook | URL; eventos; cadastrar; segredo de assinatura uma vez | Implementado; conectores externos continuam condicionados | Admin | 26 |
| UX-129 | Integrações técnicas | Assinaturas e entregas | Lista; estado; desativar; consultar entrega/tentativa; erro | Implementado; conectores externos continuam condicionados | Admin | 26 |
| UX-130 | Integrações técnicas | Documentação de API | Link OpenAPI; sem editor geral de integrações sem código | Implementado; conectores externos continuam condicionados | Admin | 26 |
| UX-131 | Auditoria e retenção | Auditoria | Período; data; ação; alvo; resultado; vazio/erro | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-132 | Auditoria e retenção | Alertas | Job antigo; conector desconectado; webhook pendente; limites de idade | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-133 | Auditoria e retenção | Política de retenção | Prazos por classe; não definidos; guardar; expurgo desligado | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-134 | Auditoria e retenção | Uso e limites | Pessoas; números; bytes usados; tetos configurados/ausentes | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-135 | Auditoria e retenção | Exportar demandas | Contato; protocolos/ciclos; resultado/erro | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-136 | Auditoria e retenção | Exportar titular | Contato; dados permitidos; erro | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-137 | Auditoria e retenção | Excluir titular | Confirmação digitada; em andamento; anonimização/remoção; falha parcial; protocolo preservado | Implementado em parte; expurgo automático ausente | Admin e administração autorizada | 27 |
| UX-138 | Transversal | Conexão e autorização | Conectando; reconectando; ressincronizando; revogação; provedor suspenso | Obrigatório para os recursos correspondentes | Todos, com recorte | 28 |
| UX-139 | Transversal | Concorrência e intenção | Versão alterada; repetição; resposta tardia; decisão nova | Obrigatório para os recursos correspondentes | Todos, com recorte | 28 |
| UX-140 | Transversal | Sem dados e falhas | Zero; vazio; sem amostra; não configurado; erro; sem permissão | Obrigatório para os recursos correspondentes | Todos, com recorte | 28 |
| UX-141 | Transversal | Isolamento visual | Troca de conta/tenant/papel sem histórico ou rascunho indevido | Obrigatório para os recursos correspondentes | Todos, com recorte | 28 |
| UX-142 | Transversal | Acessibilidade e celular | Foco; rótulo; contraste; teclado; largura; compositor; detalhes | Obrigatório para os recursos correspondentes | Todos, com recorte | 28 |
