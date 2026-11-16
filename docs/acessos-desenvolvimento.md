# Atalhos de desenvolvimento no login

O build só mostra os quatro atalhos quando `VITE_DEVELOPMENT_LOGIN_SHORTCUTS=true`. O padrão é desligado. Para desativar, gerar e publicar novamente o frontend sem essa variável. As receitas Docker padrão não habilitam a opção.

Os botões preenchem e-mail e senha do administrador SaaS, dono da empresa, atendimento e supervisão **após importar o arquivo privado de acessos** na opção Importar acessos privados (JSON). Sem importação, preenchem somente o e-mail. Nenhuma senha real, token ou sessão é embutida no código público ou nos pacotes. O arquivo selecionado é lido localmente; a senha só é enviada à API de login ao clicar Entrar. O login, MFA e as permissões continuam sendo validados normalmente.

As senhas importadas ficam apenas na memória da aba, sem localStorage, sessionStorage ou cookies. Sair mantém essa memória para facilitar alternar os perfis de desenvolvimento; Limpar senhas carregadas, recarregar ou fechar a aba remove os acessos importados. Não é um gerenciador de senhas: quem puder operar a mesma aba poderá usar esses perfis. Importar não autentica e não envia o arquivo. Formato: array de objetos com email e senha (campo `senha`); aceita apenas os quatro e-mails de demonstração, até 16 KiB, sem duplicatas. Erros não exibem o conteúdo do arquivo.

Na instalação de desenvolvimento existente, a conta SaaS separada foi criada por operação auditada; as demais contas já existiam. Credenciais privadas em `/home/enok/.local/share/chatisp/pronto-hoje-20261106/acessos-desenvolvimento.json` (0600). Não versionar nem distribuir esse arquivo. Não é criada conta automaticamente ao instalar o produto. O link “Esqueci a senha” permanece; envio de recuperação depende do SMTP configurado.

Teste de regressão: `web/src/components/auth/development-logins.test.tsx`, cobrindo opção desligada, quatro perfis, limpeza da senha anterior, importação local, preenchimento dos quatro perfis, foco, recusa de arquivo inválido, limpeza explícita e ausência de autenticação automática ou armazenamento persistente.
