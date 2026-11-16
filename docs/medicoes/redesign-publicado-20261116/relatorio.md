# Publicação do redesenho grafite

Verificado em 16/11/2026 às 11:10 (America/Fortaleza, UTC-03), em https://chatisp.tokstiloesporte.com/painel.

Código instalado: `24a64ac93eae8f032ef259ec7345eee779ab8c2c`, merge do [PR 1](https://github.com/TavaresEnok/chatisp/pull/1). Árvore de produto idêntica a `dc4f922`, aprovado no [CI 43803618966](https://github.com/TavaresEnok/chatisp/actions/runs/43803618966) e nos cinco roteiros Playwright. Revisão em [VERIFICACAO.md](../../design/redesign-20261115/VERIFICACAO.md).

## Instalação e provas

Atualizadas somente API, worker e Caddy de `chatisp-pronto-hoje`, sem migração nova. PostgreSQL e Garage mantiveram os contêineres; mounts comparados por destino e preservados. Ambiente, senha e arquivo de acesso coincidem byte a byte com o backup. Nenhum fator MFA foi cadastrado nem fornecedor ativado.

- Login existente, topo grafite, menu em pílula, expansão e temas claro/escuro conferidos em Chromium.
- Três downloads autenticados: nome, tamanho, ZIP e SHA-256 conferidos.
- WebSocket autenticado de Conversas recebeu `snapshot`.
- `/download` atualizado: SHA-256 correto; `/metrics`: 404; catálogo e download administrativo sem sessão: 401; logout da prova: 204.
- Cinco serviços saudáveis após recriação, com dados persistidos.

Provas pelo nome público resolvido ao Nginx `10.10.0.10`, com validação TLS normal. A conexão direta deste host ao IP público continua recusada; não é nova prova a partir de outra rede. Os proxies compartilhados foram preservados. Uma aba já aberta precisa recarregar (`Ctrl + F5`) para carregar os novos assets.

Runtime: `sha256:556c24def75ed316ccbc7cd15a6be0ec724e7039c062afa8cfde98da3eadc4e3`. Edge: `sha256:4f2759d4328c0710de328107709c48d0b3819075102d0028002177d2d2fcf00a`.

Logs e capturas privados em `.tmp/grafite-release/`: `installed-final.txt`, `painel-escuro.png`, `painel-claro.png` e `conversas.png`. Recibo: `/home/enok/.local/share/chatisp/pronto-hoje-20261106/publicacao-grafite-20261116.json`; configuração e controlador continuam nesse diretório privado.

## Backup e reversão

Snapshot anterior: `/home/enok/.local/share/chatisp/backups-publicacao/snapshot-20261116T140248Z-5ZflGJZy`, diretório 0700 e arquivos 0600. Sete checksums conferidos, dump PostgreSQL legível e arquivos tar íntegros. Não foi repetida restauração completa nesta publicação. A cópia contém dados, credenciais, configuração e imagens, no mesmo host; recuperação de dados em [backup.md](../publicacao-20261106/backup.md).

A reversão desta mudança não exige restaurar o banco. Imagens anteriores preservadas nas tags abaixo; usar só este projeto, sem remover volumes:

```sh
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py stop chatisp-api chatisp-worker
docker tag chatisp-pronto-hoje:runtime-before-grafite-20261116 chatisp-pronto-hoje:runtime
docker tag chatisp-pronto-hoje:edge-before-grafite-20261116 chatisp-pronto-hoje:edge
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-deps --no-build --wait chatisp-api chatisp-worker caddy
```

Após reversão, conferir saúde/login e registrar imagens ativas no recibo privado. A homologação de fornecedores, microfone e entrega real de notificações segue na [tabela de dependências](../../dependencias-externas.md).
