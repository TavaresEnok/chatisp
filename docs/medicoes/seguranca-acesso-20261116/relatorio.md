# Correção do acesso anônimo às áreas internas

Publicado e verificado em 16/11/2026 às 13:37 (America/Fortaleza, UTC-03). Código instalado: `0149ab7`. Domínio: https://chatisp.tokstiloesporte.com.

## Causa e correção

O agrupamento de rotas que montava `AppShell` não exigia uma sessão centralmente. Assim, `/painel` e outras páginas genéricas podiam mostrar a estrutura interna em estado deslogado. Retirar o atalho do login não bloqueava a entrada direta pela URL.

`RequireSession` agora envolve todo esse agrupamento, antes de montar menu, presença e páginas internas. Sem principal autenticado, navega para `/entrar` com substituição do histórico. A restauração de sessão válida por cookie foi mantida, incluindo a espera existente quando há CSRF. O cadastro obrigatório de MFA, quando sinalizado pelo servidor, mantém seu bloqueio anterior. Os fluxos públicos de login, convite, recuperação e MFA não recebem acesso a dados privados por essa guarda.

O HTML da SPA passou a responder com `Cache-Control: no-store` nos três perfis Caddy. Os arquivos estáticos não são a barreira de autorização do servidor: a SPA continua podendo responder HTML 200, mas o navegador deslogado não monta a área interna e é encaminhado ao login. Cada API continua responsável por autorizar seus dados e operações.

## Evidências

- Vitest completo na versão final: **329 testes em 65 arquivos**, todos aprovados. ESLint e build aprovados. Configuração Caddy ativa validada antes da troca.
- [11 rotas no navegador](navegador.json): raiz, Painel, Conversas, Contatos, Equipes, Relatórios, Configurações, Canais, Usuários, Provedores e caminho desconhecido → `/entrar`, sem `.app-shell`; HTML no-store.
- Cookie inválido recusado; login válido e recarregamento preservados; logout e Voltar sem reabrir a interface; replay do cookie revogado recusado com 401.
- Expiração real de uma sessão criada exclusivamente para a prova: alterado somente `idle_expires_at` daquele UUID recém-criado; nova entrada recusada com 401. Nenhuma sessão de outro usuário foi expirada ou revogada por esse teste.
- PATCH das preferências sem CSRF recusado com 403. O teste usou os mesmos valores lidos da conta, sem alterar preferências.
- [45 GET REST sem cookies](api-anonima.json), obtidos do OpenAPI de panel/platform: todos 401 e no-store. Nenhuma resposta de sucesso com dados.
- Handshake WebSocket correto, com Origin legítimo e sem cookie: 401/no-store. O WebSocket autenticado recebeu snapshot.
- [Quatro perfis](papeis.json): SaaS acessa administração da plataforma; dono da empresa, atendimento e supervisão recebem 403 nessa API, mantendo seus papéis próprios.
- Downloads autenticados conferidos por ZIP/SHA-256; catálogo/download administrativo anônimo 401; `/metrics` 404. `/download` permanece público por autorização anterior e contém só os pacotes selecionados.

A primeira sonda também fez GET HTTP comum em `/api/panel/ws` e recebeu 500 do ingresso, sem dados. Esse resultado não testa um handshake WebSocket: a prova posterior com Upgrade foi recusada com 401. Não houve alteração do Apache/Nginx compartilhados nesta correção.

A revisão estática independente conferiu os controles de sessão, papel, tenant, CSRF, WebSocket, anexos, pacotes e tokens da API pública. Não encontrou exposição anônima de dados nas superfícies revisadas. Os contratos de autenticação e o backend permaneceram inalterados; a imagem runtime foi atualizada apenas para distribuir os pacotes de fontes atuais. Esta revisão não é certificação de ausência de todas as vulnerabilidades nem varredura de toda a infraestrutura.

## Operação e limites

Recriadas somente API, worker e Caddy de `chatisp-pronto-hoje`; cinco serviços saudáveis. Banco, anexos, contas, senhas, dados demonstrativos e configurações de fornecedores preservados. Nenhuma migração ou ativação externa. Uma aba já aberta precisa recarregar, preferencialmente Ctrl+F5; com sessão válida, restaurar o acesso é esperado. Para testar anonimamente, usar janela anônima.

As provas de domínio usaram Chromium com resolução ao proxy interno `10.10.0.10` e TLS normal. Não representam uma nova prova a partir de outra rede nem um teste de fornecedores reais. Logs e scripts privados: `.tmp/security-route-release/`, especialmente `vitest-final.txt`, `lint.txt`, `build.txt`, `browser-final.txt`, `authenticated.txt` e `roles.txt`.

Backup anterior: `/home/enok/.local/share/chatisp/backups-publicacao/snapshot-20261116T162340Z-WkdK0QNG`, sete checksums verificados. Imagens anteriores preservadas nas tags `chatisp-pronto-hoje:runtime-before-security-routes` e `chatisp-pronto-hoje:edge-before-security-routes`. Não há migração a reverter; recuperar dados somente pelo [procedimento isolado](../publicacao-20261106/backup.md), preservando alterações posteriores. Retornar ao frontend anterior também retiraria esta correção de navegação.
