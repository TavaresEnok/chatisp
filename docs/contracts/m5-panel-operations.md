# Painel — documentos, presença e distribuição

Usa o cliente OpenAPI gerado. Código e APIs do recorte anterior preservados.

- Documento do contato na conversa e no diretório: máscara fixa por tipo;
  versão da API em definir/limpar; controles de escrita/revelação só para
  admin/supervisor. O servidor continua sendo a autoridade. Revelação explícita
  fica somente no estado transitório do componente por até 15s, oculta na aba
  escondida/troca de contato/sessão e nunca vai para Query Cache/storage/log.
  Respostas atrasadas são descartadas por AbortSignal. Erro remoto não é ecoado.
- Busca exata por POST na área Contatos, corpo com documento e campo protegido;
  sem documento em URL, histórico de navegação ou cache. O resultado abre o
  contato pela consulta autorizada existente. Nenhum identificador autentica
  cliente nem dispara consulta a ERP.
- `OperatorPresence` no `AppShell`: todas as páginas autenticadas, independente
  de caixa. UUID por montagem de efeito/aba, heartbeat imediato e a cada 10s
  (TTL servidor 30s), sem requisições sobrepostas. Logout/revogação/troca de
  escopo aborta o sinal e o intervalo; fechar/desmontar só fecha sua aba. Em
  restauração bfcache a página recarrega para restabelecer sessão/heartbeat.
  Estado manual vem da API e é compartilhado pelo servidor entre abas.
- Ocupação própria vem de `GET /api/panel/presence/occupancy`. Parâmetro opcional
  `sector_id` exige admin/supervisor. A consulta é tenant, revalida principal
  e devolve `Cache-Control: no-store`, lista de membership/ocupadas/capacidade.
  Mesma regra da reserva: open/waiting_customer atribuídas, padrão 20.
  A leitura não reserva vaga. Sem setor, atendente consulta só a própria.
- Configurações mostra fila, presença e vagas do setor e flag de atribuição;
  admin liga/desliga com versão da API. Supervisor consulta. Nomes vêm do
  diretório operacional paginado já autorizado para a caixa; não há leitura
  global de usuários. O formulário de capacidade existente passa a selecionar
  caixa/atendente pelo nome, conservando o endpoint de capacidade existente.

Vitest `m5ops`: heartbeat fora de Conversas, intervalo/parada na perda de
sessão, duas abas distintas/fechamento individual, troca manual, máscara,
revelação negada, atendente sem controles, resposta atrasada descartada e
ocultação após 15s. TypeScript verifica contratos gerados. API PG prova
ocupação própria, acesso de admin ao setor, recusa de atendente ao setor e
sessão obrigatória.

Não declara M5 completo. Separação de abertas/esperando/adiadas na ocupação,
configuração visual de menu/inatividade e homologação por operador seguem
lacunas do produto; o indicador entregue mostra o total que consome vagas.
