# ChatISP — nova identidade visual (preto azul-grafite, menu em pílula)

Data: 15/11/2026. Base: `696dd6785f2acdba3156c308fba917d1259f980d` (`main`).
Ramo de revisão: `redesign/grafite-20261115`. Alterações enviadas no [PR 1](https://github.com/TavaresEnok/chatisp/pull/1). A instalação de produção foi preservada durante a revisão.

## O que mudou

O redesenho altera a apresentação do frontend (`web/`). A revisão também corrige os estados de aparência, os fluxos móveis e a representação dos relatórios descritos adiante. Regras de negócio, permissões, contratos, chamadas à API, WebSocket, rascunhos e estados de envio foram preservados.

- **Tokens** (`web/src/index.css`): tema escuro em preto azul-grafite (fundo `#06070A`, painéis `#0E1116`, superfícies até `#252C39`) e tema claro em branco neutro. Uma única cor viva, `--hot` (laranja), para o que pede atenção. Novos tokens: `--soft`, `--hot`, `--ok`.
- **Tema claro e escuro**: botão no topo. O escuro é o padrão. A preferência fica no dispositivo (`web/src/stores/interface-store.ts`) e é apagada junto com o restante do armazenamento ao sair da sessão.
- **Estrutura** (`web/src/components/layout/app-shell.tsx`): barra de topo com a marca do provedor, o local, a presença do atendente, o tema, o nome da aba e Sair. O menu lateral virou uma pílula de ícones que expande para mostrar os nomes.
- **Expandir o menu**: aba na borda da pílula (`nav-handle.tsx`, um SVG que redesenha as duas bordas), botão `»` abaixo da pílula e tecla `[`. A tecla não dispara durante a digitação nem com um diálogo aberto.
- **Conversas**: fila, conversa e detalhes em três painéis. O título, a caixa de atendimento e os avisos de conexão foram para o topo da coluna da fila (`conversations-page.tsx`). Balões, eventos, estados de envio e a caixa de resposta foram redesenhados por CSS. No tema claro a caixa de resposta é escura (`#252C39`).
- **Conversas refeita na estrutura** (16/11/2026): a primeira passada só trocava o CSS e ficou abaixo do desenho aprovado. Agora a marcação segue o protótipo.
  - Fila (`queue-controls.tsx`, `queue-search.tsx`, `conversations-page.tsx`): caixa de atendimento como linha discreta, contagens "sem responsável · com você" (vêm de `counts` da fila), busca com ícone, botão de filtros que abre sobre a lista (a ordenação foi para dentro dele), abas, cartões em três linhas e rodapé "Minha ocupação" (mesma leitura de vagas da presença, via `useOccupancyStore`).
  - Conversa (`conversation-header.tsx`, `message-timeline.tsx`): cabeçalho em uma linha; balões alinhados por sentido, com nome e hora acima, estado de envio abaixo e "Responder" ao passar o mouse; eventos em linha central. Os ícones do histórico são desenhados por CSS, porque o histórico não aceita `<svg>`.
  - Caixa de resposta (`message-composer.tsx`, `composer-tools.tsx`, `audio-recorder.tsx`): barra de ícones (anexo, áudio, negrito, itálico, emojis) e Enviar com `Ctrl ↵`. O campo "Responder à mensagem" do áudio só aparece quando há gravação.
  - Detalhes (`conversation-workspace.tsx`): três abas (Atendimento, Contato, Integrações) no lugar das três seções recolhíveis; fatos do atendimento, ações em lista, prioridade, tags e notas.
- **Contatos e Equipes**: lista à esquerda e ficha à direita (`contacts-page.tsx`, `teams-page.tsx`, classes `split-*`).
- **Relatórios** (`attendance-report.tsx`, `heatmap.tsx`): totais em cartões, tabela de tempos, barras por dimensão, gráfico do agrupamento e mapa de calor em quatro níveis.
- **Configurações** (`settings-page.tsx`, `settings-sections.tsx`): seções à esquerda, painéis à direita. Todos os painéis continuam na página (a lista rola até eles), para não esconder nenhum controle.
- **Texto para leitores de tela**: onde a tela mostra uma leitura compacta, a frase por extenso continua no documento (`visually-hidden` ou `sr-only`), como o quadro "Visão do atendimento" já fazia. Rótulos, nomes de botões e papéis não mudaram.
- **Avatar** (`contact-avatar.tsx`): iniciais sobre superfície neutra do tema, em vez da cor derivada do nome.

## O que não mudou, de propósito

- A fila continua sem prévia do texto da última mensagem e sem foto: o contrato não traz esses dados.
- A caixa de atendimento ainda mostra o estado técnico do canal (`ready`) como vem da API.
- Nenhum endpoint, campo, métrica ou integração foi criado.

## Bancada visual

`web/bancada.html` + `web/src/test/bancada.tsx`: página só de desenvolvimento que monta as telas reais com a API trocada por respostas sintéticas no navegador (tudo marcado como SIMULAÇÃO). Serve para conferir a aparência sem subir backend e PostgreSQL. Não entra no build (`vite build` usa só `index.html`).

```sh
cd web && npm run dev
# http://localhost:5173/bancada.html?rota=/conversas&conversa=AT-2048&tema=dark&menu=collapsed&papel=admin
```

Rotas sem resposta sintética devolvem 404, e a tela mostra o próprio estado de indisponível.

## Evidências desta alteração

Ver `VERIFICACAO.md` nesta pasta: verificação de tipos, ESLint, testes do Vitest e build, com os números da execução.

Conferência visual feita na bancada, em Chrome, nos temas escuro e claro: Conversas (fila, conversa aberta, detalhes, menu recolhido e expandido), Painel, Relatórios, Configurações, Contatos e Entrar, além de Conversas em largura de celular.

## Revisão atual — 16/11/2026

A revisão preserva o desenho aprovado. Foram corrigidos os seletores dos testes para o nome acessível “Caixa de atendimento”, sem retirar as verificações; a abertura de novo setor e o aviso de erro de contato no celular; a limpeza de tema e menu em memória ao sair; e a indicação de registros fora da amostra e a altura das barras com valor zero. Os roteiros E2E foram adaptados às abas de detalhes. A área acionável da aba do menu tem 24 px, o topo contém o texto na área disponível, as telas passaram por ajuste de reflow com altura de 256 px e a fixture de teclado inicia no corpo do documento e limpa o foco entre auditorias.

A execução atual registrou 307 testes em 64 arquivos, `make lint` e build aprovados, além de M3B, M4A, M4B–M6 e M7-OPS com API Go e PostgreSQL reais. Os fornecedores continuam sintéticos, sem homologação externa. UX passou em 52 vistas por tema, sem falhas. A mesclagem exige CI verde no cabeçalho final do PR; o resultado está nos Checks. Consulte `VERIFICACAO.md` para as evidências.

## Limites históricos da primeira execução

As observações abaixo descrevem a primeira execução e não o estado da revisão atual.

- Não houve jornada com API Go e PostgreSQL reais: esta máquina não tem PostgreSQL nem Docker. A bancada usa dados sintéticos e não comprova fluxo de ponta a ponta.
- Os roteiros Playwright (`web/e2e`) não foram executados, pelo mesmo motivo.
- Meta, ERP, Evolution, notificações do navegador e microfone não foram tocados nem homologados.
- Os pacotes de download (`scripts/build-download-packages.py`) não foram regenerados.
- Dentro de Configurações, cada painel (horários, automação, respostas prontas, integrações etc.) mantém o formulário que já existia; só a moldura mudou.
- A tela de Contatos não mostra o histórico de atendimentos do contato: a página atual do produto não o tem, e nada foi acrescentado.

## Histórico de reversão

A orientação inicial de voltar ao `main` dizia respeito à cópia local daquela execução; o estado atual do ramo e eventuais alterações devem ser conferidos antes de qualquer reversão.
