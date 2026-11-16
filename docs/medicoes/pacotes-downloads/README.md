# Pacotes de documentação no painel

O Painel `/painel` inclui a seção **Pacotes para download**, visível a administradores autenticados. O catálogo GET `/api/panel/packages` e os downloads GET `/api/panel/packages/{id}/download` revalidam sessão/papel no servidor; não são arquivos públicos no Caddy. Os ZIPs estão na imagem da API em `/usr/share/chatisp/packages`, fora do docroot. Respostas usam `private, no-store` e download ZIP usa `attachment`/`nosniff`. IDs/nomes permitidos são fixos; symlinks e hashes divergentes são recusados.

Pacotes:

- `inventario-funcional`: inventário, checklist de telas e matriz de requisitos.
- `briefing-tecnico`: stack, fontes do frontend, manifests e contratos OpenAPI, junto do inventário.
- `referencias-visuais`: capturas e documentação das propostas/demonstrações; imagens conceituais não significam recursos implementados.

`python3 scripts/build-download-packages.py` regenera os ZIPs e o manifesto em `backend/package-assets/` por allowlists. Não inclui `.env`, banco, backups, credenciais privadas, `node_modules` ou `.git`. Regenerar depois de mudanças relevantes de frontend/contratos/documentação, revisar e versionar os artefatos; ambos Dockerfiles copiam o diretório para a imagem de runtime. Os ZIPs não substituem o repositório completo.

A publicação também atualiza Go para 1.27.4, correção de GO-2026-6859 (https://pkg.go.dev/vuln/GO-2026-6859), sem alterar schema do banco ou políticas de MFA/fornecedores. O perfil de SMTP de teste usa o mesmo patch. O gate inicialmente identificou a vulnerabilidade no Go 1.27.3; repetir os gates com o patch, sem omitir a detecção.

Antes da atualização foram preservadas as imagens `runtime-before-downloads` e `edge-before-downloads` no namespace `chatisp-pronto-hoje` e criado snapshot privado `snapshot-20261116T020023Z-DuwsICt8` em `/home/enok/.local/share/chatisp/backups-publicacao/`. Dados/credenciais existentes não devem ser sobrescritos. Sem migração nova: reversão de API/worker/Caddy pode usar as imagens anteriores preservadas, coordenando a interrupção e retomada apenas desses serviços.

Testes do backend cobrem autorização, ZIP, cabeçalhos, hash, symlink e tentativa de traversal. Testes do frontend cobrem catálogo, retry/vazio, papéis e perda de sessão. A publicação deve confirmar os três downloads em navegador autenticado, integridade SHA-256 e recusa sem sessão. Segredos e cookies não devem aparecer nos logs da prova.

## Verificação da entrega

Código instalado `ab31f72`, sem migração nova. Go 1.27.4. A análise inicial em Go 1.27.3 encontrou GO-2026-6859; após atualização, lint completo/gerados concluíram com saída 0 e govulncheck apontou zero vulnerabilidades alcançáveis. Testes dos pacotes HTTP API, autenticação e runtime passaram. Quatro testes da área de download, typecheck, ESLint e build frontend passaram. ZIPs e SHA-256 foram conferidos antes da construção.

As imagens preparadas foram aplicadas somente à API, worker e Caddy do projeto existente. PostgreSQL e Garage mantiveram os volumes e a mesma configuração; os cinco serviços ficaram saudáveis. Uma jornada Chromium pelo domínio com resolução ao proxy interno validou login administrativo, seção no Painel, download dos três arquivos por seus links reais, nome/tamanho/magic ZIP e SHA-256 de cada download. Catálogo e download sem sessão responderam 401; a sessão de verificação terminou com logout 204. Nenhum MFA foi iniciado, nem conexão de fornecedor ou expurgo habilitado. A prova usa TLS público normal; não é uma nova prova a partir da Internet.

Logs locais privados em `.tmp/downloads-release/`: `go.txt`, `lint-final.txt`, `build-final.txt`, `build-packages-final.txt`, `deploy.txt` e `installed.txt`. A captura de tela da sessão ficou apenas nesse diretório privado. Sem limpeza global ou alteração de outros projetos.
