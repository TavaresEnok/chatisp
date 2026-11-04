# Continuidade no NexoISP

## Base preservada

Inspeção em 03/11/2026 (America/Fortaleza): repositório principal e worktree
`task-5160ece54301-m5c-runtime-wiring` limpos, ambos em `d7178d5`.
Plano `5160ece54301` pausado: 22 tarefas `done`, runtime M5C `paused`, painel
M5 `queued`. A entrega `2757deb` já é ancestral da base. Não houve mudanças
locais a resgatar. Branch de trabalho: `continuation/m5-runtime-panel`.

Consultados README, especificação V4.1, plano, contexto histórico, contratos
M3C/M5B/M5C e estado do controlador. O contexto histórico contém estados
antigos; o estado consultado do plano prevalece. AGENTS herdado contém o
fluxo de gerência do Paseo; a instrução atual do proprietário autoriza esta
implementação direta e proíbe retomar esse fluxo.

Agentes consultados tanto no daemon padrão quanto em
`paseo --home /home/enok/srv_projetos`: nenhum executor ativo; executor M5C
`5b711950-9059-49b5-8cce-9a532494e714` ocioso. Seu contêiner persistente
não significa turno em execução. Nenhum agente foi retomado, nenhum estado,
banco ou política do controlador foi alterado. A worktree pausada permanece
intacta. Não usar a pasta histórica em Documentos como fonte atual.

## Implementação e evidências

Em andamento. Os commits de produto e as verificações serão registrados aqui.

## Execução e limites

Usar Go e Node fixados no README. Desenvolvimento local: `./scripts/dev.sh`,
com portas loopback livres; sessão Secure exige HTTPS para uso autenticado
no navegador. Banco dos testes é descartável. Não apontar os comandos de
migração para dados reais. Não há autorização para deploy, retomada do Paseo,
Meta/ERP/Evolution reais ou APIs pagas.

## Interrupção para transferência solicitada pelo proprietário

O proprietário pediu passar a continuidade a outra IA, priorizando desenvolvimento
e resultado funcional. Nenhuma nova implementação deve ser inferida deste registro.

### Código preservado nesta sessão

- `3140385`: composição de lifecycle/triage no processo e no HTTP; jobs River
  `scheduled` de 10s; triagem opt-in por `CHATISP_TRIAGE_INBOXES`; mensagem atual
  retira snooze; saídas automáticas ligadas à outbox com deduplicação persistida;
  revalidação de aviso obsoleto na fronteira de despacho; confirmação por
  aceitação do driver antes de armar encerramento; migração 00028; leitura
  autorizada de ocupação; OpenAPI/sqlc/TS gerados; fixture descartável composta.
- Painel implementado e preservado em commit próprio de continuidade: documento
  mascarado, definir/limpar/revelação temporária por papel; busca exata; presença
  no layout autenticado, heartbeat 10s/UUID por aba e parada por sessão;
  ocupação própria/setor e flag admin; seleção de atendente por diretório.
  Ver `docs/contracts/m5-panel-operations.md`. Ainda requer fechamento integrado.
- Ajuste de infraestrutura de teste em commit separado: porta da API da fixture
  M3B/M4A configurável por `CHATISP_E2E_API_PORT`. Alterados m3b.go, vite.config.ts
  e injeção dos cenários E2E. Essa última mudança ainda não foi validada em
  navegador; não alegar E2E aprovado.

### Evidências executadas e falhas classificadas

- `go -C backend build ./...`: passou antes do último ajuste da porta da fixture.
- `go -C backend vet ./...`: passou antes desse ajuste.
- `go -C backend test -count=1 ./...`: passou antes desse ajuste; log
  `/tmp/chat-isp-go-all.log`. Inclui testes PG dos módulos e da composição.
- Bateria focada lifecycle/triage/pipeline/platform: passou nas áreas executadas;
  lifecycle teve uma execução interrompida porque o pré-lock novo perdeu o
  comentário `lifecycle-conv` usado pelo teste para sincronizar a corrida.
  Marcador restaurado; reexecução específica e a suíte ampla posterior passaram.
- `npm --prefix web test -- m5ops`: 5 testes passaram.
- `npm --prefix web run typecheck`: passou, antes do ajuste final da porta.
- `npm --prefix web run build`: passou, com aviso de chunk >500 kB.
- ESLint focado nos arquivos novos/editados do painel: passou.
- `npm --prefix web run lint`: 34 erros/5 warnings em arquivos preexistentes
  fora do diff (refs durante render, setState em effect, variável não usada
  em E2E). Não foi declarado verde nem alterada a política de lint.
- Vitest completo: 204 testes, 199 passaram e 5 falharam. Log
  `/tmp/chat-isp-web-all.log`.
  * 2 falhas reproduzidas na base limpa `d7178d5`, em worktree temporária
    `/tmp/chat-isp-baseline-review`: `m3b-socket-transfer` (espera aviso de
    resposta perdida) e `m2-attendance` (ainda procura conversa como role option,
    enquanto a fila atual usa button). Log `/tmp/chat-isp-baseline-tests.log`.
  * 3 falhas surgiram com a montagem global da presença: duas em `area-states`
    usam queries globais de role=status/alert e agora há também status/erro
    de presença; outra em `contacts-panel` conta todos os POSTs como escrita
    de contato, incluindo o novo heartbeat. Atualizar doubles/seletores de
    forma explícita, preservando as asserções funcionais. Não esconder presença
    no produto nem desabilitar cenários para fazer teste passar.
  * Em `m3b-socket-transfer`, o double devolve `{}` para
    `/api/panel/attendant-signature`; a validação existente de getSignature exige
    enabled/prefix/version e impede envio sem configuração válida. Investigar
    primeiro esse contrato do double, sem remover a proteção do compositor.
- Chromium real 140.0.7339.186 abriu. M3B não iniciou o fluxo funcional porque
  `8080` está ocupada; tentativa seguinte em `18080` também encontrou porta
  ocupada. Não houve aceite E2E novo. A correção agora permite escolher porta
  livre, sem parar serviços existentes. M4A não foi executado nesta sessão.
- Não há comandos de teste ativos desta sessão na conferência final. A worktree
  pausada do Paseo continua limpa em `d7178d5`; Paseo/controlador não alterados.

### Lacunas reais confrontadas com a especificação

Estas lacunas são desenvolvimento restante, não simplesmente falta de testes:

- M3: regra persistida de auto-reabertura pela janela X e composição no ingresso.
  Há regra de domínio em conversation/state.go, mas Open/AcceptInbound não a
  compõem no fluxo persistido. Reabertura manual não equivale a essa entrega.
- M4B: pipeline de mídia recebida/saída, citação, gravação MediaRecorder e
  conversão limitada por ffmpeg. Storage/anexo seguro existente não entrega
  esse pipeline. `ffmpeg` não foi encontrado no PATH deste host nesta inspeção.
- M5: gancho externo de bot, tokens/epoch/handoff/deadline e interruptor;
  desatribuição efetiva de offline/inativo (presence expõe sinal, não executor);
  configuração visual de menu/snooze/inatividade e indicação de suas pendências;
  ocupação separando abertas/espera/adiadas. O motivo do menu é guardado no
  claim/decisão, ainda não integra um modelo geral de classificação/relatórios.
- M6: API pública de integração, webhooks de saída, contrato/fake do ERP,
  contexto externo/iframe e conector real condicionado aos contratos externos.
  Nenhum diretório integration/erp implementado foi encontrado em backend/internal.
  Evolution segue condicionada e desligada; não inventar endpoints.
- M7: relatórios/CSV com denominadores e união de intervalos, exportação/
  retenção/exclusão, agregados de uso e gestão completa. Não existe módulo
  report/retention no backend atual. Telas básicas não equivalem a essas funções.
- M8: restauração banco/anexos/chaves, alertas com destino, perfil de carga e
  metas/RPO/RTO, segurança e homologação humana. Não houve deploy nem dados reais.
- Dependências externas: credenciais/autorização Meta, contrato ERP e mecanismo
  de verificação de cliente, versão/licença/aceite Evolution, retenção aplicável
  e perfil de piloto. Separar essas dependências dos módulos implementáveis.

### Próxima execução recomendada

1. Conferir a branch de continuidade e commits recentes, não recomeçar do plano
   antigo. Ler os dois contratos novos e este registro. Preservar worktrees.
2. Fechar o bloco funcional M5 iniciado: revisar composição e uso no painel,
   corrigir defeitos concretos e completar os controles operacionais necessários.
3. Tratar em bloco as falhas delimitadas acima; checks somente nas áreas tocadas.
   Não repetir build/baterias verdes sem mudança que justifique.
4. Na próxima validação de navegador, escolher uma porta API loopback realmente
   livre e executar com `CHATISP_E2E_API_PORT=PORTA`. O restante da fixture ainda
   usa 9090/18081/5173/5174 e também precisa estar livre. Não parar serviços do host.
5. Prosseguir com os próximos blocos da especificação. Não gastar a continuidade
   só em auditoria, relatório ou aumentar testes enquanto faltar implementação.
6. Fazer o marco integrado no fechamento do bloco e homologação humana depois.
   Não marcar as tarefas do Paseo como done via edição de banco/controlador.

A worktree temporária de comparação em `/tmp/chat-isp-baseline-review` foi criada
por esta sessão, em detached HEAD, sem alterações rastreadas; contém symlink de
node_modules para o repositório atual. Pode ser removida quando não for útil.
Logs em /tmp são evidência temporária; este documento guarda o resultado durável.


## Continuação em 04/11/2026 (branch `continuation/m5-runtime-panel`)

### Implementado e commitado
- `4b88353` testes web: doubles/seletores alinhados à presença global, à fila
  `/queue` e à assinatura. Vitest completo passou (204, depois 208 com os novos).
- `933404a` M3 auto-reabertura: migração 00029 (`inboxes.reopen_window_hours`,
  0 = desligada, máx. 720), `service.ReopenWithinInboxWindow` composta no
  `ApplyInbound` (mesmo protocolo, novo ciclo, menu, fila); fora da janela ou
  desligada abre novo atendimento. `GET/PUT /api/panel/inboxes/{id}/reopen-window`
  (admin edita, supervisor lê, auditado). Provas PG: reabre, replay idempotente,
  janela desligada/expirada.
- `6426a4a` painel: seção "Automação do atendimento" (prazos de inatividade,
  tolerância offline, janela X e editor do menu de triagem por caixa) e botão
  "Adiar" na conversa. Usa APIs M5C que não tinham tela.
- `a294763` M5: migração 00030 e `pipeline.UnassignOffline` no
  tick de 10 s — devolve à fila conversas `open` de atendente offline além da
  tolerância (evento `offline_unassigned`, sem ator); `waiting_customer` mantém
  o dono por causa do relógio de inatividade. Ocupação separa abertas /
  aguardando / adiadas (adiadas não consomem vaga). Linha do tempo do painel com
  rótulos em português.

### Verificações executadas
- E2E Chromium 140 com `CHATISP_E2E_API_PORT=28080`: M3B e M4A passaram
  (evidência fresca; portas 8080/18080 do host ocupadas e preservadas).
- `go vet ./...` e `go test -count=1 ./...` completos: passaram.
- `tsc -b` e Vitest completo (208): passaram. ESLint global segue com a dívida
  preexistente já descrita (não alterado).
- Não houve E2E novo cobrindo reabertura/adiamento/desatribuição no navegador.

### Lacunas restantes
M4B (mídia/áudio; sem ffmpeg no host), M5 gancho de bot externo, M6, M7, M8 e
dependências externas conforme a seção de lacunas acima. Próximo bloco sugerido:
M7 (relatórios e CSV a partir de eventos/intervalos persistidos) ou M6 contrato/fake
do ERP e API pública; confirmar prioridade com o proprietário.

## Relatórios M7, nesta sessão (depois de `a294763`)

### Implementado
Relatórios V1 a partir de ciclos, eventos, mensagens, fila e adiamentos já
persistidos. `GET /api/panel/reports` (datas civis no fuso do provedor, agrupamento
dia/semana/mês). A média e a mediana ignoram o que não se aplica. Sem resposta
humana o tempo não vira zero. O operacional desconta uma vez a união de espera do
cliente e adiamento. Setor e atendente usam o intervalo real; transferência reparte.
Motivo principal é único; etiqueta adicional se sobrepõe. Encerramento pelo bot fica
em linha própria. O CSV repete esses números e diz que não é o histórico do ERP.
O quadro “agora” alimenta o Painel: fila, atendimento, espera, adiadas, sem
responsável, presença vigente, maior espera e alerta com o `wait_alert_seconds` já
gravado (urgente não entra). Atendente não lê outro setor: pedido explícito responde
403. Contrato em `docs/contracts/m7-reports.md`.

### Verificações executadas
- `go test -count=1 ./internal/reports/`: passou. Inclui 8h–12h com 3h de espera
  (4h e 1h), sobreposição descontada uma vez, bot sem resposta humana, leitura
  PostgreSQL desses dois casos e isolamento do atendente de outro setor.
- `go vet ./internal/reports/ ./internal/platform/httpapi/`: passou.
- `tsc -b` e Vitest de `attendance-report` mais `panel.test`: passaram (14).
- ESLint só nos arquivos web deste bloco: passou. O ESLint global não foi
  alterado e não foi reexecutado.
- Não rodei `go test ./...`, Vitest completo nem E2E.

### Lacunas que permanecem
M4B (sem ffmpeg no host), gancho de bot externo, M6 (API pública, webhooks e
contrato/fake do ERP), o resto do M7 (retenção, expurgo — continua desligado —,
exportação e exclusão de contato, histórico de demandas, logo, limites de plano) e
M8. Sem chamadas reais a Meta, ERP ou Evolution.
