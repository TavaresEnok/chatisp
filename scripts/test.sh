#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
. ./scripts/lib.sh
use_npm11

npm ci --prefix backend/tools/pg --no-audit --no-fund
go -C backend vet ./...
go -C backend test -count=1 ./...
npm ci --prefix web --no-audit --no-fund
npm --prefix web run test
