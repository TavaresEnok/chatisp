# MFA opcional e ativação por QR Code — 06/11/2026

Decisão expressa do proprietário: MFA não é obrigatório; a conta deve oferecer ativação voluntária. Essa decisão substitui a política histórica que exigia cadastro de administradores e superadmin. Código instalado: **`8150c9b`**, migração **00057**. A especificação, o plano, a matriz e o procedimento operacional foram atualizados.

## Comportamento entregue

Contas sem fator confirmado entram com senha e recebem acesso normal conforme seu papel. Sessões antigas de cadastro também são aceitas enquanto o fator não estiver confirmado. Contas que já ativaram MFA continuam exigindo TOTP ou código de recuperação: a mudança não remove fatores existentes.

A opção está no início de **Configurações → Segurança da conta**. O cadastro voluntário mostra QR Code SVG gerado no navegador por `qrcode.react` 4.2.0, sem serviço externo, e chave manual copiável dentro de uma seção expansível. Os códigos de recuperação permanecem visíveis até a conclusão; a confirmação exige o código do aplicativo. Abrir a página não gera segredo automaticamente. Uma conta com MFA ativo mostra esse estado e não oferece novo cadastro por cima do fator atual.

A confirmação compara o segredo validado sob lock e revoga as outras sessões na mesma transação. Início, confirmação e reset revalidam sessão/usuário/vínculo/provedor. Reset de fator continua sendo uma operação privilegiada e exige um ator com MFA ativo. O [parecer de revisão](revisao.md) detalha os controles e limites dos testes concorrentes.

## Verificações

- Execução ampla de Go: 62 pacotes passaram; quatro falharam por expectativas antigas de MFA obrigatório. Corrigidas essas expectativas, a repetição completa dos quatro pacotes (`cmd/chatisp`, `businesshours`, `channelpolicy`, `triage`) passou. Ao todo, os 66 pacotes com testes ficaram verificados; não se declara que a primeira execução ampla terminou com sucesso.
- Os testes focados de autenticação/HTTP/configuração de canal passaram. Provas de sessão legada, sessão revogada, segredo substituído, revogação de outra sessão, fator ativo sem keyring e os dois ordenamentos de concorrência entre login e confirmação passaram, inclusive com `-race` nos testes críticos.
- Frontend: 61 arquivos/297 testes passaram antes dos últimos ajustes visuais; após eles passaram os 12 testes finais de autenticação, build/typecheck e lint. O lint completo do repositório, incluindo gerados e análise Go, terminou com saída 0.
- A primeira prova Compose (`642407`) falhou porque o runner navegava antes do término do aceite do convite. A espera foi corrigida, preservando a verificação do QR e do fluxo real. A prova final **`chatisp-validate-720729` terminou com saída 0**, incluindo cadastro optativo, MFA ativo, convite/recuperação por SMTP sintético TLS, cookies/CSRF/Origin, atendimento/WebSocket, anexos, suspensão, queda/retorno do storage e reinício com persistência. O perfil de backup completo não foi repetido nesta rodada.
- Os dois projetos descartáveis foram removidos pelo seu próprio validador; a instalação persistente foi preservada. Não houve limpeza global nem push.

Logs brutos locais: `.tmp/mfa-opcional-20261106/` (`go-all.txt`, `go-recheck.txt`, `lint.txt`, `compose.txt`, `compose-final.txt`, `build-final.txt`, `deploy.txt` e `installed-check.txt`). As provas anteriores amplas de backup/restore permanecem nos relatórios de prontidão e publicação.

## Aplicação na instalação existente

Antes da atualização foi criado o snapshot privado `/home/enok/.local/share/chatisp/backups-publicacao/snapshot-20261106T161518Z-LGZWY2kw`; todos os itens de `SHA256SUMS` foram verificados. Essa cópia continua no mesmo servidor, sem substituir um destino independente.

Imagens novas foram preparadas e validadas antes da janela. API e worker antigos foram parados; a consulta ao PostgreSQL confirmou zero conexões dos papéis de runtime antes de aplicar a migração 00057. Os mesmos arquivos Compose, projeto, rede, banco, volumes e credenciais foram reutilizados. A subida concluiu com PostgreSQL, Garage, API, worker e Caddy saudáveis. Nenhum fornecedor real ou expurgo foi habilitado.

Uma prova final em Chromium entrou com a senha original de `admin@chatisp.local`, abriu o painel sem MFA obrigatório, verificou `mfa_enabled=false` e a conexão WebSocket autenticada pelo Nginx, abriu Segurança da conta e a tela de ativação voluntária, e encerrou a sessão com 204. **Não foi iniciado nem confirmado MFA do proprietário.** A consulta posterior confirmou migração 57 e uma conta original sem fator confirmado. A prova usou o nome público conectado ao Nginx interno `10.10.0.10`, com validação normal do certificado; não representa nova prova de conectividade externa.

Estado, referências das imagens e acesso privado foram atualizados em `/home/enok/.local/share/chatisp/pronto-hoje-20261106/`. A configuração posterior foi arquivada em `/home/enok/.local/share/chatisp/publicacao-20261106/config-after-mfa.tar.gz`, com checksum e recibo `mfa-installed.json`. Senhas e segredos não foram incluídos no Git.

## Recuperação

A migração 00057 revoga execução das funções antigas de cadastro/reset; não executar binário antigo com essa migração nem misturar versões. Em falha, manter API/worker fechados e corrigir a versão ou planejar recuperação coordenada de banco e imagens a partir do snapshot, preservando dados e decisões de exclusão posteriores. Não executar `down -v`, SQL de rollback ou restauração por cima de volumes em uso para contornar um erro. O [procedimento de recuperação existente](../publicacao-20261106/backup.md) continua aplicável junto desta ressalva de versão.
