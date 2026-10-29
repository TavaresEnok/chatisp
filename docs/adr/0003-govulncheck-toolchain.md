# ADR 0003 — Toolchain do govulncheck

- Status: aceita para este repositório
- Data: 29/10/2026
- Escopo: somente `scripts/lint.sh`. Não altera `backend/go.mod`, `backend/go.sum`, `backend/vendor`, a versão de Go do produto, golangci-lint, README, launcher, frontend nem urlguard.
- Relação com a especificação: B.3.4 pede govulncheck no gate de qualidade. A ADR 0002 já excluía o gerenciador de pacotes do sistema e apontava `go tool` ou binário pinado. O bloco `tool` do módulo da aplicação fica de fora deste gate para o manifesto do produto não absorver a ferramenta.

## Problema

`scripts/lint.sh` fixava `golang.org/x/vuln/cmd/govulncheck@v1.1.4` e o invocava com `go run` dentro de `backend/`. Neste host o Go é 1.27.3 (`go version go1.27.3 linux/amd64`, `/usr/local/go`). O produto permanece em `go 1.27` no `backend/go.mod`.

A tag v1.1.4, publicada em 2025-01-06, exige `golang.org/x/tools v0.29.0`. O construtor SSA dessa revisão encerra em panic ao ver `*ast.KeyValueExpr` numa posição que trata como expressão genérica:

```text
panic: unexpected expr: *ast.KeyValueExpr
golang.org/x/tools@v0.29.0/go/ssa/builder.go:958
```

A linha 958 da tag `v0.29.0` de `go.googlesource.com/tools` é o `default` de `expr0`: `panic(fmt.Sprintf("unexpected expr: %T", e))`. Na reprodução, a chamada sobe de `compLit` (`builder.go:1304`). O panic ocorre com um módulo mínimo (`fmt.Println`) no Go 1.27.3; não depende de uma construção particular do backend. O processo do govulncheck termina com status 2. Isso é falha da ferramenta, antes de qualquer relatório de vulnerabilidade. Não é sucesso e não pode ser ignorado.

`go run` também não preserva o status do binário. Um programa que chama `os.Exit(3)` faz `go run` imprimir `exit status 3` e devolver 1 ao shell. O govulncheck v1.9.0 usa 3 quando encontra vulnerabilidade aplicável no modo texto (`errVulnerabilitiesFound` em `internal/scan/errors.go`). Uso inválido é 2. Os demais erros saem por `os.Exit(1)` em `cmd/govulncheck/main.go`. Colapsar esses códigos em 1 mistura vulnerabilidade com falha da ferramenta.

## Fontes oficiais

Consulta em 29/10/2026 ao proxy de módulos e ao Gerrit do Go. Não há tarball de release do govulncheck; a distribuição oficial é o módulo.

| Peça | Valor |
| --- | --- |
| Comando | `golang.org/x/vuln/cmd/govulncheck` |
| Versão fixada | `v1.9.0`, a última tag listada por `https://proxy.golang.org/golang.org/x/vuln/@v/list` |
| Instante da tag | `2026-10-11T03:39:53Z` (`@v/v1.9.0.info`) |
| Origem | `https://go.googlesource.com/vuln`, `refs/tags/v1.9.0`, commit `e672cbe8fbc8c76e28fcb6db0efd9ce354e0dac5` |
| `go` do módulo da ferramenta | `1.26.0` (inferior ao Go 1.27.3 local; não pede outro toolchain) |
| Dependência SSA | `golang.org/x/tools v0.52.0`, tag `https://go.googlesource.com/tools` `refs/tags/v0.52.0`, commit `16517d951552611e7bd358c48ee1051fd0e57b0c`, `2026-10-10T19:29:17Z` |
| Soma do módulo vuln | `h1:12VaqnI9kJfqwRoPptxoqsUhQtdfrSbdksT7X/KY/sc=` (gravada no binário por `go install`) |
| Versão retirada | `v1.1.4`, `2025-01-06T19:26:26Z`, commit `d1f380186385b4f64e00313f31743df8e4b89a77`, `golang.org/x/tools v0.29.0` |
| Documentação de status | `cmd/govulncheck/doc.go` da mesma tag: status 0 sem vulnerabilidade aplicável; status diferente de 0 quando há. `-json`, `-format sarif` e `-format openvex` devolvem 0 mesmo com achados. O gate não usa esses formatos. |
| Banco consultado | `https://vuln.go.dev`, como descrito em `doc.go` |

O `GOSUMDB` deste host é `sum.golang.org`. O `go install` da versão com sufixo `@v1.9.0` confere a soma e não altera o `go.mod` do diretório corrente.

## Alternativas

1. Manter v1.1.4. Reproduz o panic no Go 1.27.3. Descartada.
2. Tratar panic ou status 2 como sucesso, ou deixar de chamar govulncheck. Desliga o gate. Descartada.
3. Subir o Go do produto, ou fixar `GOTOOLCHAIN` numa versão mais nova que 1.27.3, para acomodar a ferramenta antiga. A ferramenta antiga quebra neste Go; um Go ainda mais novo não é o pedido. Descartada.
4. Colocar govulncheck no bloco `tool` de `backend/go.mod`. Pinaria a versão no manifesto da aplicação e no `vendor`. Fora do escopo autorizado. Descartada.
5. `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0`. Não grava o manifesto e usa o cache de módulos, mas reescreve o status 3 para 1. Descartada como invocação do gate.
6. `go install` da tag v1.9.0 com `GOTOOLCHAIN=local` e `GOBIN` em `.tools/`, já ignorado pelo Git, e executar o binário. É a alternativa adotada. O binário fica amarrado ao Go que está no `PATH` (`go version` do arquivo tem de ser o `GOVERSION` atual), porque o próprio govulncheck recusa análise quando o Go que o compilou difere do Go do `PATH`.

## Decisão

- `scripts/lint.sh` fixa `govulncheck_version=v1.9.0`.
- Se `.tools/govulncheck` não existir, não tiver o módulo `golang.org/x/vuln` naquela versão (`go version -m`) ou não tiver sido compilado com `go env GOVERSION`, o script reinstala:

```sh
GOBIN="$tools_dir" GOTOOLCHAIN=local GOFLAGS= go install "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}"
```

`GOTOOLCHAIN=local` impede o download de outro Go. `GOFLAGS` vazio evita um `-mod=vendor` herdado do ambiente, que não se aplica a este módulo externo. `backend/go.mod` e `backend/vendor` não entram nessa instalação.

- A análise continua sendo a do modo texto padrão, no módulo do backend: `govulncheck ./...`. Sem `-json` e sem filtro de identificadores. golangci-lint permanece 2.15.0, no mesmo bloco de instalação de antes. gofmt, ESLint, TypeScript e `scripts/check-generated.sh` permanecem no script.
- `set -eu` propaga o status do binário. Vulnerabilidade aplicável (código 3) falha o script. Erro de uso (2), panic (2) e demais erros (1) também falham. Saída 0 só quando a ferramenta conclui e não há vulnerabilidade no nível de símbolo que o modo padrão trata como aplicável.
- Achado de módulo exigido, sem chamada ao símbolo vulnerável, continua visível no resumo impresso pela ferramenta. Não é apagado nem convertido em sucesso artificial: o status 0 nesse caso é a regra do govulncheck, não uma supressão deste repositório.

## Resultado medido em 29/10/2026

Go usado: `go1.27.3`. Binário: `.tools/govulncheck: go1.27.3`, módulo `golang.org/x/vuln v1.9.0`, dependência `golang.org/x/tools v0.52.0`.

Reprodução da falha antiga, módulo mínimo, `GOTOOLCHAIN=local go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...`: panic `unexpected expr: *ast.KeyValueExpr` em `x/tools@v0.29.0/go/ssa/builder.go:958`, `exit status 2`. Nenhuma vulnerabilidade foi reportada; a ferramenta morreu antes.

Análise do backend com o binário v1.9.0, `cd backend && ../.tools/govulncheck ./...`: terminou sem panic, status 0.

```text
=== Symbol Results ===

No vulnerabilities found.

Your code is affected by 0 vulnerabilities.
This scan also found 0 vulnerabilities in packages you import and 1
vulnerability in modules you require, but your code doesn't appear to call these
vulnerabilities.
```

A mesma árvore com `-show verbose` (só para nomear o achado; o gate não passa esse flag) lista 20 pacotes raiz do módulo `chatisp`, 27 módulos e a biblioteca padrão `go1.27.3`. O único achado é de módulo, não de símbolo chamado:

- ID: `GO-2026-5932`
- Módulo: `golang.org/x/crypto@v0.58.0`
- Texto da base: o pacote `golang.org/x/crypto/openpgp` está sem manutenção, é inseguro por desenho e tem problemas de segurança conhecidos
- Correção indicada pela base: `N/A`
- Ficha: `https://pkg.go.dev/vuln/GO-2026-5932`
- Efeito no gate: o código do backend não chama esse símbolo. O govulncheck registra o achado no resumo e devolve 0. Não foi ocultado e não foi “corrigido” por exclusão. Uma vulnerabilidade que o modo padrão classifique como chamada continua falhando o script com status 3.

## Custo

- O primeiro `go install` baixa `golang.org/x/vuln@v1.9.0` e as dependências dele para o cache de módulos e grava o binário em `.tools/`. Rede e disco, sem root e sem serviço pago.
- As execuções seguintes reutilizam o binário enquanto a versão e o Go compilador baterem.
- A análise fala com `https://vuln.go.dev`. É o mesmo tráfego que o govulncheck já faria; o `doc.go` limita o pedido a caminhos de módulo que a base já conhece.
- Nenhum Go adicional é baixado. Nenhum manifesto da aplicação muda.

## Operação

Para repetir fora do script, na raiz do repositório, com o mesmo Go 1.27.3 no `PATH`:

```sh
mkdir -p .tools
GOBIN="$PWD/.tools" GOTOOLCHAIN=local GOFLAGS= go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
go version -m .tools/govulncheck
( cd backend && ../.tools/govulncheck ./... )
```

`go version -m` precisa mostrar `golang.org/x/vuln` `v1.9.0` e a primeira linha precisa ser o `go1.27.3` local. A saída do scan é o registro: status 0, 3 ou erro. Não se redireciona para `/dev/null`.

O script de lint chama essa sequência depois do gofmt e do golangci-lint 2.15.0. Falha conhecida de `ineffassign` no golangci-lint é outro trabalho; este gate não a trata e não a esconde.

## Reversibilidade

Voltar a uma tag anterior é trocar `govulncheck_version` e apagar `.tools/govulncheck` para forçar o `go install`. Enquanto a tag for v1.1.4 no Go 1.27.3, o panic retorna. Passar a usar o bloco `tool` no futuro exige mudança explícita de `backend/go.mod` e de `vendor`, com o mesmo número de versão se a análise tiver de permanecer comparável. Nenhuma das duas voltas mexe em dados de atendimento.
