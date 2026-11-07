# ChatISP — interface de atendimento implementada

Data: 07/11/2026. Base: `8cf84edaccf5f4829d1b76f82510f33357983239`.
Branch: `redesign/atendimento-20261107`.
Fonte canônica: `/home/enok/Documentos/projetos/chatisp` no NexoISP.

## Resultado

Redesenho do frontend existente, aplicado em https://chatisp.tokstiloesporte.com.
A inspiração do Chatwoot é a hierarquia da operação: fila à esquerda, mensagens no centro e contexto à direita. As implementações, permissões e contratos continuam sendo do ChatISP.

- Navegação, marca e presença do atendente em uma barra lateral compacta.
- Caixa selecionada e fila com Minhas / Não atribuídas / Todas; busca, filtros completos, ordenação, alertas e paginação preservados.
- Histórico com balões para entrada e saída, eventos separados, mídia, citações, recibos e estados de envio preservados. A direção visual usa o campo direction do DTO, nunca o nome do autor nem uma suposição baseada no estado local.
- Atribuição, transferência de setor/atendente, devolução à fila, adiamento, prioridade, tags e notas agrupados no contexto.
- Documentos do contato, janela do canal, templates, consentimento, anexos, ERP, painel externo e histórico do contato continuam acessíveis em seções expansíveis.
- Compositor com rascunho, respostas rápidas, anexos, gravação, formatação, citação e estados de incerteza existentes. Nenhum recurso foi substituído por botão fictício.
- No celular: fila e conversa alternam sem desmontar o compositor; detalhes e navegação abrem sob demanda. O título da página dá lugar à conversa e o compositor permanece dentro da altura disponível.
- Painel com os oito indicadores reais da API. Login com a mesma autenticação e uma apresentação coerente com a central.

A política de acesso, limites, integração com provedores, banco e trabalhadores não foi alterada. Nenhuma conexão externa foi habilitada.

## Evidências

1. TypeScript + build Vite aprovados.
2. ESLint dos componentes alterados aprovado.
3. 89 testes aprovados em 20 arquivos: atendimento, compositor, mídia, fila, presença, relatórios, layout e marca. Log em `verificacao-frontend.log`.
4. Jornada de navegador Chromium real usando a fixture M4A existente, com API Go e PostgreSQL descartáveis. Mensagens sintéticas identificadas como SIMULAÇÃO; nenhuma rota da API foi interceptada ou substituída por mock nessa jornada.
5. Na bancada: seleção de caixa/conversa, assumir, enviar mensagem pelo fluxo real, filtros por atendente, rascunho preservado ao alternar fila/conversa no celular, detalhes e menu móvel. O envio foi aceito pelo fluxo local; isso não comprova entrega WhatsApp real.
6. Desktop 1536x1000 e celular 390x844 sem rolagem horizontal; no celular a base do compositor ficou em 844px. Sem erros JavaScript na jornada (`navegador.json`).

`atendimento-desktop.png`, `atendimento-mobile.png`, `fila-mobile.png` e `detalhes-mobile.png` são capturas da bancada sintética. Não são dados da instalação operacional nem prova de homologação Meta/ERP.

## Publicação e reversão

Foi reconstruída somente a imagem `chatisp-pronto-hoje:edge`, e somente o serviço Caddy dessa instalação foi recriado, com `--no-deps --no-build`. API, worker, PostgreSQL, Garage e os demais projetos permaneceram ativos.

A imagem anterior está preservada em `chatisp-pronto-hoje:edge-before-redesign-20261107`.
Para reverter exclusivamente o frontend:

```sh
docker image tag chatisp-pronto-hoje:edge-before-redesign-20261107 chatisp-pronto-hoje:edge
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-deps --no-build --force-recreate caddy
```

A conta administrativa consultada na instalação pública não possui caixa disponível. A interface apresenta esse estado e a entrada para configurar caixas; não foram inseridas conversas fictícias no banco operacional para preencher a tela.

## Limites

Não é uma nova homologação do produto inteiro. Meta, ERP, Evolution, notificações de navegador, gravação com microfone físico e dispositivos móveis físicos não foram homologados nesta alteração de apresentação. Os controles existentes foram preservados; integrações ausentes continuam ausentes. O aviso pré-existente de tamanho do bundle Vite permanece e não impede o build.

## Reproduzir a jornada isolada

No servidor NexoISP, com as dependências de desenvolvimento instaladas, use três terminais. Não aponte a fixture para banco existente.

Terminal 1:

```sh
export PATH=/home/enok/.local/share/go1.27.3/bin:/home/enok/.local/share/node24/bin:$PATH
cd /home/enok/Documentos/projetos/chatisp
mkdir -p /home/enok/.local/share/chatisp/redesign-20261107
umask 077
go -C backend build -mod=vendor -o /home/enok/.local/share/chatisp/redesign-20261107/fixture ./cmd/m2demo
cd backend
CHATISP_E2E_API_PORT=28080 /home/enok/.local/share/chatisp/redesign-20261107/fixture m4a > /home/enok/.local/share/chatisp/redesign-20261107/fixture.json
```

Terminal 2: no diretório `web`, execute `CHATISP_E2E_API_PORT=28080 npm run dev -- --host 127.0.0.1 --port 5173 --strictPort` usando Node 24.

Terminal 3: após a fixture escrever o JSON, na raiz do projeto execute `python3 docs/design/redesign-20261107/popular-bancada.py` e `node docs/design/redesign-20261107/verificar-ui.mjs`. O Chromium usado está fixado no script; ajuste apenas o caminho do executável caso a revisão local seja diferente. As portas 28080, 9090, 18081, 5173 e 5174 precisam estar livres; o programa recusa conflito em vez de parar serviços existentes.

Ao terminar, Ctrl+C nos terminais 1 e 2. A fixture remove seu PostgreSQL descartável. Remova o JSON de credenciais efêmeras. Nesta execução a bancada foi encerrada após as capturas; não permanece nenhum agente ou fila trabalhando.
