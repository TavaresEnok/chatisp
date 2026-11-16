# Verificação — redesign de 15/11/2026

## Revisão atual — 16/11/2026

| Verificação | Resultado |
|---|---|
| Vitest | 307 testes em 64 arquivos passaram |
| `make lint` e `npm --prefix web run build` | Passaram; o build mantém aviso de tamanho de pacote |
| E2E M3B, M4A, M4B–M6 e M7-OPS | Passaram com API Go e PostgreSQL reais, sem skips nos roteiros |
| UX nos temas escuro e claro | 52 vistas por tema; zero falhas, saída 0 nos dois |
| CI | [Checks do PR 1](https://github.com/TavaresEnok/chatisp/pull/1/checks); rodada [43802462051](https://github.com/TavaresEnok/chatisp/actions/runs/43802462051) aprovada em `0860c80`; exigir verde também no cabeçalho final antes de mesclar |

As integrações de fornecedores nos E2E são sintéticas; não constituem homologação externa. Os logs locais desta rodada estão em `.tmp/grafite-review/`: `vitest-final.txt`, `lint.txt`, `build.txt`, `m3b-final.txt`, `m4a-final.txt`, `m4b-m6.txt` e `m7-final.txt`. A aprovação automatizada de UX está concluída; a mesclagem depende também do CI verde no cabeçalho final do PR.

Os testes agora procuram o nome acessível “Caixa de atendimento” e mantêm suas verificações. Os fluxos E2E foram atualizados para as abas de detalhes; a fixture de teclado inicia cada percurso no corpo e remove o foco ao final, para que o link de pular conteúdo da rota anterior não interfira na próxima auditoria axe. As correções da revisão abrangem novo setor e erro de contato no celular, reset da aparência ao sair, amostra excluída e barras de valor zero, alvo de 24 px no menu, recorte do topo e reflow com altura de 256 px. O desenho aprovado foi preservado.

## Ambiente e reprodução desta revisão

Linux, Node 24.11.0, toolchain Go 1.27.4 do módulo, PostgreSQL efêmero da fixture e Chromium 140.0.7339.186. Os executáveis já disponíveis foram reutilizados. O navegador usa o certificado efêmero do laboratório; nenhuma configuração de TLS de produção foi alterada.

```sh
export PATH=/tmp/chatisp-node24/bin:/home/enok/.local/share/go1.27.3/bin:$PATH
export M3B_CHROMIUM_EXECUTABLE=/home/enok/.cache/ms-playwright/chromium-1193/chrome-linux/chrome
# Executar em série: os roteiros compartilham as portas de frontend do laboratório.
CHATISP_E2E_API_PORT=28080 npm --prefix web run test:e2e:m3b
CHATISP_E2E_API_PORT=28080 npm --prefix web run test:e2e:m4a
npm --prefix web run test:e2e:m4b-m6
npm --prefix web run test:e2e:m7-ops
UX_THEME=dark npm --prefix web run test:e2e:ux
UX_THEME=light npm --prefix web run test:e2e:ux
```

Evidências detalhadas: [tema escuro](evidencias/ux-dark.json) e [tema claro](evidencias/ux-light.json). Cada arquivo contém 13 páginas em quatro tamanhos: 1280×800, 768×1024, 360×640 e 320×256 CSS px (alvo de reflow equivalente a 400%). Foram verificados axe WCAG A/AA/2.2, foco visível, controles com nome, tamanho mínimo de alvo, rolagem horizontal e chaves React duplicadas. Não se trata de certificação de toda a WCAG nem de teste com tecnologia assistiva humana.

A API local de notificações foi exercitada pelo roteiro, incluindo situações simuladas de recusa e indisponibilidade. Isso não homologa entrega real de notificações, microfone, Meta, ERP ou Evolution. Nenhum fornecedor real foi ativado. A instalação de produção, seus dados e credenciais foram preservados. Os pacotes foram regenerados com o frontend revisto, estas notas e os relatórios de UX; a disponibilidade pública da nova versão depende de publicação posterior.

## Histórico da execução inicial

Executado em 16/11/2026, em Windows 11 com Node 24.15 e npm 11.12, no ramo `redesign/grafite-20261115`. As limitações e falhas abaixo descrevem essa execução histórica e foram superadas ou atualizadas pela rodada acima.

| Verificação | Comando (em `web/`) | Resultado |
|---|---|---|
| Tipos | `npx tsc -b --pretty false` | Sem erros |
| ESLint | `npx eslint .` | Sem apontamentos |
| Testes | `npx vitest run` | 299 aprovados, 3 reprovados (302); 61 de 62 arquivos |
| Build | `npx vite build` | Concluído; permanece o aviso antigo de tamanho do pacote |

### Os 3 testes reprovados já falhavam no `main`

Arquivo `src/test/m3b-socket-transfer.test.tsx`:

- transferência com socket ativo preserva sessão, outro rascunho e a intenção perdida
- transferência tardia após mudança de conversation preserva seleção e controles
- transferência tardia após mudança de inbox preserva seleção e controles

Os três procuram um campo com o rótulo `Caixa`, e a tela o chama de `Caixa de atendimento`.
O mesmo arquivo foi executado em uma cópia intocada do `main` (`696dd67`) e reprovou os mesmos três testes, com o mesmo erro. Não foram alterados nesta entrega.

### O que não foi executado na execução inicial

- Roteiros Playwright de `web/e2e` e a jornada com API Go e PostgreSQL: a máquina não tem PostgreSQL nem Docker.
- Testes e lint do backend: o backend não foi alterado.

### Rodada inicial de 16/11/2026 (estrutura de Conversas, Contatos, Equipes, Relatórios e Configurações)

- `npx tsc -b --noEmit`: sem erros.
- `npx eslint src --max-warnings=0`: sem avisos.
- `npx vitest run`: 299 aprovados e 3 reprovados em 302 (61 de 62 arquivos). As 3 reprovações são as mesmas de `src/test/m3b-socket-transfer.test.tsx`, que também reprovam no `main` sem alteração.
- `npx vite build`: concluído.
- Conferência visual na bancada, temas escuro e claro: Conversas (fila, filtros abertos, conversa aberta e encerrada, três abas de detalhes, adiar), Contatos, Equipes, Relatórios e Configurações; largura de celular sem rolagem horizontal nas seis áreas.
- Não executado: roteiros Playwright (`web/e2e`), que dependem de API e PostgreSQL.
