# Painel M3B

A fila consulta a API com aba, busca, filtros, ordenação e cursor de página; o cursor de eventos não é usado como página. A navegação de página substitui os resultados visíveis. As consultas são isoladas por tenant, usuário, sessão, papel, vínculo e caixa.

As ações de atribuição utilizam versão e identificador de intenção; uma resposta perdida deve ser repetida com a mesma intenção. Notas privadas, tags, prioridade e leitura pessoal usam os endpoints de anotações e não enviam mensagens. A leitura usa o cursor do histórico observado e a versão do marcador pessoal.

O compositor conserva Ctrl+Enter para enviar e mantém sua própria intenção e rascunho. Os atalhos da área de trabalho usam Ctrl+Alt+J para próxima conversa, Ctrl+Alt+F para busca e Ctrl+Alt+N para nota, sem capturar teclas de campos editáveis ou diálogos. Indicadores de visualização e digitação são consultivos, expiram e não bloqueiam envio.

## Diretório de atendentes
Filtro de atendente, rótulos de candidatos de transferência, atribuição na fila e indicadores de presença usam
`GET /api/panel/inboxes/{inbox_id}/directory` (ver [m3b-directory.md](m3b-directory.md)), paginado e independente
da página corrente de conversas. Presença só exibe rótulo de IDs presentes no diretório autorizado; os demais
aparecem como "Outro atendente"/"Atendente indisponível".

Correções de contrato: nota e prioridade enviam UUID nulo em `subject_id`.
Os destinos de setor vêm da conversa autorizada em `transfer-sectors`, sem usar
o catálogo administrativo para decidir elegibilidade. Após transferência de
setor, o painel remove o conteúdo e os caches da conversa de origem.
Fechamentos WebSocket 1008 `slow_client`/`resync_required` mantêm ações HTTP e
rascunhos em modo degradado, sem reconexão automática; `authorization_lost`
remove o escopo. `authorization_revoked` é ambíguo: remove imediatamente os
dados HTTP e o conteúdo visível da caixa e confirma a identidade em
`GET /api/panel/session`. Somente 401/403 nessa confirmação encerra a sessão e
limpa armazenamento global. Rede/5xx mantém rascunhos e intenções e suspende a
caixa. Com a mesma identidade válida, uma única assinatura nova reconstrói o
snapshot autorizado; outro fechamento suspende a caixa sem loop. Frames e
confirmações de instâncias antigas são ignorados.

Transferências pendentes pertencem à instância tenant/usuário/sessão/papel/
vínculo/caixa/conversa original. Após mudança ou desmontagem, a conclusão só
invalida consultas da caixa original; não altera a seleção ou os controles atuais.
