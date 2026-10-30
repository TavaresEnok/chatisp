# Compositor M3

`MessageComposer` recebe `scope`, `onSend`, `offline`, `createIntentKey` e `localQueue`. O comando contém texto e `intentKey`; resultados perdidos ou incertos mantêm a chave para repetir o mesmo texto. Uma decisão explícita de enviar como nova mensagem cria outra chave. Aceitação da fila local indica apenas enfileiramento, não entrega ou leitura.

O rascunho e a intenção pendente são armazenados localmente por tenant, usuário, sessão e conversa autenticados. A retomada de uma intenção pendente usa a mesma chave; edição durante envio é preservada. Logout ou troca de usuário limpa os rascunhos. Sem armazenamento disponível o compositor segue em memória. A página M2 inclui a sessão no identificador de usuário do escopo; o compositor valida esse escopo contra o store de sessão.

Ctrl/Command+Enter envia no campo. As ferramentas inserem emoji ou delimitadores `*` e `_` na seleção e devolvem o foco. No workspace, Ctrl/Command+Alt+J seleciona e focaliza a próxima conversa; Ctrl/Command+Alt+F e Ctrl/Command+Alt+N chamam `onSearch` e `onNote` apenas quando fornecidos. Esses atalhos do workspace não interceptam campos editáveis ou diálogos.
