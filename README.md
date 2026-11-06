# Chat ISP

Webchat de atendimento para provedores de internet: WhatsApp Cloud API oficial e modalidade Web/Evolution opcional; ERP existente integrado por API.

- Especificação normativa: [docs/especificacao.md](docs/especificacao.md) (V4.1, 28/10/2026).
- Referência funcional: [docs/chatwoot-referencia.md](docs/chatwoot-referencia.md).

Identificadores de código em inglês. Interface, mensagens e documentos em pt-BR.

## Ambiente

- Go 1.27.3, com o módulo em `backend/`.
- Node.js 24 e npm 11.19.1, o npm que acompanha esse Node. O npm 10 de `/usr/local/bin` não deve gravar o lockfile. Os scripts colocam o npm 11 à frente no `PATH` quando ele está instalado ao lado de um Node 24.
- gcc para o que o toolchain Go pedir.
- GNU make é opcional: `make test` executa `scripts/test.sh` e os outros alvos seguem a mesma regra.

## Sem Docker

Na raiz do repositório:

```sh
./scripts/dev.sh
```

O script instala, se ainda não estiverem no disco, os binários de teste do PostgreSQL 18.4 (`npm ci --prefix backend/tools/pg`), sobe esse servidor só em loopback, aplica as migrações, cria os papéis do banco e um atendente local, e então inicia o processo `chatisp` no modo `all` junto com o Vite. Se a API não ficar pronta, ou se o script receber um sinal, ele encerra só o PostgreSQL, a API e o Vite que ele mesmo iniciou. Um serviço que já escutava na máquina não é parado nem tem a porta trocada.

No padrão, a API fica em `http://127.0.0.1:8080`, as métricas em `127.0.0.1:9090`, o PostgreSQL em `127.0.0.1:55432` e o painel em `https://127.0.0.1:5173`. O Vite encaminha `/api`, `/healthz` e `/readyz` para o endereço da API. A origem do painel acompanha o host e a porta do Vite.

| Variável | Padrão | Uso |
| --- | --- | --- |
| `CHATISP_HTTP_ADDR` | `127.0.0.1:8080` | API (`/healthz`, `/api/...`) |
| `CHATISP_METRICS_ADDR` | `127.0.0.1:9090` | métricas |
| `CHATISP_DEV_PG_HOST` | `127.0.0.1` | PostgreSQL |
| `CHATISP_DEV_PG_PORT` | `55432` | PostgreSQL |
| `CHATISP_DEV_VITE_HOST` | `127.0.0.1` | painel Vite |
| `CHATISP_DEV_VITE_PORT` | `5173` | painel Vite |

Só entram endereços loopback (`127.0.0.0/8` ou `::1`). `0.0.0.0` e nomes de host são recusados. As quatro portas precisam ser distintas. Se alguma já estiver em uso, o script sai com erro. Para escolher outras portas:

```sh
CHATISP_HTTP_ADDR=127.0.0.1:18080 \
CHATISP_METRICS_ADDR=127.0.0.1:19090 \
CHATISP_DEV_PG_PORT=55433 \
CHATISP_DEV_VITE_PORT=5174 \
./scripts/dev.sh
```

- Na primeira subida, o terminal mostra o e-mail `dev.local@example.com` e a senha gerada. Ela não é gravada no repositório. Nas subidas seguintes a senha não muda.
- O launcher gera por padrão um certificado autoassinado de 1 dia só para loopback (`openssl`, em `/tmp`, apagado ao sair). Abra `https://127.0.0.1:5173/entrar` (ou a porta escolhida), aceite o aviso do certificado e entre; o cookie `__Host-session` continua `Secure`. A API permanece em HTTP loopback atrás do Vite. Para uma instalação local persistente, use o Compose com Caddy em `https://localhost:8443`.
- `CHATISP_DEV_TLS=0 ./scripts/dev.sh` habilita HTTP apenas para diagnóstico. Em HTTP o navegador não guarda essa sessão, então o painel não fica autenticado; a flag `Secure` não é alterada.

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

O [roteiro M3B](docs/m3b-entrega.md) descreve a fixture `m2demo m3b` e `npm --prefix web run test:e2e:m3b`, com PostgreSQL descartável, workers, Meta falso e Playwright.

O [roteiro M4A](docs/m4a-entrega.md) descreve `m2demo m4a` e `npm --prefix web run test:e2e:m4a`. Para limitações e homologações ainda abertas, consulte [dependências externas](docs/dependencias-externas.md).

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

Requisitos: Docker Engine 20.10.10 ou mais novo, Compose 2.24.4 ou mais novo e `openssl`. O build precisa baixar imagens e dependências. As versões efetivamente testadas e suas limitações estão em [operação](docs/operacao.md).

```sh
./deploy/init-local-env.sh
docker compose -f deploy/compose.yaml --env-file deploy/.env up -d --build
docker compose -f deploy/compose.yaml --env-file deploy/.env ps
```

O Compose sobe:

- PostgreSQL `18.6-trixie`, minor fixado. O script de primeira inicialização cria `unaccent`, `pg_trgm` e os papéis `chatisp_owner`, `chatisp_app`, `chatisp_platform` e `chatisp_ingress`, sem `BYPASSRLS`.
- Garage `v2.5.0` como armazenamento S3 só de desenvolvimento, com segredo gerado no volume na primeira subida. Não é redundância de produção. MinIO não faz parte desta instalação.
- `chatisp migrate`, depois os modos `api` e `worker`.
- Caddy na frente, com certificado interno. O painel fica em `https://localhost:8443`.

O gerador cria `deploy/.env` com segredos aleatórios e permissão 0600; ele recusa sobrescrever um arquivo existente. Preserve esse arquivo em local seguro: as chaves são necessárias para recuperar dados cifrados. Os volumes persistem entre reinícios; `down -v` os apaga. `deploy/.env.example` documenta as opções e contém apenas placeholders.

Abra `https://localhost:8443`. O certificado é de uma CA interna do laboratório, não reconhecida automaticamente pelo navegador; confira a CA da instalação antes de autorizá-la no navegador. A raiz pública pode ser copiada, sem a chave privada, com `docker compose -f deploy/compose.yaml --env-file deploy/.env cp caddy:/data/caddy/pki/authorities/local/root.crt /tmp/chatisp-local-ca.crt`. Não instale essa CA globalmente sem uma decisão explícita. O storage usa `storage.localhost:8443`.

Primeiro administrador de provedor, uma vez, com senha lida sem eco. Informe um e-mail e escolha um limite de bytes para anexos; sem limite os anexos são recusados. O exemplo abaixo usa um limite sintético para o laboratório:

```bash
read -r -s -p 'Senha do administrador: ' SENHA_DO_ADMIN; echo
printf '%s\n' "$SENHA_DO_ADMIN" | docker compose -f deploy/compose.yaml --env-file deploy/.env run --rm -T chatisp-migrate bootstrap -email admin@exemplo.local -storage-bytes 1073741824
unset SENHA_DO_ADMIN
```

No primeiro acesso, configure MFA. Sem SMTP configurado, convites e recuperação de senha ficam indisponíveis. A opção `-superadmin` cria uma conta de plataforma que não opera o atendimento; nesse caso, é preciso SMTP para convidar um administrador de provedor. O bootstrap não promove uma conta existente numa instalação já criada. Consulte [operação](docs/operacao.md) antes de escolher o papel inicial.

O Compose usa `CHATISP_ENV=production` também no laboratório, mantém o envio oficial Meta desligado e publica HTTPS apenas em loopback. O serviço Garage local é de nó único e não substitui backup independente. Para domínio próprio e certificado público, consulte [implantação](docs/implantacao.md). Validação completa em ambiente isolado: `./scripts/compose-validate.sh`.

### Laboratório com Docker 20.10.9

Em hosts com Docker 20.10.9, o perfil abaixo usa bases Alpine e rede do host apenas durante o build, para contornar a incompatibilidade do seccomp antigo com as imagens Debian. É uma instalação local persistente de laboratório, sem domínio público nem implantação de produção. Escolha um nome exclusivo para o projeto e o prefixo de imagem; use os mesmos argumentos em todos os comandos da instalação. As portas, a CA interna e os volumes continuam os do Compose base.

```sh
./deploy/init-local-env.sh
export CHATISP_LOCAL_IMAGE_PREFIX=chatisp-pronto-hoje
docker compose -p chatisp-pronto-hoje -f deploy/compose.yaml -f deploy/compose.local-alpine.yaml --env-file deploy/.env config -q
docker compose -p chatisp-pronto-hoje -f deploy/compose.yaml -f deploy/compose.local-alpine.yaml --env-file deploy/.env up -d --build
```

Para criar o primeiro administrador, use a leitura de senha e os argumentos de `bootstrap` mostrados acima, acrescentando ao comando Compose os mesmos `-p` e `-f` deste perfil. Se a porta 8443 já estiver ocupada, escolha uma porta livre e ajuste em `deploy/.env` as três variáveis `CHATISP_PUBLIC_PORT`, `CHATISP_PANEL_ORIGINS=https://localhost:<porta>` e `CHATISP_ATTACHMENTS_S3_PUBLIC_ENDPOINT=https://storage.localhost:<porta>` antes da subida. Não altere `CHATISP_PUBLIC_BIND` para fora de loopback. O perfil não configura SMTP e não ativa simulação da Meta.

Não há chamada a Meta, Evolution ou ERP neste fluxo.
