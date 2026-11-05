#!/usr/bin/env bash
# Valida deploy/compose.yaml e o Caddy em ambiente isolado: projeto próprio, volumes próprios,
# porta de loopback livre, segredos descartáveis gerados na hora. Não toca em outros projetos.
# Requer Docker >= 20.10.10 com a ponte docker0 e Compose >= 2.24.4. Na máquina de desenvolvimento
# (Docker 20.10.9 sem docker0) o build das imagens falha antes de subir; veja docs/operacao.md.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
project="chatisp-validate-$$"
tmp=$(mktemp -d /tmp/chatisp-compose-XXXXXX)
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
r() { openssl rand -hex 12; }
cat >"$tmp/env" <<ENV
POSTGRES_PASSWORD=$(r)
CHATISP_OWNER_PASSWORD=$(r)
CHATISP_APP_PASSWORD=$(r)
CHATISP_PLATFORM_PASSWORD=$(r)
CHATISP_INGRESS_PASSWORD=$(r)
CHATISP_JOBS_PASSWORD=$(r)
CHATISP_DATA_KEYS=1:$(openssl rand -base64 32)
CHATISP_DATA_KEY_VERSION=1
CHATISP_META_VERIFY_TOKEN=$(r)
ENV
chmod 600 "$tmp/env"
cat >"$tmp/override.yaml" <<YAML
services:
  caddy:
    ports: !override
      - "127.0.0.1:${port}:443"
YAML
compose=(docker compose -p "$project" -f "$root/deploy/compose.yaml" -f "$tmp/override.yaml" --env-file "$tmp/env")
cleanup() { "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT
"${compose[@]}" config -q
"${compose[@]}" up -d --build --wait --wait-timeout 300
curl -fsSk "https://127.0.0.1:${port}/healthz" >/dev/null
curl -fsSk "https://127.0.0.1:${port}/readyz" >/dev/null
curl -fsSk "https://127.0.0.1:${port}/entrar" | grep -qi '<div id="root"'
echo "compose ok: postgres, garage, migrate, api, worker e caddy (https://127.0.0.1:${port})"
