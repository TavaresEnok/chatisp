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

1. **Rede do build.** Todo `RUN` na rede padrão falha ao criar o endpoint: `failed to create endpoint ... on network bridge: adding interface veth... to bridge docker0 failed: could not find bridge docker0` (BuildKit e builder clássico: `probe-buildkit-default.log`, `probe-classic-default.log`).
2. **Seccomp do 20.10.9 com glibc ≥ 2.34.** Com `--network host` a rede funciona (DNS e download do índice do apt), mas:
   - `apt-get update` do Debian 13 termina com `E: Problem executing scripts APT::Update::Post-Invoke` (`probe-buildkit-host.log`). O mesmo comando em `docker run` passa com `--security-opt seccomp=unconfined` (rc 0) e falha com o perfil padrão (rc 100).
   - `node` 24 (bookworm, glibc 2.36) aborta já em `node -e 'console.log(1)'`: `Assertion failed: (0) == (uv_thread_create(...))`; com `seccomp=unconfined` executa.
   - O perfil padrão do 20.10.9 nega `clone3` com EPERM; a glibc só recua para `clone` com ENOSYS. O perfil upstream de 20.10.24 (que devolve ENOSYS via `errnoRet: 38`) **não resolveu neste daemon**: o Node continuou abortando, ou seja, o 20.10.9 não aplica o `errnoRet` desse perfil. Perfil permissivo sem regras (`defaultAction: ALLOW`) faz o Node rodar, o que confirma seccomp como causa.
   - Em execução isso também afeta qualquer binário com threads da glibc nova dentro dos contêineres (o `ffmpeg` do Debian 13 na imagem da API, por exemplo). PostgreSQL 18 (trixie) e o binário Go estático não foram afetados nas sondas.

Conclusão: o host não é compatível para build nem para execução fiel. A correção é Docker ≥ 20.10.10 (perfil com `clone3` → ENOSYS) e uma rede de build funcional, o que exigiria atualizar ou reconfigurar o Docker compartilhado. Isso não foi feito.

### Ambiente descartável

- Nenhum ambiente descartável autorizado foi encontrado. A rede `paseo-workers` (`br-paseowork`, egresso só para endereços públicos) é a rede dos trabalhadores do orquestrador e não oferece um Docker próprio.
- Tentativa: BuildKit rootless em contêiner próprio (`moby/buildkit:v0.16.0-rootless`, rede do host). Exige `seccomp=unconfined` e `apparmor=unconfined` no contêiner do builder; a execução foi **negada** pela política de permissões desta sessão, e não houve contorno. A imagem baixada foi removida (`docker rmi moby/buildkit:v0.16.0-rootless`, conferido: nenhum contêiner a usava).
- Portanto **não foram executados**: build das imagens, subida, migrações via Compose, HTTPS do Caddy, login, healthchecks, persistência após reinício e encerramento.

### Procedimento exato num host compatível

Requisitos: Docker Engine ≥ 20.10.10 com a ponte padrão funcional e NAT de saída para o build, Compose ≥ 2.24.4, `openssl`, `python3`, `curl`.

```
git clone <repositório> && cd chat-isp && git checkout <commit desta entrega>
docker version --format '{{.Server.Version}}'      # >= 20.10.10
docker run --rm debian:13-slim sh -c 'apt-get update >/dev/null && echo ok'   # prova seccomp/rede
./scripts/compose-validate.sh
```

O script cobre build, subida com `--wait` (healthcheck da API e dependências `service_healthy`/`service_completed_successfully`, o que inclui `chatisp-migrate`), `/healthz`, `/readyz` e o painel por HTTPS no Caddy, e desfaz o projeto com `down -v`. **Não cobre** login, persistência após reinício nem o primeiro usuário; ver a lacuna abaixo. Passos manuais para essas provas, no mesmo projeto antes do `down -v`:

```
docker compose -p <projeto> ... restart                     # reinício
docker compose -p <projeto> ... exec postgres psql -U chatisp -d chatisp -c 'select count(*) from tenants'
curl -fsSk https://127.0.0.1:<porta>/readyz                  # de novo, depois do reinício
```

### Lacuna de instalação encontrada (implementação faltante)

Numa instalação por Compose não há como criar o primeiro usuário:

- `is_superadmin` só é ligado por SQL nos testes (`grep` no código de produção: nenhuma ocorrência).
- `POST /api/platform/tenants` (`auth.CreateProvider`) cria o provedor sem administrador nem convite; `auth.Invite` exige um administrador já existente no provedor.
- `chatisp dev-prepare` cria `dev.local@example.com`, mas é a preparação de desenvolvimento: recria papéis com senhas novas e serve ao `scripts/dev.sh`, não ao Compose.

Consequência: login no Compose depende de SQL manual com hash de senha. Afeta 13-self-host, 14-provisionamento ("sem alterar código") e 12-superadmin. Falta implementar: um comando de inicialização idempotente (por exemplo `chatisp bootstrap-superadmin -email`, senha lida da entrada padrão, auditado em `platform_audit_logs`) e o convite do primeiro administrador ao criar o provedor. Prova de fechamento: teste Go do comando e do convite, e o login pelo Caddy no `compose-validate.sh` estendido.

### Recursos criados por esta tarefa (05/11/2026)

| Recurso | Situação |
| --- | --- |
| Imagem `moby/buildkit:v0.16.0-rootless` (`dd70a5f3784e`, baixada 14:42 UTC) | removida individualmente |
| Contêineres das sondas (`docker run --rm`) | removidos pelo `--rm` |
| Builds da sonda (`chatisp-probe-net:*`) | falharam no `RUN`; nenhuma etiqueta nem cache gerado |
| Contêiner `determined_hermann` (`172052ca40ee`, `npm ci` do build clássico de 10:07:45, saiu com 134) e imagem sem etiqueta `933e4b0aa2d1` (estágio `web`: `WORKDIR /src/web` + `COPY package*.json`, 231 MB), criados pela rodada anterior | **ainda presentes**: a remoção foi negada pela política de permissões. Conferidos: nenhum outro contêiner usa a imagem e nenhuma imagem deriva dela. Comandos para remover, nesta ordem: `docker rm 172052ca40ee0fb33d7fad428949b96ec0aa93d036569178d9679177925ab24c` e `docker rmi 933e4b0aa2d1dee54230fa3a9e78e924443dc99467c6fa190b36bf4e38b8e933` |

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

Contagem nova em `docs/matriz-requisitos.md`: 202 verificados, 33 aguardando homologação externa, 22 parciais, 0 não implementados (257). Mudanças: 14 linhas saíram de "aguardando homologação" por terem implementação faltante (adaptador real do ERP e conector Evolution), e 12-superadmin e 14-provisionamento saíram de "verificado" pela lacuna do primeiro usuário.

### Os seis parciais anteriores

| Requisito | Código que já existe | O que falta implementar | Informação ou ambiente externo | Prova que encerra |
| --- | --- | --- | --- | --- |
| 12-https | `deploy/Caddyfile` (TLS e proxy para a API); cookies `__Host-session` com `Secure; HttpOnly; SameSite=Lax`, conferidos pelo proxy HTTPS do `scripts/dev.sh` | nada no proxy; o primeiro usuário (lacuna acima) para provar login | host com Docker ≥ 20.10.10 e rede de build | `compose-validate.sh` verde com login pelo Caddy |
| 13-self-host | `deploy/compose.yaml`, `deploy/Dockerfile`, `.env.example` com teste de guarda (`deploy_files_test.go`), `compose-validate.sh` | comando de inicialização do superadmin/primeiro admin | idem | instalação do zero, login e reinício com dados preservados |
| 13-compose | idem; `docker compose config -q` passa | nenhum código além do anterior | idem | `compose-validate.sh` verde, mais reinício e `down -v` sem sobras |
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

Reutilizados os testes válidos da rodada `b2b6910`. Nesta rodada só foram executados: sondas Docker, verificação dos contêineres e imagens do host, govulncheck (símbolo e pacote) e `go list` do grafo. Nenhum código de produção mudou, então a suíte Go, o Vitest e os E2E não foram repetidos.
