# Permissão de reindexação do River — 17/11/2026

Publicação do commit `1343857`, a pedido do proprietário. Compilado a partir de `ea5be3f`. Frontend, PostgreSQL e Garage não foram alterados nem reiniciados.

## O que mudou

O reindexador de manutenção do River roda no worker com o papel `chatisp_jobs`, que não tinha `MAINTAIN` em `river.river_job`. Toda noite os sete índices da fila falhavam com erro no log do worker. A restrição do schema agora concede `MAINTAIN` só nessa tabela e só a esse papel.

## Publicação

- Cópia lógica do banco antes da troca: `~/.local/share/chatisp/backups-publicacao/pgdump-20261117-antes-maintain.sql.gz` (íntegra, acesso privado).
- Imagem `runtime` compilada de `deploy/validacao/Dockerfile.alpine`. Pacotes de download e as 56 migrações idênticos aos da imagem anterior; só o binário mudou.
- `chatisp-migrate` executado uma vez (saída 0). Nenhuma migração de schema nova; aplicou a concessão.
- `chatisp-api` e `chatisp-worker` recriados; os cinco serviços saudáveis.

## Verificação

- `has_table_privilege`: `chatisp_jobs` com `MAINTAIN` em `river.river_job`; `chatisp_app` continua sem.
- `REINDEX INDEX CONCURRENTLY river.river_job_kind` executado com `SET ROLE chatisp_jobs`: concluído.
- Nenhum erro nos logs da API e do worker depois da troca. `/entrar` e `/healthz` 200 no domínio público; entrada pelo atalho "Supervisão" e fila carregada, sem erros de página.
- O teste do commit (`postgres_test.go`) passou no CI do GitHub em `1343857`.

Não verificado: a execução noturna do reindexador em si. O próximo ciclo deve terminar sem as sete linhas `maintenance.Reindexer: Error reindexing`.

Runtime: `sha256:e7822dc2bf03c8a34b4d29630bd9057765ce5fa607e39a8a3be9cb4c6230b96e`.

## Reversão

```sh
docker tag chatisp-pronto-hoje:runtime-before-river-maintain-20261117 chatisp-pronto-hoje:runtime
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-deps --no-build --wait chatisp-api chatisp-worker
docker exec chatisp-pronto-hoje-postgres-1 psql -U chatisp -d chatisp -c "REVOKE MAINTAIN ON TABLE river.river_job FROM chatisp_jobs"
```

Provas privadas: `.tmp/river-maintain-release/`.
