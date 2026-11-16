# Atalhos de desenvolvimento no login

O build só mostra os quatro atalhos quando `VITE_DEVELOPMENT_LOGIN_SHORTCUTS=true`. O padrão é desligado. Para desativar, gerar e publicar novamente o frontend sem essa variável. As receitas Docker padrão não habilitam a opção.

Os botões preenchem apenas e-mails de demonstração: administrador SaaS, dono da empresa, atendimento e supervisão. Limpam a senha, o código MFA e a seleção de provedor anteriores, focam a senha e não enviam o formulário. O login e as permissões continuam validados pela API. Nenhuma senha, token ou sessão é incluída no frontend.

Na instalação de desenvolvimento existente, a conta SaaS separada foi criada por operação auditada; as demais contas já existiam. Credenciais privadas em `/home/enok/.local/share/chatisp/pronto-hoje-20261106/acessos-desenvolvimento.json` (0600). Não versionar nem distribuir esse arquivo. Não é criada conta automaticamente ao instalar o produto. O link “Esqueci a senha” permanece; envio de recuperação depende do SMTP configurado.

Teste de regressão: `web/src/components/auth/development-logins.test.tsx`, cobrindo opção desligada, quatro perfis, limpeza da senha, foco e ausência de autenticação automática.
