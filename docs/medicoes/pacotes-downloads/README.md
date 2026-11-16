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
