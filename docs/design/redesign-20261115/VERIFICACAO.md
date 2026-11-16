# Verificação — redesign de 15/11/2026

Executado em 16/11/2026, em Windows 11 com Node 24.15 e npm 11.12, no ramo `redesign/grafite-20261115`.

| Verificação | Comando (em `web/`) | Resultado |
|---|---|---|
| Tipos | `npx tsc -b --pretty false` | Sem erros |
| ESLint | `npx eslint .` | Sem apontamentos |
| Testes | `npx vitest run` | 299 aprovados, 3 reprovados (302); 61 de 62 arquivos |
| Build | `npx vite build` | Concluído; permanece o aviso antigo de tamanho do pacote |

## Os 3 testes reprovados já falham no `main`

Arquivo `src/test/m3b-socket-transfer.test.tsx`:

- transferência com socket ativo preserva sessão, outro rascunho e a intenção perdida
- transferência tardia após mudança de conversation preserva seleção e controles
- transferência tardia após mudança de inbox preserva seleção e controles

Os três procuram um campo com o rótulo `Caixa`, e a tela o chama de `Caixa de atendimento`.
O mesmo arquivo foi executado em uma cópia intocada do `main` (`696dd67`) e reprovou os mesmos três testes, com o mesmo erro. Não foram alterados nesta entrega.

## O que não foi executado

- Roteiros Playwright de `web/e2e` e a jornada com API Go e PostgreSQL: a máquina não tem PostgreSQL nem Docker.
- Testes e lint do backend: o backend não foi alterado.

## Rodada de 16/11/2026 (estrutura de Conversas, Contatos, Equipes, Relatórios e Configurações)

- `npx tsc -b --noEmit`: sem erros.
- `npx eslint src --max-warnings=0`: sem avisos.
- `npx vitest run`: 299 aprovados e 3 reprovados em 302 (61 de 62 arquivos). As 3 reprovações são as mesmas de `src/test/m3b-socket-transfer.test.tsx`, que também reprovam no `main` sem alteração.
- `npx vite build`: concluído.
- Conferência visual na bancada, temas escuro e claro: Conversas (fila, filtros abertos, conversa aberta e encerrada, três abas de detalhes, adiar), Contatos, Equipes, Relatórios e Configurações; largura de celular sem rolagem horizontal nas seis áreas.
- Não executado: roteiros Playwright (`web/e2e`), que dependem de API e PostgreSQL.
