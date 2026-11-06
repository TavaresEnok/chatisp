#!/usr/bin/env bash
# Simula um backup filho que termina mais tarde ao receber SIGTERM.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin" "$tmp/run"
cat >"$tmp/bin/bash" <<'MOCK'
#!/bin/bash
: >"$CHATISP_VALIDATE_TMP/env"
: >"$CHATISP_VALIDATE_TMP/override.yaml"
/bin/bash -c 'trap '\''sleep 0.4; echo child-finished >>"$TEST_LOG"; exit 0'\'' TERM; echo started >"$TEST_READY"; while :; do sleep 1; done' &
wait
MOCK
cat >"$tmp/bin/docker" <<'MOCK'
#!/bin/bash
if [[ " $* " == *' down '* ]]; then echo down >>"$TEST_LOG"; fi
MOCK
chmod +x "$tmp/bin/bash" "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" TMPDIR="$tmp/run" TEST_LOG="$tmp/log" TEST_READY="$tmp/ready"
/bin/bash "$root/scripts/compose-validate.sh" >"$tmp/output" 2>&1 &
pid=$!
for _ in {1..100}; do [ -f "$TEST_READY" ] && break; sleep 0.05; done
[ -f "$TEST_READY" ] || { echo 'filho não iniciou' >&2; exit 1; }
kill -TERM "$pid"
status=0
wait "$pid" || status=$?
[ "$status" = 143 ] || { echo "status inesperado: $status" >&2; exit 1; }
[ "$(cat "$TEST_LOG")" = $'child-finished\ndown' ] || { echo 'limpeza ocorreu antes do fim do filho' >&2; exit 1; }
[ -z "$(ls -A "$TMPDIR")" ] || { echo 'diretório temporário não removido' >&2; exit 1; }
echo 'ok: SIGTERM aguarda descendentes antes da limpeza'
