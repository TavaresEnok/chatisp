#!/bin/bash
# Ensaio de carga local. Sobe só o PostgreSQL embutido do teste e um listener
# em 127.0.0.1 escolhido pelo próprio processo. Não disca para outro serviço.
# Opcional: CHATISP_LOAD_CLIENTS (1..32) e
# CHATISP_LOAD_REQUESTS_PER_CLIENT (1..100); limites são só da ferramenta.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ -n "${CHATISP_DATABASE_URL:-}" ] || [ -n "${CHATISP_TEST_DATABASE_URL:-}" ] || [ -n "${CHATISP_LOAD_URL:-}" ]; then
	echo "ensaio recusado: não usa banco nem URL externos" >&2
	exit 2
fi
if [ ! -d backend/tools/pg/node_modules/@embedded-postgres/linux-x64 ]; then
	echo "ensaio recusado: instale previamente as dependências de backend/tools/pg (npm ci --prefix backend/tools/pg)" >&2
	exit 2
fi
# Não baixar módulos, banco ou toolchain durante o ensaio.
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C backend test -v -count=1 -timeout 360s -run 'TestSyntheticLocalLoad$' ./cmd/chatisp/
echo "Resultado sintético. Não é capacidade de piloto. A D2-07 continua aberta. k6 não rodou."
