#!/bin/bash
# Prova o launcher com doubles no PATH e portas efêmeras. Não instala
# dependências nem deixa PostgreSQL, API ou Vite de verdade em execução.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
mkdir -p "$root/.tmp"
# /tmp é noexec neste host; os doubles precisam ser executáveis.
work=$(mktemp -d "$root/.tmp/launcher-test.XXXXXX")
tracked=()

cleanup_test() {
	local pid
	for pid in "${tracked[@]}"; do
		if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
			kill -TERM "$pid" 2>/dev/null || true
		fi
	done
	wait "${tracked[@]}" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup_test EXIT

fail() {
	echo "dev.test.sh: $*" >&2
	exit 1
}

assert_dead() {
	local pid=$1 label=$2
	if [ -z "$pid" ] || [[ ! "$pid" =~ ^[0-9]+$ ]]; then
		fail "$label sem pid"
	fi
	if ps -p "$pid" >/dev/null 2>&1; then
		fail "$label $pid continua em execução"
	fi
}

assert_alive() {
	local pid=$1 label=$2
	if ! ps -p "$pid" >/dev/null 2>&1; then
		fail "$label $pid encerrou"
	fi
}

free_ports() {
	python3 - "$1" <<'PY'
import socket
import sys
count = int(sys.argv[1])
sockets = []
ports = []
for _ in range(count):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.bind(("127.0.0.1", 0))
    ports.append(str(sock.getsockname()[1]))
    sockets.append(sock)
for sock in sockets:
    sock.close()
print(" ".join(ports))
PY
}

install_doubles() {
	local bin=$work/bin
	mkdir -p "$bin"
	cat >"$bin/npm" <<'EOF'
#!/bin/bash
set -euo pipefail
if [ "${1:-}" = "-v" ]; then
	echo "11.19.1"
	exit 0
fi
if [ "${1:-}" = "ci" ]; then
	echo "npm ci não deve rodar no teste do launcher" >&2
	exit 99
fi
dir=${CHATISP_DEV_TEST_DIR:?}
mkdir -p "$dir"
{
	printf 'CHATISP_DEV_API_ORIGIN=%s\n' "${CHATISP_DEV_API_ORIGIN-}"
	printf 'CHATISP_DEV_VITE_HOST=%s\n' "${CHATISP_DEV_VITE_HOST-}"
	printf 'CHATISP_DEV_VITE_PORT=%s\n' "${CHATISP_DEV_VITE_PORT-}"
} >"$dir/vite.env"
printf '%s\n' "$@" >"$dir/vite.args"
echo $$ >"$dir/vite.pid"
exec sleep 300
EOF
	cat >"$bin/initdb" <<'EOF'
#!/bin/bash
set -euo pipefail
dir=${CHATISP_DEV_TEST_DIR:?}
data=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "-D" ]; then
		data=$arg
	fi
	prev=$arg
done
[ -n "$data" ] || exit 1
mkdir -p "$data" "$dir"
echo 18 >"$data/PG_VERSION"
echo "$data" >"$dir/initdb.datadir"
exit 0
EOF
	cat >"$bin/postgres" <<'EOF'
#!/bin/bash
set -euo pipefail
dir=${CHATISP_DEV_TEST_DIR:?}
host=127.0.0.1
port=""
for arg in "$@"; do
	case "$arg" in
	listen_addresses=*) host=${arg#listen_addresses=} ;;
	port=*) port=${arg#port=} ;;
	esac
done
[ -n "$port" ] || exit 1
mkdir -p "$dir"
echo $$ >"$dir/pg.pid"
exec python3 - "$host" "$port" <<'PY'
import signal
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
family = socket.AF_INET6 if ":" in host else socket.AF_INET
server = socket.socket(family, socket.SOCK_STREAM)
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind((host, port))
server.listen(8)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
signal.signal(signal.SIGINT, lambda *_: sys.exit(0))
while True:
    try:
        conn, _ = server.accept()
    except OSError:
        break
    conn.close()
PY
EOF
	cat >"$bin/go" <<'EOF'
#!/bin/bash
set -euo pipefail
dir=${CHATISP_DEV_TEST_DIR:?}
mkdir -p "$dir"
joined="$*"
if [[ "$joined" == *devdb* ]]; then
	cat <<ENV
CHATISP_DATABASE_URL=postgresql://postgres@127.0.0.1:9/postgres?sslmode=disable
CHATISP_APP_DATABASE_URL=postgresql://app@127.0.0.1:9/db?sslmode=disable
CHATISP_PLATFORM_DATABASE_URL=postgresql://platform@127.0.0.1:9/db?sslmode=disable
CHATISP_DEV_EMAIL=dev.local@example.com
CHATISP_DEV_SEEDED=0
ENV
	exit 0
fi
if [[ "$joined" == *chatisp* ]]; then
	{
		printf 'CHATISP_HTTP_ADDR=%s\n' "${CHATISP_HTTP_ADDR-}"
		printf 'CHATISP_METRICS_ADDR=%s\n' "${CHATISP_METRICS_ADDR-}"
		printf 'CHATISP_PANEL_ORIGINS=%s\n' "${CHATISP_PANEL_ORIGINS-}"
	} >"$dir/api.env"
	echo $$ >"$dir/api.pid"
	if [ "${CHATISP_DEV_API_MODE:-serve}" = "fail" ]; then
		for _ in $(seq 1 100); do
			[ -s "$dir/vite.pid" ] && [ -s "$dir/pg.pid" ] && exit 1
			sleep 0.05
		done
		exit 1
	fi
	exec python3 - "${CHATISP_HTTP_ADDR:?}" <<'PY'
import signal
import socket
import sys

raw = sys.argv[1]
if raw.startswith("["):
    end = raw.index("]")
    host = raw[1:end]
    port = int(raw.rsplit(":", 1)[1])
    family = socket.AF_INET6
else:
    host, port_raw = raw.rsplit(":", 1)
    port = int(port_raw)
    family = socket.AF_INET
server = socket.socket(family, socket.SOCK_STREAM)
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind((host, port))
server.listen(8)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
signal.signal(signal.SIGINT, lambda *_: sys.exit(0))
while True:
    try:
        conn, _ = server.accept()
    except OSError:
        break
    try:
        data = b""
        conn.settimeout(1)
        while b"\r\n\r\n" not in data and len(data) < 8192:
            chunk = conn.recv(1024)
            if not chunk:
                break
            data += chunk
        if b"GET /healthz " in data:
            body = b"ok"
            head = b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n"
            conn.sendall(head + body)
        else:
            conn.sendall(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
    except OSError:
        pass
    finally:
        conn.close()
PY
fi
echo "comando go inesperado: $joined" >&2
exit 1
EOF
	chmod 755 "$bin/npm" "$bin/initdb" "$bin/postgres" "$bin/go"
}

read_pid() {
	local file=$1
	[ -s "$file" ] || fail "pid ausente em $file"
	local pid
	pid=$(tr -d '[:space:]' <"$file")
	[[ "$pid" =~ ^[0-9]+$ ]] || fail "pid inválido em $file"
	printf '%s\n' "$pid"
}

run_launcher() {
	local log=$1 dir=$2 data=$3 api=$4 metrics=$5 pg=$6 vite=$7 mode=$8 timeout=$9
	env \
		PATH="$work/bin:$PATH" \
		CHATISP_DEV_POSTGRES=postgres \
		CHATISP_DEV_INITDB=initdb \
		CHATISP_DEV_TEST_DIR="$dir" \
		CHATISP_DEV_PG_DATA="$data" \
		CHATISP_DEV_PG_LOG="$data.log" \
		CHATISP_DEV_READY_TIMEOUT="$timeout" \
		CHATISP_DEV_API_MODE="$mode" \
		CHATISP_HTTP_ADDR="$api" \
		CHATISP_METRICS_ADDR="$metrics" \
		CHATISP_DEV_PG_HOST=127.0.0.1 \
		CHATISP_DEV_PG_PORT="$pg" \
		CHATISP_DEV_VITE_HOST=127.0.0.1 \
		CHATISP_DEV_VITE_PORT="$vite" \
		bash "$root/scripts/dev.sh" >"$log" 2>&1 &
	launcher_pid=$!
	tracked+=("$launcher_pid")
}

wait_exit() {
	local pid=$1 timeout=$2
	local ticks=$((timeout * 10))
	local i
	for ((i = 0; i < ticks; i++)); do
		if ! kill -0 "$pid" 2>/dev/null; then
			wait "$pid"
			return $?
		fi
		sleep 0.1
	done
	kill -TERM "$pid" 2>/dev/null || true
	fail "launcher $pid não encerrou em ${timeout}s"
}

node --input-type=module <<'JS'
import { devServerSettings } from "./scripts/vite.dev.mjs";

const settings = devServerSettings({
  CHATISP_DEV_VITE_HOST: "127.0.0.1",
  CHATISP_DEV_VITE_PORT: "4173",
  CHATISP_DEV_API_ORIGIN: "http://127.0.0.1:18080",
});
if (settings.host !== "127.0.0.1" || settings.port !== 4173 || settings.strictPort !== true) {
  process.exit(1);
}
for (const path of ["/api", "/healthz", "/readyz"]) {
  if (settings.proxy[path] !== "http://127.0.0.1:18080") {
    process.exit(1);
  }
}
const defaults = devServerSettings({});
if (defaults.host !== "127.0.0.1" || defaults.port !== 5173 || defaults.proxy["/api"] !== "http://127.0.0.1:8080") {
  process.exit(1);
}
const v6 = devServerSettings({
  CHATISP_DEV_VITE_HOST: "::1",
  CHATISP_DEV_VITE_PORT: "4174",
  CHATISP_DEV_API_ORIGIN: "http://[::1]:18081",
});
if (v6.host !== "::1" || v6.proxy["/api"] !== "http://[::1]:18081") {
  process.exit(1);
}
for (const env of [
  { CHATISP_DEV_API_ORIGIN: "http://example.com:8080" },
  { CHATISP_DEV_VITE_HOST: "0.0.0.0" },
  { CHATISP_DEV_API_ORIGIN: "https://127.0.0.1:8080" },
]) {
  let threw = false;
  try {
    devServerSettings(env);
  } catch {
    threw = true;
  }
  if (!threw) {
    process.exit(1);
  }
}
JS

python3 - <<'PY'
from pathlib import Path
text = Path("README.md").read_text(encoding="utf-8")
required = [
    "CHATISP_HTTP_ADDR",
    "CHATISP_METRICS_ADDR",
    "CHATISP_DEV_PG_PORT",
    "CHATISP_DEV_VITE_PORT",
    "127.0.0.1:8080",
    "127.0.0.1:55432",
    "127.0.0.1:5173",
    "Secure",
    "https://localhost:8443",
]
missing = [item for item in required if item not in text]
if missing:
    raise SystemExit("README sem: " + ", ".join(missing))
lower = text.lower()
if "não descarte" in lower or "sessão funcional" in lower and "não" not in text[text.lower().find("http"):text.lower().find("http")+400]:
    pass
if "continua aceitando o login em HTTP" in text:
    raise SystemExit("README apresenta login HTTP como sessão")
if "Em HTTP o navegador não guarda" not in text and "em HTTP o navegador não guarda" not in text:
    raise SystemExit("README não explica o limite do cookie Secure em HTTP")
PY

install_doubles

bystander_py=$work/bystander.py
cat >"$bystander_py" <<'PY'
import time
time.sleep(300)
PY
python3 "$bystander_py" &
bystander=$!
tracked+=("$bystander")

# Endereço que não é loopback não sobe filho nenhum.
scenario=$work/reject
mkdir -p "$scenario/run"
run_launcher "$scenario/log" "$scenario/run" "$scenario/pg" "0.0.0.0:18080" "127.0.0.1:19090" "55432" "5173" "fail" "5"
launcher=$launcher_pid
set +e
wait_exit "$launcher" 10
run_status=$?
set -e
[ "$run_status" -ne 0 ] || fail "endereço público foi aceito"
[ ! -e "$scenario/run/pg.pid" ] || fail "PostgreSQL subiu com endereço público"
[ ! -e "$scenario/run/vite.pid" ] || fail "Vite subiu com endereço público"
[ ! -e "$scenario/run/initdb.datadir" ] || fail "initdb rodou com endereço público"
assert_alive "$bystander" "processo alheio"

# Porta ocupada: o ouvinte que já estava lá permanece.
read -r busy_api busy_metrics busy_pg busy_vite <<<"$(free_ports 4)"
scenario=$work/busy
mkdir -p "$scenario/run"
python3 - "$busy_api" <<'PY' &
import socket
import sys
import time
port = int(sys.argv[1])
server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("127.0.0.1", port))
server.listen(1)
time.sleep(300)
PY
listener=$!
tracked+=("$listener")
for _ in $(seq 1 50); do
	if python3 - "$busy_api" <<'PY'
import socket, sys
s = socket.socket()
s.settimeout(0.2)
try:
    s.connect(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(1)
finally:
    s.close()
PY
	then
		break
	fi
	sleep 0.05
done
run_launcher "$scenario/log" "$scenario/run" "$scenario/pg" "127.0.0.1:${busy_api}" "127.0.0.1:${busy_metrics}" "$busy_pg" "$busy_vite" "fail" "5"
launcher=$launcher_pid
set +e
wait_exit "$launcher" 10
run_status=$?
set -e
[ "$run_status" -ne 0 ] || fail "porta ocupada foi aceita"
[ ! -e "$scenario/run/pg.pid" ] || fail "PostgreSQL subiu com a API ocupada"
[ ! -e "$scenario/run/vite.pid" ] || fail "Vite subiu com a API ocupada"
assert_alive "$listener" "ouvinte da porta ocupada"
assert_alive "$bystander" "processo alheio"
if ! grep -q "não altera o processo" "$scenario/log"; then
	fail "falta aviso de que a porta ocupada não é alterada"
fi

# Falha da API encerra Vite e PostgreSQL.
read -r fail_api fail_metrics fail_pg fail_vite <<<"$(free_ports 4)"
scenario=$work/fail
mkdir -p "$scenario/run"
run_launcher "$scenario/log" "$scenario/run" "$scenario/pg" "127.0.0.1:${fail_api}" "127.0.0.1:${fail_metrics}" "$fail_pg" "$fail_vite" "fail" "15"
launcher=$launcher_pid
set +e
wait_exit "$launcher" 20
run_status=$?
set -e
[ "$run_status" -ne 0 ] || fail "falha da API terminou com sucesso"
api_pid=$(read_pid "$scenario/run/api.pid")
vite_pid=$(read_pid "$scenario/run/vite.pid")
pg_pid=$(read_pid "$scenario/run/pg.pid")
assert_dead "$api_pid" "API"
assert_dead "$vite_pid" "Vite"
assert_dead "$pg_pid" "PostgreSQL"
assert_alive "$listener" "ouvinte da porta ocupada"
assert_alive "$bystander" "processo alheio"
grep -q "CHATISP_HTTP_ADDR=127.0.0.1:${fail_api}" "$scenario/run/api.env"
grep -q "CHATISP_METRICS_ADDR=127.0.0.1:${fail_metrics}" "$scenario/run/api.env"
grep -q "CHATISP_PANEL_ORIGINS=http://127.0.0.1:${fail_vite}" "$scenario/run/api.env"
grep -q "CHATISP_DEV_API_ORIGIN=http://127.0.0.1:${fail_api}" "$scenario/run/vite.env"
grep -q "CHATISP_DEV_VITE_HOST=127.0.0.1" "$scenario/run/vite.env"
grep -q "CHATISP_DEV_VITE_PORT=${fail_vite}" "$scenario/run/vite.env"
grep -q -- "--host" "$scenario/run/vite.args"
grep -q -- "--port" "$scenario/run/vite.args"
grep -q -- "$fail_vite" "$scenario/run/vite.args"
grep -q -- "--strictPort" "$scenario/run/vite.args"
if grep -q "CHATISP_DEV_PASSWORD" "$scenario/log"; then
	fail "senha apareceu no log sem seed"
fi

# Cancelamento encerra os filhos e deixa o processo alheio.
read -r ok_api ok_metrics ok_pg ok_vite <<<"$(free_ports 4)"
scenario=$work/signal
mkdir -p "$scenario/run"
run_launcher "$scenario/log" "$scenario/run" "$scenario/pg" "127.0.0.1:${ok_api}" "127.0.0.1:${ok_metrics}" "$ok_pg" "$ok_vite" "serve" "15"
launcher=$launcher_pid
ready=0
for _ in $(seq 1 150); do
	if python3 - "$ok_api" <<'PY'
import sys
import urllib.request
try:
    with urllib.request.urlopen(f"http://127.0.0.1:{sys.argv[1]}/healthz", timeout=0.5) as response:
        if response.status != 200:
            sys.exit(1)
except Exception:
    sys.exit(1)
PY
	then
		ready=1
		break
	fi
	if ! kill -0 "$launcher" 2>/dev/null; then
		break
	fi
	sleep 0.1
done
[ "$ready" = 1 ] || fail "API de teste não ficou pronta"
api_pid=$(read_pid "$scenario/run/api.pid")
vite_pid=$(read_pid "$scenario/run/vite.pid")
pg_pid=$(read_pid "$scenario/run/pg.pid")
assert_alive "$api_pid" "API"
assert_alive "$vite_pid" "Vite"
assert_alive "$pg_pid" "PostgreSQL"
kill -TERM "$launcher"
set +e
wait_exit "$launcher" 15
run_status=$?
set -e
[ "$run_status" -eq 143 ] || fail "cancelamento saiu com status $run_status"
assert_dead "$api_pid" "API"
assert_dead "$vite_pid" "Vite"
assert_dead "$pg_pid" "PostgreSQL"
assert_alive "$listener" "ouvinte da porta ocupada"
assert_alive "$bystander" "processo alheio"
if ! grep -q "não guarda essa sessão" "$scenario/log"; then
	fail "launcher não explicou o limite do cookie Secure em HTTP"
fi
if ! grep -q "API em http://127.0.0.1:${ok_api} e painel em http://127.0.0.1:${ok_vite}" "$scenario/log"; then
	fail "anúncio de API e painel não segue a configuração"
fi

echo "dev.test.sh: ok"
