#!/bin/bash
# Ensaio de carga local. Sobe só o PostgreSQL embutido do teste e um listener
# em 127.0.0.1 escolhido pelo próprio processo. Não disca para outro serviço.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ -n "${CHATISP_DATABASE_URL:-}" ] || [ -n "${CHATISP_TEST_DATABASE_URL:-}" ] || [ -n "${CHATISP_LOAD_URL:-}" ]; then
	echo "ensaio recusado: não usa banco nem URL externos" >&2
	exit 2
fi
if [ ! -d backend/tools/pg/node_modules/@embedded-postgres/linux-x64 ]; then
	npm ci --prefix backend/tools/pg --no-audit --no-fund
fi
go -C backend test -count=1 -timeout 360s -run 'TestSyntheticLocalLoad$' ./cmd/chatisp/
echo "Resultado sintético. Não é capacidade de piloto. A D2-07 continua aberta. k6 não rodou."
