# ADR 0002 — Ferramentas de build

- Status: aceita para este repositório
- Data: 28/10/2026
- Escopo: frontend, Makefile, scripts e ferramentas Go de geração
- Relação com a especificação: C.1 e C.3 citam Makefile, `make test` e `make lint`. B.3 não fixa pnpm. B.6 pede problema, alternativas, custo, operação e reversibilidade.

## Problema

O host de desenvolvimento não tem `make` e não tem root. A especificação ainda pede uma interface `make test` / `make lint` para CI e para máquinas que tenham make. O frontend precisa de um gerenciador único. Neste host o Node é v24.22.1 e o npm desse prefixo é 11.19.1, mas `/usr/local/bin/npm` 10.9.9 aparece antes no `PATH` e rodaria no mesmo Node por `#!/usr/bin/env node`. pnpm 12 também está instalável por Corepack. Dois lockfiles, ou um lockfile gerado pelo npm 10, quebrariam a reprodução.

sqlc e goose precisam rodar para gerar código e aplicar SQL. Instalação global em `/usr/local/bin` exige root e solta a versão.

## Alternativas

1. Só Makefile. Atende a especificação e a CI, e não roda neste host. Insuficiente sozinha.
2. Só scripts, apagando o Makefile. Quebraria o contrato `make test` / `make lint` dos hosts que têm make.
3. Makefile mantido e scripts equivalentes, com um único lugar para cada comando. Atende os dois ambientes.
4. pnpm, já presente no host. Foi rejeitado para este repositório. O gerenciador é npm.
5. `go run` com módulo solto, sem versão no `go.mod`. Cada execução poderia resolver outra revisão. Foi rejeitado.
6. `go tool` no `backend/go.mod`, com sqlc e goose pinados. O Go 1.27 aceita o bloco `tool` no `go.mod` (desde o Go 1.24). Não pede root. É a alternativa adotada para sqlc e goose.

## Decisão

- O frontend usa npm. O lockfile é `web/package-lock.json`. Não há `pnpm-lock.yaml`, `yarn.lock` nem chamada a pnpm nos scripts do projeto.
- A versão de npm é a 11 que acompanha o Node 24 selecionado. Neste host isso é npm 11.19.1 junto de Node v24.22.1. Scripts e CI colocam esse prefixo à frente no `PATH`, ou invocam esse npm pelo caminho do Node 24, de modo que `/usr/local/bin/npm` 10.9.9 não grave o lockfile.
- O Makefile permanece para CI e para hosts com GNU make. Os alvos mínimos são `test` e `lint`, como em C.3. Outros alvos de desenvolvimento delegam do mesmo jeito.
- Cada comando vive num script equivalente, por exemplo `scripts/test.sh` e `scripts/lint.sh`, criados no M1. O alvo do Makefile só chama o script. Host sem make executa o script. Os dois caminhos rodam a mesma sequência.
- sqlc e goose entram no bloco `tool` de `backend/go.mod` no M1, com versão fixada no `go.sum`. A invocação é `go tool sqlc` e `go tool goose`, a partir de `backend/`. Não há binário global.
- golangci-lint e govulncheck, quando o M1 ligar a qualidade pedida em B.3.4, seguem a mesma regra de `go tool` ou de binário pinado no cache do módulo. Eles não são instalados com o gerenciador de pacotes do sistema.

## Custo

- Dois pontos de entrada, Makefile e script, com a disciplina de o Makefile não repetir o comando.
- O primeiro `go tool` baixa módulos para o cache do Go. Isso usa rede e disco, sem root.
- npm 11 é um requisito explícito de ambiente. Quem rodar npm 10 por engano gera diff de lockfile e o review devolve.
- pnpm instalado na máquina do desenvolvedor fica sem efeito no repositório. Não há custo de desinstalação.
- Não entra serviço novo em produção. A decisão não muda a stack de runtime da especificação.

## Operação

- `scripts/test.sh` cobre o que `make test` cobre: testes Go com o banco da ADR 0001 e, quando o marco já tiver frontend, a suíte npm correspondente.
- `scripts/lint.sh` cobre o que `make lint` cobre.
- Geração: `go tool sqlc generate` e `go tool goose`, com diretórios `backend/db/migrations` e `backend/db/queries` previstos em B.7. A versão da ferramenta fica no `go.mod`, não num comentário solto.
- No frontend, os comandos são `npm ci` e `npm run …`. O campo `packageManager` do `package.json`, quando o M1 criar o pacote, aponta para npm 11.19.1 para o Corepack não escolher pnpm.
- CI com make chama `make test` e `make lint`. CI sem make chama os scripts. Os dois publicam o mesmo resultado.
- Este host, hoje, usa os scripts. A ausência de make não é motivo para apagar o Makefile quando ele for adicionado no M1.

## Reversibilidade

Adotar pnpm no futuro é trocar o lockfile e o campo `packageManager`, sem mexer no backend. Voltar a um Makefile autossuficiente é copiar os comandos de volta para os alvos, se todo host de desenvolvimento passar a ter make. Tirar sqlc ou goose do bloco `tool` e instalá-los à parte não altera o SQL gerado, desde que a versão pinada permaneça a mesma.

Nenhuma dessas voltas pede migração de dados de atendimento.
