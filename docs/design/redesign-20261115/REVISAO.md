# Revisão do PR 1 — bf4563a sobre 696dd67

Revisão estática do diff de produção em `web/src`; o teste de ponta a ponta com API real e a verificação de CI ficam com a integração.

## Achados

1. **Resolvido — criar o primeiro setor fica indisponível no celular.** Em `web/src/pages/teams-page.tsx:22-39`, a ficha começa fechada e só se abre selecionando um setor. A regra de `web/src/index.css:3476-3489` esconde a ficha em telas de até 767 px. Se a lista estiver vazia, não há controle para abrir o formulário de criação. Disponibilizar uma ação de novo setor na lista, inclusive no estado vazio.
2. **Resolvido — o estado de aparência sobrevivia à saída na aba atual.** `web/src/stores/interface-store.ts:34-45` inicializa o Zustand uma vez; a saída e a perda de sessão limpam o armazenamento, mas não redefinem esse estado em memória. Uma nova sessão na mesma aba herda tema e menu anteriores, apesar da limpeza prometida no comentário. A limpeza agora redefine o estado e atualiza o documento sem gravar preferências.
3. **Resolvido — a amostra excluída sumia visualmente do relatório.** `web/src/features/reports/attendance-report.tsx:43-48` não mostra `not_applicable` quando há amostra, ao contrário do texto anterior. A informação ainda existe apenas no texto para leitor de tela. A quantidade fora da amostra agora aparece junto dos tempos.
4. **Resolvido — o gráfico atribuía altura positiva a valores zero.** `web/src/features/reports/attendance-report.tsx:74-76` impõe 2% à altura mesmo quando `cycles` é zero. A altura agora é proporcional, inclusive zero para zero.
5. **Resolvido — falha ao abrir contato por documento fica escondida no celular.** `web/src/pages/contacts-page.tsx:38,71` coloca o erro na ficha, sem abri-la em caso de falha. Como a ficha está oculta no estado inicial em telas estreitas, a mensagem de erro não aparece nem é anunciada. Apresentar o erro no painel ativo.

Os painéis de detalhes inativos usam `display: none`, e as abas usam seleção e navegação por setas. A revisão estática não substitui a confirmação de foco e fluxos em navegador.

As correções 2–4 foram verificadas nos testes direcionados de interface e relatório (10 testes passaram).

Os fluxos 1 e 5 foram corrigidos com ação administrativa Novo setor na lista e alerta de abertura na lista de contatos; três testes dedicados passaram sem alterar as permissões.
