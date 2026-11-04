# Chat ISP

Webchat de atendimento para provedores de internet: WhatsApp Cloud API oficial e modalidade Web/Evolution opcional; ERP existente integrado por API.

- Especificação normativa: [docs/especificacao.md](docs/especificacao.md) (V4.1, 28/10/2026).
- Referência funcional (fora do build): checkout do Chatwoot em `/home/enok/Documentos/projetos/webchat/chatwoot`, commit `f428ad8e6f`.

Identificadores de código em inglês. Interface, mensagens e documentos em pt-BR.

## Ambiente

- Go 1.27.3, com o módulo em `backend/`.
- Node.js 24 e npm 11.19.1, o npm que acompanha esse Node. O npm 10 de `/usr/local/bin` não deve gravar o lockfile. Os scripts colocam o npm 11 à frente no `PATH` quando ele está instalado ao lado de um Node 24.
- gcc para o que o toolchain Go pedir.
- Neste host não há Docker, make, PostgreSQL de sistema nem root. Os scripts abaixo cobrem esse caso. Onde houver GNU make, `make test` executa `scripts/test.sh` e os outros alvos seguem a mesma regra.

## Sem Docker

Na raiz do repositório:

```sh
./scripts/dev.sh
```

O script instala, se ainda não estiverem no disco, os binários de teste do PostgreSQL 18.4 (`npm ci --prefix backend/tools/pg`), sobe esse servidor só em loopback, aplica as migrações, cria os papéis do banco e um atendente local, e então inicia o processo `chatisp` no modo `all` junto com o Vite. Se a API não ficar pronta, ou se o script receber um sinal, ele encerra só o PostgreSQL, a API e o Vite que ele mesmo iniciou. Um serviço que já escutava na máquina não é parado nem tem a porta trocada.

No padrão, a API fica em `http://127.0.0.1:8080`, as métricas em `127.0.0.1:9090`, o PostgreSQL em `127.0.0.1:55432` e o painel em `http://127.0.0.1:5173`. O Vite encaminha `/api`, `/healthz` e `/readyz` para o endereço da API. A origem do painel acompanha o host e a porta do Vite.

| Variável | Padrão | Uso |
| --- | --- | --- |
| `CHATISP_HTTP_ADDR` | `127.0.0.1:8080` | API (`/healthz`, `/api/...`) |
| `CHATISP_METRICS_ADDR` | `127.0.0.1:9090` | métricas |
| `CHATISP_DEV_PG_HOST` | `127.0.0.1` | PostgreSQL |
| `CHATISP_DEV_PG_PORT` | `55432` | PostgreSQL |
| `CHATISP_DEV_VITE_HOST` | `127.0.0.1` | painel Vite |
| `CHATISP_DEV_VITE_PORT` | `5173` | painel Vite |

Só entram endereços loopback (`127.0.0.0/8` ou `::1`). `0.0.0.0` e nomes de host são recusados. As quatro portas precisam ser distintas. Se alguma já estiver em uso, o script sai com erro. Neste host a porta 8080 costuma ser do Tomcat; escolha outro endereço local:

```sh
CHATISP_HTTP_ADDR=127.0.0.1:18080 \
CHATISP_METRICS_ADDR=127.0.0.1:19090 \
CHATISP_DEV_PG_PORT=55433 \
CHATISP_DEV_VITE_PORT=5174 \
./scripts/dev.sh
```

- Na primeira subida, o terminal mostra o e-mail `dev.local@example.com` e a senha gerada. Ela não é gravada no repositório. Nas subidas seguintes a senha não muda.
- O cookie de sessão é `__Host-session` com a flag `Secure`. O launcher local fala HTTP só para desenvolvimento. Em HTTP o navegador não guarda essa sessão, então o painel aberto nesse endereço não fica autenticado. O script não desliga `Secure` e não cria outro tipo de sessão. Para uma sessão real no navegador, use o Compose com Caddy em `https://localhost:8443`.
- Alternativa sem Docker: `CHATISP_DEV_TLS=1 ./scripts/dev.sh`. O script gera um certificado autoassinado de 1 dia só para loopback (`openssl`, em `/tmp`, apagado ao sair), serve o Vite em HTTPS e passa a origem do painel para `https://`. Abra `https://127.0.0.1:5173/entrar` (ou a porta escolhida), aceite o aviso do certificado e entre; o cookie `__Host-session` continua `Secure`. A API permanece em HTTP loopback atrás do proxy do Vite. Só para desenvolvimento: produção segue com o Caddy.

Atalhos, quando o make existir:

```sh
make dev
make test
make lint
make generate
```

Sem make, os mesmos comandos são `./scripts/dev.sh`, `./scripts/test.sh`, `./scripts/lint.sh` e `./scripts/generate.sh`.

`make generate` atualiza o código do sqlc, `backend/api/openapi.json` e o cliente TypeScript do painel.

## Testes

Para experimentar o atendimento com banco descartável e Meta simulado, veja [o roteiro M2](docs/m2-entrega.md). O comando é `go -C backend run ./cmd/m2demo demo`, após instalar as dependências conforme o roteiro.

O demo inclui setor padrão e vínculo setor/caixa do agente para o recorte M3A. [A entrega M3A](docs/m3a-entrega.md) registra as provas de autorização, contatos, compositor e notificações, a revogação de vínculo populado com histórico preservado e o backlog restante. A migração incremental 00012 separa identidade histórica e autorização operacional.

O [roteiro M3B](docs/m3b-entrega.md) acrescenta fixture opt-in `m2demo m3b` e `npm --prefix web run test:e2e:m3b`, com PostgreSQL descartável, workers, Meta falso e navegador Playwright. O runner aceita `M3B_CHROMIUM_EXECUTABLE` ou detecta `.paseo-build-tmp/browser/chromium-launcher` relativo ao repositório. Em 03/11/2026 o cenário passou no Chromium 140.0.7339.186 provisionado: receber, assumir, transferir, responder, encerrar e revogação entre setores. M3 completo (incluindo auto-reabertura X e CPF/CNPJ autorizado com cifra/HMAC), revisão independente e gate amplo final permanecem pendentes.

O [roteiro M4A](docs/m4a-entrega.md) acrescenta `m2demo m4a` e `npm --prefix web run test:e2e:m4a`, reutilizando o mesmo PostgreSQL embutido e o mesmo Chromium. O runner usa o launcher local, o cache do Playwright ou o Chromium 1193 da conta, e falha sem baixar browser. Em 03/11/2026 o cenário passou no Chromium 140.0.7339.186: respostas, atalho, assinatura, horário por caixa, janela expirada no despacho, template driver aceito na fila com a janela já fechada, recusa `template_not_authorized` do catálogo fake com consentimento ainda válido, opt-out `consent_required` num passo separado e anexo autorizado. M4B, M5 e os marcos M3/M4/M5 inteiros continuam pendentes. Auto-reabertura X e documentos autorizados do M3, o pipeline de mídia e áudio do M4B, round-robin, presença e triagem do M5, e as integrações, relatórios e M8 seguem no backlog.

```sh
./scripts/test.sh
```

Isso equivale a:

```sh
npm ci --prefix backend/tools/pg --no-audit --no-fund
go -C backend vet ./...
go -C backend test -count=1 ./...
npm ci --prefix web --no-audit --no-fund
npm --prefix web run test
```

Os testes de banco usam o PostgreSQL 18.4 desses binários, com dados em `.tmp/testdb/`. Se `CHATISP_TEST_DATABASE_URL` estiver definida, os testes usam esse servidor PostgreSQL 18 e não sobem o embutido. Ausência do binário ou falha ao subir o banco falha o teste. Não há skip silencioso.

O build do painel, separado da suíte:

```sh
npm --prefix web run build
```

`./scripts/lint.sh` confere gofmt, golangci-lint 2.15.0, govulncheck, ESLint, TypeScript e se o OpenAPI e o sqlc gerados estão atualizados. O binário do golangci-lint fica em `.tools/`, fora do Git.

## Com Docker

Requisitos: Docker com Compose, em uma máquina que possa puxar imagens. Este host de desenvolvimento não executa esse caminho.

```sh
cp deploy/.env.example deploy/.env
# edite deploy/.env e troque os placeholders
docker compose -f deploy/compose.yaml --env-file deploy/.env up --build
```

O Compose sobe:

- PostgreSQL `18.6-trixie`, minor fixado. O script de primeira inicialização cria `unaccent`, `pg_trgm` e os papéis `chatisp_owner`, `chatisp_app`, `chatisp_platform` e `chatisp_ingress`, sem `BYPASSRLS`.
- Garage `v2.5.0` como armazenamento S3 só de desenvolvimento, com segredo gerado no volume na primeira subida. Não é redundância de produção. MinIO não faz parte desta instalação.
- `chatisp migrate`, depois os modos `api` e `worker`.
- Caddy na frente, com certificado interno. O painel fica em `https://localhost:8443`.

`deploy/.env.example` só tem placeholders. Segredos reais ficam em `deploy/.env`, que não entra no Git.

Para produção, defina `CHATISP_ENV=production` e a chave `CHATISP_DATA_KEYS` ou `CHATISP_DATA_KEYS_FILE` com `CHATISP_DATA_KEY_VERSION`. O exemplo de Compose deixa `development` para a instalação local com Docker.

Não há chamada a Meta, Evolution ou ERP neste fluxo.
