#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib.sh
use_npm11

golangci_version=2.15.0
# v1.9.0: última tag estável em proxy.golang.org (2026-10-11). v1.1.4 puxa
# x/tools v0.29.0, cujo SSA entra em panic com fonte de Go 1.27.
govulncheck_version=v1.9.0
tools_dir=$PWD/.tools
mkdir -p "$tools_dir"

if [ ! -x "$tools_dir/golangci-lint" ] || ! "$tools_dir/golangci-lint" version 2>/dev/null | grep -q " ${golangci_version} "; then
	archive="$tools_dir/golangci-lint.tar.gz"
	url="https://github.com/golangci/golangci-lint/releases/download/v${golangci_version}/golangci-lint-${golangci_version}-linux-amd64.tar.gz"
	curl -fsSL "$url" -o "$archive"
	tar -C "$tools_dir" -xzf "$archive" --strip-components=1 "golangci-lint-${golangci_version}-linux-amd64/golangci-lint"
	rm -f "$archive"
	chmod 755 "$tools_dir/golangci-lint"
fi

# Binário compilado com o Go do PATH. `go run` não devolve o status do
# programa (vulnerabilidade aplicável é 3; falha de uso é 2; panic é 2).
govulncheck_bin=$tools_dir/govulncheck
govulncheck_modline=$(printf '\tgolang.org/x/vuln\t%s\t' "$govulncheck_version")
govulncheck_go=$(go env GOVERSION)
if [ ! -x "$govulncheck_bin" ] \
	|| ! go version -m "$govulncheck_bin" 2>/dev/null | grep -q -F "$govulncheck_modline" \
	|| ! go version "$govulncheck_bin" 2>/dev/null | grep -q -E ": ${govulncheck_go}"'$'; then
	GOBIN="$tools_dir" GOTOOLCHAIN=local GOFLAGS= go install "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}"
	chmod 755 "$govulncheck_bin"
fi

unformatted=$(gofmt -l backend/cmd backend/internal)
if [ -n "$unformatted" ]; then
	echo "gofmt pendente:" >&2
	echo "$unformatted" >&2
	exit 1
fi

(
	cd backend
	../.tools/golangci-lint run --config ../.golangci.yml ./...
)
(
	cd backend
	"$govulncheck_bin" ./...
)

if [ ! -d web/node_modules ]; then
	npm ci --prefix web --no-audit --no-fund
fi
npm --prefix web run lint
npm --prefix web run typecheck
./scripts/check-generated.sh
