#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib.sh
use_npm11

golangci_version=2.15.0
govulncheck_version=v1.1.4
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
go -C backend run "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}" ./...

if [ ! -d web/node_modules ]; then
	npm ci --prefix web --no-audit --no-fund
fi
npm --prefix web run lint
npm --prefix web run typecheck
./scripts/check-generated.sh
