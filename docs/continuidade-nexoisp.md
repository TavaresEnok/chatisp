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
