# Guia de operação em desenvolvimento

Os comandos abaixo foram conferidos em `scripts/dev.sh`, `scripts/vite.dev.mjs`, `README.md`, `web/vite.config.ts` e `deploy/compose.yaml`. Portas diferentes das citadas só valem quando o ambiente as define. O script recusa endereço que não seja loopback e recusa porta já ocupada; ele não para o processo que já escuta.

## Subir sem Docker

Na raiz do repositório:

```
./scripts/dev.sh
```

Na primeira subida o terminal imprime `dev.local@example.com` e uma senha gerada. A senha não entra no Git. Na subida seguinte a senha não muda.

| Serviço | URL padrão |
| --- | --- |
| API | `http://127.0.0.1:8080` |
| Saúde | `http://127.0.0.1:8080/healthz` e `http://127.0.0.1:8080/readyz` |
| Métricas | `http://127.0.0.1:9090/metrics` |
| PostgreSQL embutido | `127.0.0.1:55432` |
| Painel Vite | `http://127.0.0.1:5173` |

O Vite deste script usa `scripts/vite.dev.mjs` e encaminha `/api`, `/healthz` e `/readyz` para `CHATISP_DEV_API_ORIGIN`, que o script define com a mesma origem da API. No padrão, o painel em `http://127.0.0.1:5173` fala com `http://127.0.0.1:8080`.

Rotas do painel, nesse host:

- `http://127.0.0.1:5173/entrar`
- `http://127.0.0.1:5173/painel`
- `http://127.0.0.1:5173/conversas`
- `http://127.0.0.1:5173/relatorios`
- `http://127.0.0.1:5173/configuracoes` (alertas operacionais do admin)

O cookie de sessão é `__Host-session` com `Secure`. Em HTTP o navegador não guarda essa sessão, então o painel aberto em `http://127.0.0.1:5173` não permanece autenticado. O script não desliga `Secure`.

Para entrar no navegador sem Docker, suba com HTTPS local:

```
CHATISP_DEV_TLS=1 ./scripts/dev.sh
```

O painel passa a ser `https://127.0.0.1:5173` (ou a porta de `CHATISP_DEV_VITE_PORT`) com certificado autoassinado descartável, apagado ao encerrar. Conferido em 04/11/2026: login `POST /api/panel/session` pelo proxy HTTPS retornou 200 com `__Host-session` (`HttpOnly; Secure; SameSite=Lax`) e `__Host-csrf`. Para encerrar, Ctrl+C no terminal do script; ele para só o PostgreSQL, a API e o Vite que iniciou.

Se a porta 8080 estiver ocupada, o próprio `scripts/dev.sh` documenta o conjunto:

```
CHATISP_HTTP_ADDR=127.0.0.1:18080 \
CHATISP_METRICS_ADDR=127.0.0.1:19090 \
CHATISP_DEV_PG_PORT=55433 \
CHATISP_DEV_VITE_PORT=5174 \
./scripts/dev.sh
```

Nesse caso a API fica em `http://127.0.0.1:18080`, as métricas em `http://127.0.0.1:19090/metrics`, o PostgreSQL em `127.0.0.1:55433` e o painel em `http://127.0.0.1:5174`. As quatro portas precisam ser distintas.

Conferir a saúde:

```
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:9090/metrics
```

Troque o host e a porta se tiver usado as variáveis acima.

## Subir com Docker

Este host de desenvolvimento não executa esse caminho. Em uma máquina com Docker e Compose:

```
cp deploy/.env.example deploy/.env
docker compose -f deploy/compose.yaml --env-file deploy/.env up --build
```

O painel com Caddy fica em `https://localhost:8443`. `deploy/.env.example` só tem placeholders. Não grave segredo real no Git.

Requisitos do host: Docker Engine 20.10.10 ou mais novo (o 20.10.9 nega `clone3` com EPERM e quebra o `apt` do Debian 13 e o Node 24), ponte padrão funcional com saída para a internet durante o build, Compose 2.24.4 ou mais novo. Prova rápida antes de subir: `docker run --rm debian:13-slim sh -c 'apt-get update >/dev/null && echo ok'`.

Primeiro acesso, uma vez, numa instalação vazia (a senha vai pela entrada padrão e não aparece em argumento, variável nem log):

```
printf '%s\n' "$SENHA_DO_ADMIN" | docker compose -f deploy/compose.yaml --env-file deploy/.env run --rm -T chatisp-migrate bootstrap -email admin@provedor.example -superadmin
```

O comando cria o primeiro provedor e o administrador (com `-superadmin`, também o operador da plataforma). Repetido com o mesmo e-mail, não altera nada; com outro e-mail numa instalação povoada, recusa. Entre em `https://localhost:8443/entrar`; o MFA é exigido no primeiro acesso. Provedores seguintes: o superadmin cria em Configurações › Provedores e usa "Convidar administrador" (exige SMTP em `CHATISP_SMTP_*`; o token vai só por e-mail). `chatisp dev-prepare` é do ambiente de desenvolvimento e não deve ser apontado para o banco do Compose.

## O que a operação local não faz

- Não usa credencial de Meta, ERP ou Evolution.
- Não aponta migração para banco já existente. Os testes usam o PostgreSQL 18.4 de `backend/tools/pg` (`@embedded-postgres/linux-x64` `18.4.0-beta.17`).
- Não liga expurgo. A política automática continua desligada enquanto a D2-06 estiver aberta.
- Não publica capacidade. O ensaio local é `./scripts/ops-load-local.sh`. A revisão operacional é `./scripts/ops-security-check.sh`.
- O ensaio de restore continua `go run ./cmd/restoredrill` a partir de `backend/`, só com socket de cluster descartável, como em `docs/contracts/m8-restore-drill.md`. O sucesso grava `metrics.prom` no diretório temporário do dump.

## Validar Compose e Caddy em ambiente isolado

```
./scripts/compose-validate.sh
```

O script usa projeto, volumes, rede e etiquetas de imagem próprios (`chatisp-validate-<pid>`), segredos descartáveis gerados na hora, porta de loopback livre para o Caddy, e se desfaz no fim (`down -v`). Não toca em outros projetos. Requer Docker com a ponte `docker0` e versão que rode imagens Debian 13/Node 24 (20.10.10 ou mais nova) e Compose 2.24.4 ou mais novo (por causa de `!override`).

Em host com Docker 20.10.9 (seccomp nega `clone3`), valide com bases musl, que não dependem disso:

```
CHATISP_VALIDATE_ALPINE=1 ./scripts/compose-validate.sh
```

Esse modo usa `deploy/validacao/Dockerfile.alpine` e `postgres:18.6-alpine`, build na rede do host e uma sub-rede /24 própria (`CHATISP_VALIDATE_SUBNET` para escolher). Ele prova o Compose, as migrações, o HTTPS do Caddy, o bootstrap, o login, o reinício e o encerramento, mas não as imagens Debian de `deploy/Dockerfile`. Verde em 05/11/2026 neste host. A variante Debian (sem a variável) ficou verde no mesmo dia num Docker 27.5.1 rootless descartável; histórico, defeitos achados (Caddyfile sem nome de host) e logs em `docs/medicoes/verificacao-entrega-20261105.md` e `docs/medicoes/docker-20261105/`.
