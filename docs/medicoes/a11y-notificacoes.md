# Acessibilidade, responsividade e notificações no navegador

Executável: `CHATISP_E2E_API_PORT=<porta livre> npm --prefix web run test:e2e:ux` (`web/e2e/ux.spec.ts`, runner `web/scripts/ux-e2e.mjs`).
Chromium 140.0.7339.186 real (Playwright 1.55.1), fixture local M4A (Meta simulada, loopback, dados sintéticos), axe-core 4.10.2.
Sem skip: qualquer achado derruba a execução. `UX_DISCOVER=1` só lista os achados.

## Notificações (Notification API local; não é push)

Não há Service Worker, Web Push nem servidor de push: a notificação é local e só aparece com o painel aberto (o próprio painel já diz "Notificações exigem o painel aberto."). O texto é fixo ("Nova conversa" / "Nova mensagem") e não leva conteúdo nem telefone do cliente.

| Caso | Como foi exercitado | Resultado |
| --- | --- | --- |
| Permissão concedida | `grantPermissions(["notifications"])` no Chromium; webhook de mensagem de um número novo e depois uma segunda mensagem | `Notification` criada com "Nova conversa" e "Nova mensagem"; nada do texto do cliente nem do telefone; sem erro de página |
| Sem concessão | contexto padrão do Chromium headless (`Notification.permission === "default"`; o pedido não pode ser respondido sem diálogo) | tela diz "Permissão ainda não solicitada."; com a preferência ligada e mensagem chegando, nenhuma notificação criada |
| API ausente | `delete window.Notification` | "Notificações indisponíveis neste navegador.", sem botão de pedir permissão, sem erro, fila funciona |
| Pedido de permissão, resposta concedida / negada | API simulada (o diálogo do navegador não é automatizável em headless): o pedido só ocorre no clique, uma vez | "Permissão concedida." e a notificação passa a sair; "Permissão recusada no navegador." e o botão some |
| Pedido que rejeita | API simulada com `requestPermission` rejeitando | alerta "Não foi possível solicitar permissão ao navegador."; estado continua "ainda não solicitada" |
| Construtor que lança (ex.: navegadores móveis) | API simulada com `new Notification` lançando | nenhum erro de página; a conversa aparece na fila |

Limite: o diálogo nativo de permissão e navegadores que não sejam Chromium não foram exercitados.

## Acessibilidade e responsividade

Páginas: Conversas (conversa aberta), Configurações, Contatos, Equipes, Relatórios, Painel, Canais e Entrar.
Viewports: 1280×800, 768×1024 e 360×640 (24 visões). Para cada uma: axe com as tags `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`, `wcag22aa`; rolagem horizontal da página e de elementos largos fora de contêiner rolável; percurso por Tab (foco visível, elemento visível, nome acessível, sem armadilha).

Resultado final: 0 violações axe, 0 px de rolagem horizontal, 0 problemas de foco, de 6 a 47 paradas de Tab por visão.

Problemas reais encontrados e corrigidos:

- **Anel de foco invisível** em `Input`, caixa de mensagem, lista de conversas, linhas da linha do tempo e diálogo de encerrar: `outline-none` define `--tw-outline-style: none` no Tailwind v4 e `focus-visible:outline-2` herdava `none`. Corrigido com `focus-visible:outline-solid`.
- `aria-expanded` não permitido na caixa de mensagem (papel textbox): removido.
- Alvos menores que 24×24 (WCAG 2.2 AA 2.5.8): caixas de seleção/rádio (regra global de 1,5rem) e botões de emoji/negrito/itálico (agora `min-h-11 min-w-11`).
- Regiões rolagem sem acesso por teclado: mapa de calor e tabela de auditoria agora são `role="region"` com nome e `tabIndex=0`.
- Tabela de auditoria estourava 60 px a 768 e 196 px a 360: dentro de contêiner rolável.
- Conversas a 360 px estouravam 133 px: grade de uma coluna `minmax(0,1fr)` e filtros da fila podem encolher (`min-w-0`).
- Campos de arquivo estouravam 17 px a 360 px: `max-width: 100%`.

Fora do alcance desta auditoria: leitor de tela real, modo de alto contraste do sistema, zoom de 400 %, e a tela inicial de MFA/convite/recuperação de senha (não percorridas no navegador).

## Avatar do contato

A especificação diz "Avatar: só se o WhatsApp fornecer" e a Cloud API não fornece foto. O painel usa, então, iniciais determinísticas (primeira e última palavra do nome; a cor sai de um hash do nome, com contraste ≥ 7:1) na fila, na lista de conversas, no cabeçalho e em Contatos. É decorativo (`aria-hidden`), nunca carrega URL remota e o nome continua sendo o nome acessível. Se um dia houver imagem autorizada, ela entra no mesmo componente (`web/src/components/ui/contact-avatar.tsx`).
