#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
go -C backend run -mod=vendor ./cmd/chatisp openapi -output "$tmp"
if ! cmp -s "$tmp" backend/api/openapi.json; then
	echo "backend/api/openapi.json está desatualizado. Rode: make generate" >&2
	exit 1
fi

snapshot() {
	find backend/internal/platform/db/dbgen -type f -print | sort | while IFS= read -r file; do
		sha256sum "$file"
	done
}
before=$(snapshot)
go -C backend tool sqlc generate
after=$(snapshot)
if [ "$before" != "$after" ]; then
	echo "código do sqlc está desatualizado. Rode: make generate" >&2
	exit 1
fi
