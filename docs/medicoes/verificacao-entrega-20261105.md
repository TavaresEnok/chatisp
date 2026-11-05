# Verificação de entrega — 05/11/2026

Escopo: fechar Docker/Compose e instalação, documentar o incidente de limpeza, conferir a matriz e preservar a evidência de segurança. Funcionalidades já verificadas não foram reabertas. Nenhum push, deploy, expurgo, chamada real ou mudança em serviço compartilhado.

## 1. Docker/Compose

### Host

| Item | Valor |
| --- | --- |
| Engine | Docker 20.10.9 (cliente e servidor, API 1.41), containerd 1.6.9, runc 1.1.4 |
| Compose / Buildx | v2.24.7 / v0.6.3 |
| SO / kernel | Ubuntu 20.04.6, 5.17.15-pnetlab-uksm-2, cgroup v1, libseccomp2 2.5.1 |
| Daemon | `/etc/systemd/system/docker.service.d/service.conf` (desde 03/2020): `ExecStart=/usr/bin/dockerd --iptables=false`; `daemon.json` com `default-address-pools` 10.177.0.0/16 e API em `tcp://127.0.0.1:4243` |
| Rede padrão | a rede `bridge` aponta para `docker0` (`com.docker.network.bridge.name`), mas a interface `docker0` não existe (`ip link show docker0`: "Device does not exist"). Sem regra NAT para 10.177.0.0/16 (o daemon não cria, por `--iptables=false`) |

### Causa concreta (reproduzida com a sonda `docker-20261105/sonda.Dockerfile`)

Duas falhas independentes. A ausência de `docker0` sozinha não explica o build.

1. **Rede do build.** Todo `RUN` na rede padrão falha ao criar o endpoint: `failed to create endpoint ... on network bridge: adding interface veth... to bridge docker0 failed: could not find bridge docker0` (BuildKit e builder clássico: `probe-buildkit-default.txt`, `probe-classic-default.txt`).
2. **Seccomp do 20.10.9 com glibc ≥ 2.34.** Com `--network host` a rede funciona (DNS e download do índice do apt), mas:
   - `apt-get update` do Debian 13 termina com `E: Problem executing scripts APT::Update::Post-Invoke` (`probe-buildkit-host.txt`). O mesmo comando em `docker run` passa com `--security-opt seccomp=unconfined` (rc 0) e falha com o perfil padrão (rc 100).
   - `node` 24 (bookworm, glibc 2.36) aborta já em `node -e 'console.log(1)'`: `Assertion failed: (0) == (uv_thread_create(...))`; com `seccomp=unconfined` executa.
   - O perfil padrão do 20.10.9 nega `clone3` com EPERM; a glibc só recua para `clone` com ENOSYS. O perfil upstream de 20.10.24 (que devolve ENOSYS via `errnoRet: 38`) **não resolveu neste daemon**: o Node continuou abortando, ou seja, o 20.10.9 não aplica o `errnoRet` desse perfil. Perfil permissivo sem regras (`defaultAction: ALLOW`) faz o Node rodar, o que confirma seccomp como causa.
   - Em execução isso também afeta qualquer binário com threads da glibc nova dentro dos contêineres (o `ffmpeg` do Debian 13 na imagem da API, por exemplo). PostgreSQL 18 (trixie) e o binário Go estático não foram afetados nas sondas.

Conclusão: o host não é compatível para build nem para execução fiel. A correção é Docker ≥ 20.10.10 (perfil com `clone3` → ENOSYS) e uma rede de build funcional, o que exigiria atualizar ou reconfigurar o Docker compartilhado. Isso não foi feito.

### Ambiente descartável

- Nenhum ambiente descartável autorizado foi encontrado. A rede `paseo-workers` (`br-paseowork`, egresso só para endereços públicos) é a rede dos trabalhadores do orquestrador e não oferece um Docker próprio.
- Tentativa: BuildKit rootless em contêiner próprio (`moby/buildkit:v0.16.0-rootless`, rede do host). Exige `seccomp=unconfined` e `apparmor=unconfined` no contêiner do builder; a execução foi **negada** pela política de permissões desta sessão, e não houve contorno. A imagem baixada foi removida (`docker rmi moby/buildkit:v0.16.0-rootless`, conferido: nenhum contêiner a usava).
- Na primeira parte, portanto, nada disso foi executado. Ver "Validação executada" abaixo.

### Validação executada (05/11/2026, 15:24–15:33 UTC)

O builder com seccomp desligado foi negado de novo. Caminho adotado, sem mexer no daemon nem desligar proteção: a falha do seccomp atinge só a glibc nova (`clone3`), e o musl não usa `clone3`. Uma sonda `node:24.22.1-alpine` com `npm`/`apk` e `--network host` passou. Por isso:

- `deploy/validacao/Dockerfile.alpine`: mesmos estágios, fontes e versões de Node (24.22.1), Go (1.27.3) e Caddy (2.10.2) do `deploy/Dockerfile`; muda só a base (Alpine 3.22 no runtime). Build na rede do host.
- `CHATISP_VALIDATE_ALPINE=1 ./scripts/compose-validate.sh` usa esse Dockerfile e `postgres:18.6-alpine` (mesma versão 18.6). O restante é o `deploy/compose.yaml` como está: Garage, init do Garage em `debian:13-slim`, migrate, api, worker, Caddy, healthchecks e dependências.

Tentativas até o verde (resumos em `docker-20261105/compose-validate-tentativa-*.txt`):

1. Rede: `could not find an available, non-overlapping IPv4 address pool`. O `default-address-pools` do daemon (10.177.0.0/16 com tamanho /16) só comporta a rede `bridge`. O script passou a escolher uma /24 livre em 172.31.240–254 (sem rota nem rede Docker), sobrescrevível por `CHATISP_VALIDATE_SUBNET`.
2–3. `postgres:18.6-trixie` unhealthy: `initdb` falha com `could not execute command "postgres -V": Operation not permitted`. É o mesmo `clone3` negado, agora em tempo de execução. A imagem de banco do Compose também não roda em Docker 20.10.9. No modo de validação, `postgres:18.6-alpine`.
4. **Defeito do produto:** `curl: (35) ... tlsv1 alert internal error` no Caddy. O `deploy/Caddyfile` tinha só o site `:443` com `tls internal`: sem nome de host, o emissor interno não tem para quem emitir, e o handshake falha também em `https://localhost:8443`. Corrigido para `localhost:443, 127.0.0.1:443` com `default_sni localhost` (comentário no arquivo diz como acrescentar um domínio). Teste de guarda: `TestCaddyfileNamesTheInternalCertificate`, que falha com o `:443` antigo.
5. Verde. Depois disso o script passou a construir cada etiqueta uma vez (`build chatisp-migrate caddy` + `up --no-build`) e a remover as imagens que ele mesmo baixar.
6. Verde de novo com o script final (`docker-20261105/compose-validate-final.txt`, passos do build em `compose-validate-final-build-steps.txt`):

```
subida ok: postgres, garage, migrações, api, worker e caddy (https://127.0.0.1:48077)
login ok pelo Caddy com o administrador do bootstrap
reinício ok: o mesmo administrador entra e o bootstrap não recria nada
compose ok: build, healthchecks, migrações, HTTPS, login, reinício e encerramento sem sobras (chatisp-validate-320763)
```

Provado: build das imagens a partir das fontes, subida com todos os healthchecks (`postgres`, `garage`, `chatisp-api`, `chatisp-worker`, `caddy`), migrações concluídas (`chatisp-migrate` saiu com 0), HTTPS pelo Caddy (`/healthz`, `/readyz`, painel), `chatisp bootstrap -superadmin` dentro do Compose, login `POST /api/panel/session` com `__Host-session`, reinício com o mesmo login e o bootstrap repetido sem recriar nada, e `down -v` sem contêiner, volume nem rede do projeto.

### Validação com as imagens Debian originais (05/11/2026, 16:00–16:10 UTC)

Autorizado pelo usuário ("faça o que for necessário"). O Docker compartilhado não foi atualizado nem reiniciado, porque isso pararia os contêineres em execução de outros projetos. Contêiner privilegiado (dind) e builder com seccomp desligado foram negados pela política de permissões da sessão. Caminho usado, sem privilégio:

- Pacotes `uidmap` e `slirp4netns` instalados via apt (pré-requisitos do modo rootless) e **desinstalados** no fim.
- Binários estáticos oficiais em diretório temporário: Docker 27.5.1 e extras rootless (`download.docker.com`), Compose v2.32.4 e Buildx v0.20.1 (GitHub). Daemon rootless rodando como o usuário `enok`, com seccomp `builtin` (o perfil novo, que devolve ENOSYS em `clone3`), dados, socket e configuração próprios.
- Prova prévia nesse daemon: `apt-get update` no `debian:13-slim` e `node -e ... execSync` no `node:24.22.1-bookworm-slim`, as duas coisas que falhavam no 20.10.9, passaram.
- `./scripts/compose-validate.sh` **sem** `CHATISP_VALIDATE_ALPINE`, com `deploy/Dockerfile` e `deploy/compose.yaml` sem nenhuma alteração. Build: `node:24.22.1-bookworm-slim` (`npm ci`, `npm run build`), `golang:1.27.3-bookworm`, `debian:13-slim` (`apt-get install ca-certificates curl ffmpeg`), `caddy:2.10.2`; banco `postgres:18.6-trixie`. Passou de primeira (`docker-20261105/compose-validate-debian.txt` e `compose-validate-debian-build-steps.txt`):

```
docker 27.5.1 / Docker Compose version v2.32.4
subida ok: postgres, garage, migrações, api, worker e caddy (https://127.0.0.1:36653)
login ok pelo Caddy com o administrador do bootstrap
reinício ok: o mesmo administrador entra e o bootstrap não recria nada
compose ok: build, healthchecks, migrações, HTTPS, login, reinício e encerramento sem sobras (chatisp-validate-877457)
```

- Desmontagem: daemon rootless parado (PID conferido), dados (4,3 GB, com as imagens que ele baixou) apagados de dentro do namespace de usuário, binários e link `/tmp/cvrl` removidos, pacotes desinstalados. O Docker compartilhado ficou como estava: 20.10.9, ativo, nenhum recurso `chatisp-validate`, as mesmas 7 imagens sem etiqueta anteriores.

Requisito de instalação confirmado: Docker Engine ≥ 20.10.10. O modo rootless do Docker atual também serve.

### Procedimento exato num host compatível

Requisitos: Docker Engine ≥ 20.10.10 com a ponte padrão funcional e NAT de saída para o build, Compose ≥ 2.24.4, `openssl`, `python3`, `curl`.

```
git clone <repositório> && cd chat-isp && git checkout <commit desta entrega>
docker version --format '{{.Server.Version}}'      # >= 20.10.10
docker run --rm debian:13-slim sh -c 'apt-get update >/dev/null && echo ok'   # prova seccomp/rede
./scripts/compose-validate.sh
```

O script (reescrito nesta rodada) cobre: build com etiquetas próprias (`chatisp-validate-<pid>:runtime` e `:edge`), subida com `--wait` (healthchecks e `chatisp-migrate` concluído), `/healthz`, `/readyz` e painel pelo Caddy em HTTPS, `chatisp bootstrap -superadmin` com senha pela entrada padrão, login `POST /api/panel/session` pelo Caddy com cookie `__Host-session`, `restart` com novo login (dados preservados) e repetição do bootstrap sem recriar nada, e `down -v` com remoção das duas etiquetas e conferência de que não sobrou contêiner, volume nem rede com o rótulo do projeto. Neste host ele continua sem execução; só a sintaxe (`bash -n`) e o override (`docker compose config`) foram conferidos.

### Lacuna do primeiro usuário: implementada

Encontrada nesta verificação e fechada na mesma rodada, a pedido:

- `chatisp bootstrap -email <e-mail> [-superadmin] [-timezone <IANA>]` (`backend/cmd/chatisp/bootstrap.go`). Lê a senha da primeira linha da entrada padrão, nunca de argumento nem de variável. Só roda em instalação vazia: com o mesmo e-mail já ativo, não altera nada; com outro e-mail numa instalação povoada, recusa. Cria provedor, usuário e vínculo `invited` numa transação de plataforma, grava a senha, marca o superadmin com a conexão do dono (a única com permissão para `is_superadmin`) e ativa o vínculo com auditoria `platform.bootstrap` em `platform_audit_logs`. Uma execução interrompida deixa um vínculo `invited`, que não entra, e a próxima com o mesmo e-mail a conclui enquanto ele for o único usuário; numa instalação povoada um convite pendente não é ativado pelo bootstrap (só pelo token). O MFA continua obrigatório no primeiro acesso do admin. No Compose: `printf '%s\n' "$SENHA" | docker compose -f deploy/compose.yaml --env-file deploy/.env run --rm -T chatisp-migrate bootstrap -email admin@provedor -superadmin`.
- `POST /api/platform/tenants/{tenant_id}/admin-invitations` (`auth.InviteProviderAdmin`): o superadmin convida o primeiro administrador de um provedor criado pelo painel. O token vai por e-mail, nunca na resposta; provedor inexistente responde 404 e suspenso responde 409; auditoria `tenant.admin_invite`. O envio reaproveita o mesmo caminho do convite do painel (`sendInvite`). A interface ganhou "Convidar administrador" em cada provedor ativo (`platform-admin.tsx`).
- Testes: `TestBootstrapFirstAdminOnEmptyInstallation` e `TestBootstrapResumesInterruptedRun` (PostgreSQL descartável: recusas antes de gravar, login, `EnrollmentOnly`, superadmin, auditoria, repetição, segundo e-mail e retomada); `TestSuperadminInvitesFirstProviderAdmin` e `TestProviderAdminInviteRefusals` (convite, aceite, papel admin no provedor novo, recusas); `roles_test.go` (403 para admin/supervisor/atendente, 201, 404, 409 pela API); `auth-flows.test.tsx` (formulário e CSRF).
- Validação: `go vet ./...` e `go test -count=1 ./...` verdes; `scripts/lint.sh` verde (golangci-lint, govulncheck, ESLint, tsc, OpenAPI e sqlc em dia); Vitest 60 arquivos e 288 testes; E2E `ux` (axe) e `m7-ops` passaram.

### Recursos criados por esta tarefa (05/11/2026)

| Recurso | Situação |
| --- | --- |
| Imagem `moby/buildkit:v0.16.0-rootless` (`dd70a5f3784e`, baixada 14:42 UTC) | removida individualmente |
| Contêineres das sondas (`docker run --rm`) | removidos pelo `--rm` |
| Builds da sonda (`chatisp-probe-net:*`) | falharam no `RUN`; nenhuma etiqueta nem cache gerado |
| Contêiner `determined_hermann` (`172052ca40ee`, `npm ci` do build clássico de 10:07:45) e imagem sem etiqueta `933e4b0aa2d1` (estágio `web`), da rodada anterior | removidos individualmente, junto com as três camadas sem etiqueta da mesma cadeia (`4b49218aed9c`, `6fe80c31333e`, `7e5c646f37c8`) |
| Projetos `chatisp-validate-*` (6 execuções) | contêineres, volumes e rede removidos por `down -v`; conferido por rótulo: nenhum sobrou |
| Etiquetas `chatisp-validate-*:runtime` e `:edge` | removidas pelo script |
| 10 imagens sem etiqueta criadas às 15:25:38 (`a4c33921d28a`, `12aaed8f12ef`, `7622eb3b3728`, `6e4465aebc22`, `0a12c7afeeea`, `e1c81aaa4e48`, `81279bf79229`, `2c850e2886d4`, `e240dc08d47f`, `95c0634276ee`), vindas dos três serviços que construíam a mesma etiqueta | conferidas uma a uma (histórico com `chatisp`, nenhum contêiner) e removidas; o script agora constrói uma vez |
| `postgres:18.6-alpine` (ausente antes) | removido; o script remove as imagens que ele mesmo baixar |
| Imagem da sonda `chatisp-probe-alpine:1` | removida |
| Cache de build de 05/11 (lista em `docker-20261105/build-cache-20261105.txt`) | removido por ID (`docker builder prune -f --filter id=<ID>`, em passadas, depois da autorização): de 2,1 GB para 867 kB. Ficaram 6 registros de 0 B ("from local"), referências a camadas de imagens locais encadeadas a um registro de 22/10 de outro projeto |
| Ambiente rootless (Docker 27.5.1, dados de 4,3 GB, binários, link `/tmp/cvrl`) e pacotes `uidmap`/`slirp4netns` | removidos/desinstalados |

## 2. Incidente de limpeza

Fonte: transcrição da sessão anterior (`~/.claude/projects/-home-enok-srv-projetos-workspace-chat-isp/7a023921-...jsonl`).

- **Comando**: `docker image prune -f --filter "until=2m" 2>&1 | tail -1`, executado em 05/11/2026 às 10:08:19 UTC (resultado às 10:08:37), depois do build com falha.
- **Saída registrada**: só `Total reclaimed space: 2.425GB`. O `| tail -1` descartou a lista de imagens apagadas; **os IDs removidos não constam de nenhum registro**. O daemon não registra `prune` no journal em nível info.
- **Erro do comando**: `until=2m` seleciona imagens criadas **há mais** de 2 minutos, o contrário do que se queria. Por isso apagou imagens sem etiqueta antigas de outros projetos e manteve a sobra recente do próprio build (`933e4b0aa2d1`).
- **O que se pode afirmar**: a listagem `docker images | head -20` de 10:05:01 mostrava quatro imagens sem etiqueta que não aparecem na listagem de 10:08:42 (`docker images -f dangling=true`): 1,02 GB (logo após `nexoisp-paseo-runtime:browser-20261103`), 2,26 GB (após `ac-synthetic-worker:20261101`), 2,18 GB (após `ac-remediation-complete:20261101`) e 1,32 GB (entre as etiquetas `agent-control-f0-access:*`). A posição e o tamanho sugerem versões anteriores dessas etiquetas, recriadas depois; isso é inferência, não registro. Imagens além da 20ª linha da listagem anterior não são conhecidas. Os 2,425 GB recuperados contam só camadas exclusivas.
- **Sobreviventes sem etiqueta** (presentes antes e depois): `fc1ae41d3a46`, `e5f30791e63e`, `d66d66c0fefb`, `28ad402eb069`, `b9cbecdbdee1`, `03f2411a6da3`, `06f5cb493aff`.

### Impacto verificado agora

- Os 80 contêineres do host (27 em execução) têm a imagem presente: `docker inspect -f '{{.Image}}'` de cada um resolve com `docker image inspect`.
- Toda referência `Config.Image` usada por contêineres resolve localmente (nenhuma etiqueta ausente).
- Um `prune` não remove imagem usada por contêiner, então nenhum contêiner perdeu a imagem e todos podem reiniciar. As etiquetas vizinhas continuam presentes, então `compose up` sem `--build` desses projetos segue possível.
- Não há imagem identificada para recuperar. Se algum projeto dependia de uma versão anterior sem etiqueta, a recuperação é reconstruir a partir do commit correspondente, com o `docker build` do próprio projeto. Isso não foi feito, para não interromper serviços nem tocar outros projetos.
- Nenhuma limpeza global foi executada de novo.

## 3. Matriz final

Contagem em `docs/matriz-requisitos.md`: 207 verificados, 33 aguardando homologação externa, 17 parciais, 0 não implementados (257). Mudanças: 14 linhas saíram de "aguardando homologação" por terem implementação faltante (adaptador real do ERP e conector Evolution). 12-superadmin e 14-provisionamento saíram de "verificado" pela lacuna do primeiro usuário e voltaram depois de `chatisp bootstrap` e do convite do admin do provedor, com testes. 12-https, 13-self-host e 13-compose passaram a verificados com a validação do Compose (bases musl no Docker do host e imagens Debian originais no Docker 27.5.1 rootless).

### Os seis parciais anteriores

| Requisito | Código que já existe | O que falta implementar | Informação ou ambiente externo | Prova que encerra |
| --- | --- | --- | --- | --- |
| 12-https | `deploy/Caddyfile` corrigido (site com nome, `default_sni`), cookies `__Host-session` com `Secure; HttpOnly; SameSite=Lax` | nada | nada | **fechado**: login pelo Caddy em HTTPS no `compose-validate.sh` (05/11, 15:33) |
| 13-self-host | `deploy/compose.yaml`, `deploy/Dockerfile`, `.env.example` com teste de guarda, `compose-validate.sh`, `chatisp bootstrap` e convite do admin do provedor | nada | nada | **fechado**: `compose-validate.sh` com as imagens Debian originais em Docker 27.5.1 (05/11, 16:10) |
| 13-compose | idem | nada | nada | **fechado**: idem |
| 08-aprovacao | ação sensível oculta (`TestSensitiveStaysHiddenWithoutVerification`); auditoria de ação ERP | mecanismo de aprovação (operação, parâmetros, hash, prazo, aprovador, uso único), junto com a primeira ação sensível | D2-01 (ERP) e D2-02 (verificação do cliente) | testes do mecanismo: uso único, mudança de parâmetro invalida, prazo |
| 08-verificacao-cliente | a ação sensível fica oculta sem verificação | reutilizar o mecanismo do provedor | D2-02 | teste com o mecanismo real ou seu contrato documentado |
| 14-carga | `internal/loadlocal`, `scripts/ops-load-local.sh` (recusa-se como capacidade) | roteiro k6 com o perfil | D2-07 (perfil, metas, RPO/RTO) | relatório k6 com p95, CPU, RAM e I/O medidos |

### Os 47 "aguardando homologação": conferência

Havia implementação faltante escondida em 14 linhas, agora "Parcial":

- **ERP (13 linhas: 07-id-erp, 07-vinculo-erp, 08-referencia-erp, 08-registro-acao, 08-permissao-erp, 08-contrato-minimo, 08-conector-erp, 08-identificar, 08-resumo, 08-faturas, 08-segunda-via, 08-protocolo-erp, 11-conector-erp).** Existem contrato, fake identificado, persistência, guarda, disjuntor, painel e fluxo. Mas `backend/internal/erp/real.go` é um stub: `SupplierEnabled()` devolve `false` e `supplier.Call` devolve `ErrDown` sem abrir conexão. O plano pede "um adaptador" do ERP existente. Falta implementar esse adaptador; para isso faltam versão, API e autenticação (D2-01). Prova: testes do adaptador contra o contrato documentado e homologação com o ERP do provedor.
- **01-evolution.** `channel/whatsapp/evolution/driver.go` é o contrato local (classes de evento, QR efêmero só para admin, flag desligada) e declara que não abre conexão. Falta o conector de rede; depende de D2-04. Prova: testes do conector com a flag ligada em ambiente de homologação.

As 33 restantes têm o código de produção completo e só esperam o externo:

- **Meta (27 linhas, D2-03)**: o driver `metacloud` fala HTTP real (envio, mídia, templates, qualidade), exercido contra servidor falso. Falta app, WABA, número, permissão, credencial e a versão da Graph API.
- **08-abrir-cadastro**: modelo de link configurável pelo admin; falta só o endereço do ERP do provedor (configuração, não código).
- **14-s3, 14-url-assinada**: SDK S3 real, exercido com transporte simulado e Garage local; falta o armazenamento de produção.
- **12-retencao, 14-retencao**: classes e recusa implementadas; o plano manda o expurgo ficar desligado até D2-06.
- **14-backup**: restore testado em descartável; falta backup de produção e RPO/RTO medidos (D2-07).

## 4. Segurança

Evidência em `docs/medicoes/govulncheck-20261105/` (rodada de 05/11/2026, 14:45 UTC):

- `versoes.txt`: Go 1.27.3, govulncheck v1.9.0 (compilado com Go 1.27.3), banco `vuln.go.dev` atualizado em 2026-11-02 22:01:43 UTC.
- `symbol.txt` (`-show verbose ./...`): 0 vulnerabilidades alcançáveis por símbolo; 0 em pacotes importados; 1 em módulo requerido: **GO-2026-5932**, `golang.org/x/crypto/openpgp` ("unmaintained, unsafe by design"), encontrada em `x/crypto@v0.58.0`, corrigida só em `v0.58.1-0.20261009181718-2f1f834330b2` (pseudo-versão).
- `package.txt` (`-scan package`): 0 vulnerabilidades em pacotes importados. Esse modo não depende de grafo de chamadas: diz que nenhum pacote afetado é importado.
- `symbol-achados.json`: configuração, OSV e achado em JSON.
- `grafo-openpgp.txt`: `go list -mod=vendor -deps -test ./...` (701 pacotes) contém só `x/crypto/argon2` e `x/crypto/blake2b`; nenhum `openpgp` em linux, darwin ou windows; `openpgp` não está em `vendor/modules.txt` nem em `vendor/golang.org/x/crypto/`.

Alcance: a afirmação não se apoia só na ausência de chamada direta. Os pacotes listados no OSV (`openpgp`, `openpgp/packet`, `armor`, `clearsign`, `errors`, `elgamal`, `s2k`) não entram no grafo de build, nem por dependência indireta, nem em testes, nem por reflexão ou plugin, porque o código não é compilado nem vendorizado. Logo, não há caminho de execução. O risco residual é de manutenção: um import futuro reintroduziria o pacote. O `scripts/lint.sh` roda o govulncheck e acusaria esse caso. Atualizar para a pseudo-versão foi recusado por ora (não é release). Reavaliar quando houver `x/crypto` v0.59.0 ou mais nova.

Já corrigida na rodada anterior: GO-2026-5764 (EventStream do AWS SDK, alcançável pelo S3), com `service/s3` v1.97.3 e `aws/protocol/eventstream` v1.7.8 (`backend/go.mod`).

Modo `-scan module` com padrão não é aceito pelo v1.9.0 ("patterns are not accepted for module only scanning") e, sem padrão, a raiz `backend/` não tem arquivos Go. A seção "Module Results" de `symbol.txt` cobre esse nível.

## Reexecução

Quarta parte (imagens Debian): `compose-validate.sh` original no Docker 27.5.1 rootless, verde. Terceira parte (validação do Compose): `go test` de `cmd/chatisp` (teste do Caddyfile) e seis execuções do `compose-validate.sh`, a última verde. Primeira parte (commit `89a95fa`): só sondas Docker, verificação dos contêineres e imagens do host, govulncheck (símbolo e pacote) e `go list` do grafo; nenhum código de produção mudou. Segunda parte (primeiro usuário): código mudou em `auth`, `httpapi`, `cmd/chatisp` e no painel. Foram reexecutados a suíte Go completa, `scripts/lint.sh`, o Vitest completo e os E2E `ux` e `m7-ops`. Os E2E `m3b`, `m4a` e `m4b-m6` não tocam essas áreas e não foram repetidos.
