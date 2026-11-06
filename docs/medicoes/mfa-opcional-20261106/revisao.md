# Revisão de segurança — MFA opcional, 06/11/2026

A revisão do diff de autenticação, migração 00057 e configuração do painel examinou as transições de sessão e o cadastro do fator. A política permite acesso por senha enquanto o fator não está confirmado; com fator confirmado, login exige TOTP ou código de recuperação. A ausência de chave para abrir fator confirmado falha fechada. A sessão antiga de cadastro, enquanto o fator não estiver confirmado, pode acessar o painel.

A migração revalida usuário ativo, vínculo, provedor e sessão válida ao iniciar e confirmar. A confirmação compara o ciphertext validado, serializa com emissão de sessão pelo usuário e revoga as demais sessões na mesma transação. Reset de fator exige sessão plena com fator confirmado e revalida ator, vínculo e sessão transacionalmente. O SQL revoga as permissões das funções antigas de início, confirmação e reset; versões antigas da aplicação não podem operar simultaneamente com a migração 00057. A implantação e eventual reversão exigem coordenação de binários e banco, com serviços fechados e backup; não executar rollback SQL apenas para contornar erro.

O responsável pelo backend informou aprovação em PostgreSQL descartável do harness para:

```sh
GOTOOLCHAIN=local GOPROXY=off go -C backend test ./internal/platform/auth ./internal/platform/httpapi ./internal/channel/configuration ./internal/channel/httpapi
GOTOOLCHAIN=local GOPROXY=off go -C backend test ./internal/platform/auth -run 'TestMFAConfirmationSerializesWithPasswordSession|TestOptionalMFALegacySessionAndEnrollmentRace|TestEnrollmentCannotUseRevokedSession' -count=1
```

Os testes cobrem sessão legada, segredo substituído, snapshot anterior à ativação, revogação de outras sessões, sessão revogada, recadastro e falta de chaves. A prova de concorrência verifica os dois ordenamentos entre emissão por senha e confirmação, com transações separadas; a observação do bloqueio usa uma janela de 150 ms. Esses resultados não provam uma implantação na instalação ativa, nem substituem os gates amplos e a validação de interface e Compose executados pelo integrador.
