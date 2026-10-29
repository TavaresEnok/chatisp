#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib.sh
use_npm11

go -C backend tool sqlc generate
go -C backend run -mod=vendor ./cmd/chatisp openapi -output api/openapi.json
if [ ! -d web/node_modules ]; then
	npm ci --prefix web --no-audit --no-fund
fi
npm --prefix web run generate:api
