#!/bin/bash
# Sobe PostgreSQL 18.4 embutido, a API em modo all e o Vite, sem Docker.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. ./scripts/lib.sh
use_npm11

root=$PWD
pkg="$root/backend/tools/pg/node_modules/@embedded-postgres/linux-x64"
if [ ! -x "$pkg/native/bin/initdb" ]; then
	npm ci --prefix backend/tools/pg --no-audit --no-fund
fi
python3 - "$pkg" <<'PY'
import json, os, stat, sys
root = sys.argv[1]
manifest = os.path.join(root, "native", "pg-symlinks.json")
with open(manifest, encoding="utf-8") as handle:
    specs = json.load(handle)
for spec in specs:
    source = os.path.normpath(os.path.join(root, spec["source"]))
    target = os.path.normpath(os.path.join(root, spec["target"]))
    if not source.startswith(root + os.sep) or not target.startswith(root + os.sep):
        raise SystemExit("symlink do PostgreSQL de teste recusado")
    rel = os.path.relpath(source, os.path.dirname(target))
    if os.path.islink(target) and os.readlink(target) == rel:
        continue
    os.makedirs(os.path.dirname(target), exist_ok=True)
    temporary = target + ".tmp-link"
    if os.path.lexists(temporary):
        os.remove(temporary)
    os.symlink(rel, temporary)
    os.replace(temporary, target)
bin_dir = os.path.join(root, "native", "bin")
for name in os.listdir(bin_dir):
    path = os.path.join(bin_dir, name)
    mode = os.stat(path).st_mode
    if stat.S_ISREG(mode) and mode & 0o111 == 0:
        os.chmod(path, mode | 0o755)
PY

data="$root/.tmp/dev-pg"
log="$root/.tmp/dev-pg.log"
mkdir -p "$root/.tmp"
export LC_ALL=C LANG=C TZ=UTC
export LD_LIBRARY_PATH="$pkg/native/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"

if [ ! -f "$data/PG_VERSION" ]; then
	"$pkg/native/bin/initdb" -D "$data" -U postgres -A trust -E UTF8 --no-sync
fi

port=55432
"$pkg/native/bin/postgres" -D "$data" \
	-c listen_addresses=127.0.0.1 \
	-c port="$port" \
	-c shared_buffers=16MB \
	-c dynamic_shared_memory_type=mmap \
	-c fsync=off \
	>>"$log" 2>&1 &
pg_pid=$!

api_pid=""
cleanup() {
	if [ -n "$api_pid" ]; then
		kill "$api_pid" 2>/dev/null || true
		wait "$api_pid" 2>/dev/null || true
	fi
	kill "$pg_pid" 2>/dev/null || true
	wait "$pg_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

python3 - "$port" <<'PY'
import socket, sys, time
port = int(sys.argv[1])
deadline = time.time() + 30
while time.time() < deadline:
    try:
        with socket.create_connection(("127.0.0.1", port), 1):
            sys.exit(0)
    except OSError:
        time.sleep(0.1)
sys.exit("PostgreSQL local não aceitou conexão")
PY

envfile=$(mktemp)
chmod 600 "$envfile"
go -C backend run -mod=vendor ./cmd/devdb prepare \
	-url "postgresql://postgres@127.0.0.1:${port}/postgres?sslmode=disable" >"$envfile"
while IFS= read -r line; do
	case "$line" in
	CHATISP_*=*)
		export "$line"
		;;
	esac
done <"$envfile"
rm -f "$envfile"

export CHATISP_ENV=development
export CHATISP_HTTP_ADDR=127.0.0.1:8080
export CHATISP_METRICS_ADDR=127.0.0.1:9090
export CHATISP_PANEL_ORIGINS=http://127.0.0.1:5173
export CHATISP_LOG_LEVEL=info
export CHATISP_MIGRATIONS_DIR="$root/backend/db/migrations"

go -C backend run -mod=vendor ./cmd/chatisp all &
api_pid=$!

echo "API em http://127.0.0.1:8080 e painel em http://127.0.0.1:5173"
echo "Usuário local: ${CHATISP_DEV_EMAIL}"
if [ "${CHATISP_DEV_SEEDED:-0}" = "1" ]; then
	echo "Senha local, só nesta subida: ${CHATISP_DEV_PASSWORD}"
else
	echo "O usuário local já existia. A senha não foi alterada."
fi
echo "O cookie de sessão é Secure. No navegador, a sessão só permanece em HTTPS (Compose com Caddy)."

cd web
npm exec vite -- --config "$root/scripts/vite.dev.mjs" --host 127.0.0.1 --port 5173 --strictPort
