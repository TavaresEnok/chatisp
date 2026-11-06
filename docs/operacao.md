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

## Subir com Docker (instalação por Compose)

Pré-requisitos: Docker com Compose 2.24.4 ou mais novo (por causa de `!override`), ponte de rede padrão com saída para a internet durante o build, `openssl`. Versões **testadas de fato**: Docker 27.5.1 (rootless) com as imagens Debian originais e Docker 20.10.9 com as imagens Alpine de validação. Docker 20.10.10 ou mais novo é **compatibilidade esperada** (corrige o perfil seccomp que nega `clone3`), não testada versão a versão; o 20.10.9 quebra o `apt` do Debian 13, o Node 24 e o `initdb` do `postgres:18.6-trixie`. Prova rápida antes de subir: `docker run --rm debian:13-slim sh -c 'apt-get update >/dev/null && echo ok'`.

```
cp deploy/.env.example deploy/.env      # troque TODOS os placeholders
docker compose -f deploy/compose.yaml --env-file deploy/.env up -d --build
```

Variáveis em `deploy/.env.example`. **O Compose só repassa ao contêiner o que está em `environment:` de `deploy/compose.yaml`**: uma variável presente no `.env` e ausente ali não chega à aplicação. Estão ligadas: banco, chaves de dados, origem do painel (`CHATISP_PANEL_ORIGINS`), SMTP (`CHATISP_SMTP_*`), Meta (`CHATISP_META_*`) e anexos (`CHATISP_ATTACHMENTS_S3_*`). As variáveis `S3_ENDPOINT/S3_REGION/S3_BUCKET` das versões antigas deste Compose **nunca foram lidas pela aplicação** e foram removidas.

O que sobe: PostgreSQL 18.6, Garage 2.5 (nó único, sem redundância de produção), `chatisp-migrate`, `storage-setup` (cria o bucket privado, importa a chave da aplicação e concede leitura e escrita, de forma idempotente), API, worker e Caddy. O painel fica em `https://localhost:8443` com o certificado da CA interna do Caddy; o storage, em `https://storage.localhost:8443` (nome usado nas URLs assinadas). A aplicação confia na CA do storage por `CHATISP_ATTACHMENTS_S3_CA_FILE` (volume `trust`); a verificação de TLS nunca é desligada.

Padrões do Compose: `CHATISP_ENV=production` (a aplicação então recusa simulação da Meta); Meta **desabilitada** (`CHATISP_META_OFFICIAL_DISPATCH=false`: o envio falha antes da rede com `driver_indisponivel`, o painel avisa, o recebimento e o resto seguem); métricas só na rede interna (porta 9090 sem publicação, `/metrics` responde 404 no acesso público); HTTPS só em `127.0.0.1`. Para o envio oficial, `CHATISP_META_OFFICIAL_DISPATCH=true` exige `CHATISP_META_GRAPH_VERSION` e `CHATISP_META_PRIMARY_DOC_URL` (decisão D2-03: o repositório não escolhe versão). Para domínio próprio com certificado público, `docs/implantacao.md`.

Primeiro acesso, uma vez, numa instalação vazia (a senha vai pela entrada padrão e não aparece em argumento, variável nem log):

```
printf '%s\n' "$SENHA_DO_ADMIN" | docker compose -f deploy/compose.yaml --env-file deploy/.env run --rm -T chatisp-migrate bootstrap -email admin@provedor.example -superadmin -storage-bytes 10737418240
```

`-storage-bytes` grava `max_storage_bytes` do plano: **sem limite de armazenamento no plano os anexos são recusados** (409), porque não há padrão comercial. O comando cria o primeiro provedor e o administrador; com `-superadmin` a mesma conta também opera a plataforma, mas **superadmin não opera atendimento por desenho** (sem acesso livre às conversas): para atender, convide um administrador de provedor. Repetido com o mesmo e-mail não altera nada; com outro e-mail numa instalação povoada, recusa. O MFA é exigido no primeiro acesso. Provedores seguintes: o superadmin cria em Configurações › Provedores e usa "Convidar administrador" (exige SMTP em `CHATISP_SMTP_*`; o e-mail leva o link do painel e o token vai só por e-mail). Recuperação de senha também usa o SMTP. `chatisp dev-prepare` é do ambiente de desenvolvimento e não deve ser apontado para o banco do Compose.

Backup e restauração: `docs/backup.md`.

## O que a operação local não faz

- Não usa credencial de Meta, ERP ou Evolution.
- Não aponta migração para banco já existente. Os testes usam o PostgreSQL 18.4 de `backend/tools/pg` (`@embedded-postgres/linux-x64` `18.4.0-beta.17`).
- Não liga expurgo. A política automática continua desligada enquanto a D2-06 estiver aberta.
- Não publica capacidade. O ensaio local é `./scripts/ops-load-local.sh`. A revisão operacional é `./scripts/ops-security-check.sh`.
- O ensaio de restore continua `go run ./cmd/restoredrill` a partir de `backend/`, só com socket de cluster descartável, como em `docs/contracts/m8-restore-drill.md`. O sucesso grava `metrics.prom` no diretório temporário do dump.

## Validar o pacote instalado em ambiente isolado

```
./scripts/compose-validate.sh
```

Projeto, volumes, rede, portas de loopback e etiquetas de imagem próprios (`chatisp-validate-<pid>`), segredos descartáveis gerados na hora; desfaz tudo no fim (`down -v` e remoção das imagens do próprio projeto). Não toca em outros projetos e não faz limpeza global. Requer Node 24 com Playwright (`web/node_modules`) e o Chromium já provisionado.

Prova, em ordem: build; subida com healthchecks; `storage-setup` idempotente; `bootstrap`; jornada no navegador (primeiro acesso e MFA, convite de administrador de outro provedor com e-mail e link, aceite, recuperação de senha, falha de entrega do SMTP, cookies/CSRF/Origin/WebSocket, canal Meta, conversa pelo worker, atualização em tempo real, anexo no Garage por TLS com URL assinada e isolamento entre provedores); envio Meta desabilitado de forma explícita; métricas privadas e portas publicadas; storage parado e de volta; reinício de todos os serviços com persistência; encerramento sem sobras.

Doubles identificados: SMTP descartável (`backend/cmd/smtpsink`) e o remetente do webhook da Meta (a própria jornada, assinando com o segredo do app que cadastra). O resto são os serviços reais.

Variações:

- `CHATISP_VALIDATE_ALPINE=1`: Docker 20.10.9 sem `docker0` e com seccomp que nega `clone3`. Usa `deploy/validacao/Dockerfile.alpine`, `postgres:18.6-alpine`, build na rede do host e sub-rede /24 própria (`CHATISP_VALIDATE_SUBNET`). Prova o Compose, mas **não as imagens Debian** de `deploy/Dockerfile`.
- `CHATISP_VALIDATE_BACKUP=1`: acrescenta pgBackRest, backup completo, exclusão de privacidade depois do backup, perda dos volumes de dados e restauração (`docs/backup.md`).
- Sem variáveis, num Docker que rode as imagens Debian: valida o pacote como é entregue.

Execuções registradas em `docs/medicoes/docker-20261105/` e `docs/medicoes/verificacao-entrega-20261105.md`.

## Atualização da retomada de 05/11/2026

Ao criar o provedor, o formulário permite definir o limite de armazenamento em bytes; sem valor, uploads novos continuam recusados. Configure setor, vínculo da caixa e setor padrão antes de receber mensagens: o worker recusa distribuição sem setor padrão. O superadmin convida o administrador e não recebe permissão de atendimento.

Links diretos e recarregamento aguardam a restauração da sessão antes dos redirecionamentos protegidos. Catálogo Meta real é instalado somente com envio oficial autorizado e versão explícita; o modo desabilitado não faz chamadas Graph. Retenção oferece preview limitado, sem executor de expurgo. Suspensão transitória usa flags/limites explícitos descritos em `docs/contracts/suspended-ingress.md`.

Evidências desta rodada e diferenças em relação às provas anteriores: `docs/medicoes/retomada-20261105/relatorio.md`. Para backup/restore com chaves realmente perdidas e alertas parametrizados, use `docs/backup.md`. Configuração de produção preparada não é produção implantada.
