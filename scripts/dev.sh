#!/bin/bash
# Sobe PostgreSQL 18.4 embutido, a API em modo all e o Vite, sem Docker.
# Endereços e portas vêm do ambiente. O bind é só loopback. Se a API falha
# ou este processo recebe um sinal, encerra apenas os filhos que ele iniciou.
set -euo pipefail
set +m
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. ./scripts/lib.sh
use_npm11

root=$PWD
http_addr=${CHATISP_HTTP_ADDR:-127.0.0.1:8080}
metrics_addr=${CHATISP_METRICS_ADDR:-127.0.0.1:9090}
pg_host=${CHATISP_DEV_PG_HOST:-127.0.0.1}
pg_port=${CHATISP_DEV_PG_PORT:-55432}
vite_host=${CHATISP_DEV_VITE_HOST:-127.0.0.1}
vite_port=${CHATISP_DEV_VITE_PORT:-5173}
ready_timeout=${CHATISP_DEV_READY_TIMEOUT:-180}
pg_data=${CHATISP_DEV_PG_DATA:-$root/.tmp/dev-pg}
pg_log=${CHATISP_DEV_PG_LOG:-$root/.tmp/dev-pg.log}

die() {
	echo "dev.sh: $*" >&2
	exit 1
}

# Grava na hora. O stdout do script costuma ir para um arquivo e o bash só
# descarrega o buffer no exit; um sinal no meio da espera de /healthz perderia
# o aviso se ele ficasse só no buffer.
note() {
	python3 -c 'import sys; sys.stdout.write(sys.argv[1] + "\n"); sys.stdout.flush()' "$1"
}

if ! command -v setsid >/dev/null 2>&1; then
	die "setsid não encontrado; ele isola os processos deste script"
fi

meta=$(
	python3 - "$http_addr" "$metrics_addr" "$pg_host" "$pg_port" "$vite_host" "$vite_port" "$ready_timeout" <<'PY'
import ipaddress
import os
import sys

http_raw, metrics_raw, pg_host, pg_port, vite_host, vite_port, timeout = sys.argv[1:]

def split_addr(raw):
    raw = raw.strip()
    if raw.startswith("["):
        end = raw.find("]")
        if end <= 1 or not raw[end + 1 :].startswith(":"):
            raise ValueError(raw)
        return raw[1:end], raw[end + 2 :]
    if raw.count(":") != 1:
        raise ValueError(raw)
    host, port = raw.split(":", 1)
    return host, port

def require_loopback(host):
    try:
        ip = ipaddress.ip_address(host)
    except ValueError as exc:
        raise ValueError(host) from exc
    if not ip.is_loopback:
        raise ValueError(host)
    return host

def require_port(port):
    if not port.isdigit():
        raise ValueError(port)
    number = int(port)
    if number < 1 or number > 65535:
        raise ValueError(port)
    return str(number)

def origin(host, port):
    if ":" in host:
        return f"http://[{host}]:{port}"
    return f"http://{host}:{port}"

def quote(value):
    return "'" + value.replace("'", "'\\''") + "'"

try:
    http_host, http_port = split_addr(http_raw)
    metrics_host, metrics_port = split_addr(metrics_raw)
    http_host = require_loopback(http_host)
    metrics_host = require_loopback(metrics_host)
    pg_host = require_loopback(pg_host.strip())
    vite_host = require_loopback(vite_host.strip())
    http_port = require_port(http_port)
    metrics_port = require_port(metrics_port)
    pg_port = require_port(pg_port)
    vite_port = require_port(vite_port)
    if len({http_port, metrics_port, pg_port, vite_port}) != 4:
        raise ValueError("portas repetidas")
    if not timeout.isdigit() or not 1 <= int(timeout) <= 600:
        raise ValueError("timeout")
except ValueError:
    sys.exit(2)

def emit(name, value):
    print(f"{name}={quote(value)}")

emit("http_host", http_host)
emit("http_port", http_port)
emit("http_addr", f"[{http_host}]:{http_port}" if ":" in http_host else f"{http_host}:{http_port}")
emit("metrics_host", metrics_host)
emit("metrics_port", metrics_port)
emit("metrics_addr", f"[{metrics_host}]:{metrics_port}" if ":" in metrics_host else f"{metrics_host}:{metrics_port}")
emit("pg_host", pg_host)
emit("pg_port", pg_port)
emit("vite_host", vite_host)
emit("vite_port", vite_port)
emit("ready_timeout", str(int(timeout)))
emit("api_origin", origin(http_host, http_port))
panel_origin_value = origin(vite_host, vite_port)
if os.environ.get("CHATISP_DEV_TLS") == "1":
    panel_origin_value = panel_origin_value.replace("http://", "https://", 1)
emit("panel_origin", panel_origin_value)
PY
) || die "use só endereços loopback (127.0.0.0/8 ou ::1), com API, métricas, PostgreSQL e Vite em portas distintas. CHATISP_DEV_READY_TIMEOUT fica entre 1 e 600"
# shellcheck disable=SC2086
eval "$meta"

pg_socket=$(
	python3 - "$pg_data" <<'PY'
import hashlib
import os
import sys

digest = hashlib.sha256(os.path.abspath(sys.argv[1]).encode()).hexdigest()[:12]
print(f"/tmp/chatisp-pg-{os.getuid()}-{digest}")
PY
)
case "$pg_socket" in
/tmp/chatisp-pg-"$(id -u)"-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
*) die "diretório de socket do PostgreSQL inválido" ;;
esac
pg_pid=""
api_pid=""
vite_pid=""
envfile=""
tls_dir=""
cleanup_ran=0

child_running() {
	local pid=$1 state
	[ -n "$pid" ] || return 1
	state=$(ps -o stat= -p "$pid" 2>/dev/null | awk 'NR==1 { print substr($1, 1, 1) }')
	case "$state" in
	R | S | D | T | I) return 0 ;;
	*) return 1 ;;
	esac
}

stop_owned() {
	if [ "$cleanup_ran" = 1 ]; then
		return 0
	fi
	cleanup_ran=1
	if [ -n "$envfile" ]; then
		rm -f -- "$envfile"
		envfile=""
	fi
	if [ -n "$tls_dir" ] && [ -d "$tls_dir" ] && [ ! -L "$tls_dir" ]; then
		rm -rf -- "$tls_dir"
		tls_dir=""
	fi
	python3 - "$pg_pid" "$api_pid" "$vite_pid" "$pg_data" <<'PY' || true
import os
import signal
import sys
import time

raw_pids = sys.argv[1:4]
data = sys.argv[4]
shell = os.getppid()
self_pid = os.getpid()

def read_status(pid):
    try:
        text = open(f"/proc/{pid}/status", encoding="utf-8").read()
    except OSError:
        return None
    info = {}
    for line in text.splitlines():
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        info[key] = value.strip()
    return info

def argv(pid):
    try:
        raw = open(f"/proc/{pid}/cmdline", "rb").read()
    except OSError:
        return []
    parts = raw.split(b"\0")
    if parts and parts[-1] == b"":
        parts.pop()
    return parts

def is_our_postmaster(pid):
    args = argv(pid)
    if not args or not data:
        return False
    base = args[0].rsplit(b"/", 1)[-1]
    if base != b"postgres":
        return False
    target = os.path.abspath(data).encode()
    if target not in args:
        return False
    pid_file = os.path.join(data, "postmaster.pid")
    try:
        lines = open(pid_file, encoding="utf-8").read().splitlines()
    except OSError:
        return False
    return len(lines) >= 2 and lines[0] == str(pid) and os.path.abspath(lines[1]) == os.path.abspath(data)

roots = []
for raw in raw_pids:
    if not raw.isdigit():
        continue
    pid = int(raw)
    if pid <= 1 or pid in (self_pid, shell):
        continue
    info = read_status(pid)
    if not info:
        continue
    if info.get("PPid") == str(shell):
        roots.append(pid)

pid_file = os.path.join(data, "postmaster.pid")
if os.path.isfile(pid_file):
    try:
        first = open(pid_file, encoding="utf-8").readline().strip()
    except OSError:
        first = ""
    if first.isdigit():
        postmaster = int(first)
        if postmaster > 1 and postmaster not in roots and is_our_postmaster(postmaster):
            roots.append(postmaster)

def descendants(root):
    children = {}
    for name in os.listdir("/proc"):
        if not name.isdigit():
            continue
        info = read_status(int(name))
        if not info or "PPid" not in info:
            continue
        children.setdefault(int(info["PPid"]), []).append(int(name))
    found = []
    stack = [root]
    seen = {root}
    while stack:
        cur = stack.pop()
        for child in children.get(cur, []):
            if child in seen or child in (self_pid, shell) or child <= 1:
                continue
            seen.add(child)
            found.append(child)
            stack.append(child)
    return found

def still_in_tree(pid, root):
    if pid == root:
        info = read_status(pid)
        if not info:
            return False
        if is_our_postmaster(pid):
            return True
        return info.get("PPid") == str(shell) or info.get("NSpgid") == str(root)
    seen = set()
    cur = pid
    while cur not in seen and cur > 1:
        seen.add(cur)
        if cur == root:
            return True
        info = read_status(cur)
        if not info:
            return False
        parent = int(info.get("PPid", "0") or "0")
        if parent <= 1:
            return False
        cur = parent
    return False

def signal_pid(pid, sig):
    info = read_status(pid)
    if not info:
        return
    pgid = info.get("NSpgid", "")
    ppid = info.get("PPid", "")
    try:
        if pgid == str(pid) and ppid == str(shell) and pid not in (self_pid, shell):
            os.killpg(pid, sig)
            return
    except OSError:
        pass
    try:
        os.kill(pid, sig)
    except OSError:
        pass

def deliver(sig):
    for root in roots:
        if not still_in_tree(root, root):
            continue
        for pid in list(descendants(root)) + [root]:
            if still_in_tree(pid, root):
                signal_pid(pid, sig)

def lingering():
    found = []
    for root in roots:
        for pid in [root, *descendants(root)]:
            info = read_status(pid)
            if not info or info.get("State", "").startswith("Z"):
                continue
            if still_in_tree(pid, root):
                found.append(pid)
    return found

deliver(signal.SIGTERM)
deadline = time.time() + 5
while time.time() < deadline and lingering():
    time.sleep(0.1)
if lingering():
    deliver(signal.SIGKILL)
PY
	if [ -n "$pg_pid" ]; then
		wait "$pg_pid" 2>/dev/null || true
	fi
	if [ -n "$api_pid" ]; then
		wait "$api_pid" 2>/dev/null || true
	fi
	if [ -n "$vite_pid" ]; then
		wait "$vite_pid" 2>/dev/null || true
	fi
	case "$pg_socket" in
	/tmp/chatisp-pg-"$(id -u)"-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f])
		if [ -d "$pg_socket" ] && [ ! -L "$pg_socket" ]; then
			rm -rf -- "$pg_socket"
		fi
		;;
	esac
}

trap 'stop_owned; exit 129' HUP
trap 'stop_owned; exit 130' INT
trap 'stop_owned; exit 143' TERM
trap 'stop_owned' EXIT

port_is_open() {
	python3 - "$1" "$2" <<'PY'
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
family = socket.AF_INET6 if ":" in host else socket.AF_INET
sock = socket.socket(family, socket.SOCK_STREAM)
sock.settimeout(0.3)
try:
    sock.connect((host, port))
except OSError:
    sys.exit(1)
finally:
    sock.close()
sys.exit(0)
PY
}

if port_is_open "$http_host" "$http_port"; then
	die "API ${http_addr} já está em uso. Escolha outro CHATISP_HTTP_ADDR. Este script não altera o processo que escuta aí"
fi
if port_is_open "$metrics_host" "$metrics_port"; then
	die "métricas ${metrics_addr} já estão em uso. Escolha outro CHATISP_METRICS_ADDR. Este script não altera o processo que escuta aí"
fi
if port_is_open "$pg_host" "$pg_port"; then
	die "PostgreSQL ${pg_host}:${pg_port} já está em uso. Escolha outro CHATISP_DEV_PG_PORT. Este script não altera o processo que escuta aí"
fi
if port_is_open "$vite_host" "$vite_port"; then
	die "Vite ${panel_origin} já está em uso. Escolha outro CHATISP_DEV_VITE_PORT. Este script não altera o processo que escuta aí"
fi

pkg="$root/backend/tools/pg/node_modules/@embedded-postgres/linux-x64"
if [ -n "${CHATISP_DEV_POSTGRES:-}" ] || [ -n "${CHATISP_DEV_INITDB:-}" ]; then
	if [ -z "${CHATISP_DEV_POSTGRES:-}" ] || [ -z "${CHATISP_DEV_INITDB:-}" ]; then
		die "defina CHATISP_DEV_POSTGRES e CHATISP_DEV_INITDB juntos, ou nenhum dos dois"
	fi
	postgres_bin=$CHATISP_DEV_POSTGRES
	initdb_bin=$CHATISP_DEV_INITDB
else
	postgres_bin=$pkg/native/bin/postgres
	initdb_bin=$pkg/native/bin/initdb
	if [ ! -x "$initdb_bin" ]; then
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
	export LD_LIBRARY_PATH="$pkg/native/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

mkdir -p "$(dirname "$pg_data")" "$(dirname "$pg_log")"
export LC_ALL=C LANG=C TZ=UTC
if [ ! -f "$pg_data/PG_VERSION" ]; then
	"$initdb_bin" -D "$pg_data" -U postgres -A trust -E UTF8 --no-sync
fi
mkdir -p "$pg_socket"
chmod 700 "$pg_socket"

setsid "$postgres_bin" -D "$pg_data" \
	-c listen_addresses="$pg_host" \
	-c port="$pg_port" \
	-c unix_socket_directories="$pg_socket" \
	-c shared_buffers=16MB \
	-c dynamic_shared_memory_type=mmap \
	-c fsync=off \
	>>"$pg_log" 2>&1 &
pg_pid=$!

deadline=$((SECONDS + ready_timeout))
pg_ready=0
while [ "$SECONDS" -lt "$deadline" ]; do
	if ! child_running "$pg_pid"; then
		break
	fi
	if python3 - "$pg_host" "$pg_port" <<'PY'
import socket
import sys

host, port = sys.argv[1], int(sys.argv[2])
family = socket.AF_INET6 if ":" in host else socket.AF_INET
sock = socket.socket(family, socket.SOCK_STREAM)
sock.settimeout(0.5)
try:
    sock.connect((host, port))
except OSError:
    sys.exit(1)
finally:
    sock.close()
PY
	then
		pg_ready=1
		break
	fi
	sleep 0.1
done
if [ "$pg_ready" != 1 ]; then
	die "PostgreSQL local não aceitou conexão em ${pg_host}:${pg_port}"
fi

if [ "$pg_host" = "::1" ]; then
	pg_url_host="[::1]"
else
	pg_url_host=$pg_host
fi
envfile=$(mktemp)
chmod 600 "$envfile"
go -C backend run -mod=vendor ./cmd/chatisp dev-prepare \
	-url "postgresql://postgres@${pg_url_host}:${pg_port}/postgres?sslmode=disable" >"$envfile"
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
	CHATISP_[A-Z0-9_]*=*)
		export "$line"
		;;
	esac
done <"$envfile"
rm -f -- "$envfile"
envfile=""

if [ -z "${CHATISP_DEV_EMAIL:-}" ]; then
	die "dev-prepare não informou CHATISP_DEV_EMAIL"
fi
echo "Usuário local: ${CHATISP_DEV_EMAIL}"
if [ "${CHATISP_DEV_SEEDED:-0}" = "1" ]; then
	echo "Senha local, só nesta subida: ${CHATISP_DEV_PASSWORD:-}"
else
	echo "O usuário local já existia. A senha não foi alterada."
fi
unset CHATISP_DEV_PASSWORD || true
unset CHATISP_DATABASE_URL || true

export CHATISP_ENV=development
export CHATISP_HTTP_ADDR="$http_addr"
export CHATISP_METRICS_ADDR="$metrics_addr"
export CHATISP_PANEL_ORIGINS="$panel_origin"
export CHATISP_LOG_LEVEL="${CHATISP_LOG_LEVEL:-info}"
export CHATISP_MIGRATIONS_DIR="$root/backend/db/migrations"
export CHATISP_DEV_API_ORIGIN="$api_origin"
export CHATISP_DEV_VITE_HOST="$vite_host"
export CHATISP_DEV_VITE_PORT="$vite_port"
export CHATISP_META_SIMULATION="${CHATISP_META_SIMULATION:-true}"
export CHATISP_META_VERIFY_TOKEN="${CHATISP_META_VERIFY_TOKEN:-dev-meta-verify-token}"
export CHATISP_META_APP_ID="${CHATISP_META_APP_ID:-dev-meta-app-id}"

note "API em ${api_origin} e painel em ${panel_origin}"
note "Métricas em ${metrics_addr}"
if [ "${CHATISP_DEV_TLS:-0}" = "1" ]; then
	command -v openssl >/dev/null 2>&1 || die "CHATISP_DEV_TLS=1 exige openssl"
	tls_dir=$(mktemp -d /tmp/chatisp-dev-tls-XXXXXX)
	chmod 700 "$tls_dir"
	openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=chatisp-dev-loopback" \
		-addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1" \
		-keyout "$tls_dir/key.pem" -out "$tls_dir/cert.pem" >/dev/null 2>&1 || die "não foi possível gerar o certificado local"
	export CHATISP_DEV_TLS_KEY="$tls_dir/key.pem"
	export CHATISP_DEV_TLS_CERT="$tls_dir/cert.pem"
	note "HTTPS local ligado: certificado autoassinado descartável (1 dia, só loopback, apagado ao sair). O navegador pedirá para aceitá-lo. O cookie Secure funciona nesta origem."
else
	note "O cookie de sessão permanece Secure. Em HTTP o navegador não guarda essa sessão. Para uma sessão real use CHATISP_DEV_TLS=1 ./scripts/dev.sh ou o Compose com Caddy em https://localhost:8443."
fi

setsid go -C backend run -mod=vendor ./cmd/chatisp all &
api_pid=$!
setsid npm --prefix "$root/web" exec vite -- --config "$root/scripts/vite.dev.mjs" --host "$vite_host" --port "$vite_port" --strictPort &
vite_pid=$!

deadline=$((SECONDS + ready_timeout))
api_ready=0
while [ "$SECONDS" -lt "$deadline" ]; do
	if ! child_running "$api_pid"; then
		wait "$api_pid" 2>/dev/null || true
		break
	fi
	if python3 - "$api_origin" <<'PY'
import sys
import urllib.request

try:
    with urllib.request.urlopen(sys.argv[1] + "/healthz", timeout=1) as response:
        if response.status != 200:
            sys.exit(1)
except Exception:
    sys.exit(1)
PY
	then
		api_ready=1
		break
	fi
	sleep 0.1
done
if [ "$api_ready" != 1 ]; then
	die "a API não ficou pronta em ${api_origin}. PostgreSQL, API e Vite deste script foram encerrados"
fi

note "API pronta em ${api_origin}; painel em ${panel_origin}. $( [ "${CHATISP_DEV_TLS:-0}" = "1" ] && echo "Abra o painel em HTTPS." || echo "O cookie de sessão permanece Secure e em HTTP o navegador não guarda essa sessão.")"

set +e
wait -n
status=$?
set -e
exit "$status"
