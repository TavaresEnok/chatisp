# Acessos públicos da demonstração

Em 16/11/2026 o proprietário autorizou explicitamente publicar os quatro acessos de demonstração, inclusive o administrador do SaaS. Nesta instalação descartável, qualquer visitante pode clicar num perfil para preencher e-mail e senha, e depois clicar Entrar. Não há importação de arquivo, armazenamento local ou configuração do navegador.

O build exige `VITE_DEVELOPMENT_LOGIN_SHORTCUTS=true` e `VITE_DEVELOPMENT_LOGIN_CREDENTIALS` com os quatro objetos `{email, senha}`. A configuração incompleta ou inválida deixa os atalhos desativados. Os builds padrão não habilitam a função. As credenciais desta instalação são lidas pelo operador do arquivo local `acessos-desenvolvimento.json`; não são gravadas em texto no código-fonte Git, mas **ficam públicas no JavaScript compilado quando a opção é ativada**, por autorização do proprietário.

O login normal, MFA de fatores já confirmados, sessão, papéis, CSRF e isolamento entre provedores continuam sendo validados pela API. O atalho preenche o formulário e não cria uma sessão sozinho. O perfil SaaS tem as permissões reais de administração da plataforma; os demais mantêm seus papéis existentes.

Para encerrar a demonstração pública: retirar a opção do build, publicar o frontend novamente, trocar as quatro senhas e revogar as sessões dessas contas. Só ocultar os botões não torna privadas as credenciais já publicadas. Não reutilizar esse conjunto em ambiente com dados ou clientes reais.

Testes: `web/src/components/auth/development-logins.test.tsx`, cobrindo configuração desativada/inválida, os quatro perfis, preenchimento completo e ausência de autenticação automática. Não altera senhas ou fatores existentes.
