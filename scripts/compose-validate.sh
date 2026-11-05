#!/usr/bin/env bash
# Valida deploy/compose.yaml e o Caddy em ambiente isolado: projeto próprio, volumes próprios,
# imagens com etiqueta própria, porta de loopback livre e segredos descartáveis gerados na hora.
# Prova: build, subida com healthchecks, migrações, HTTPS, primeiro administrador (bootstrap),
# login pelo Caddy, reinício com dados preservados e encerramento sem sobras do próprio projeto.
# Não toca em outros projetos e não faz limpeza global.
# Requer Docker >= 20.10.10 com rede de build funcional e Compose >= 2.24.4. No Docker 20.10.9
# sem docker0 o build falha antes de subir; veja docs/medicoes/verificacao-entrega-20261105.md.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
project="chatisp-validate-$$"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/chatisp-compose-XXXXXX")
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
r() { openssl rand -hex 12; }
admin_email="admin@validacao.example"
admin_password="$(r)$(r)"
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
runtime_image="${project}:runtime"
edge_image="${project}:edge"
cat >"$tmp/override.yaml" <<YAML
services:
  chatisp-migrate:
    image: ${runtime_image}
  chatisp-api:
    image: ${runtime_image}
  chatisp-worker:
    image: ${runtime_image}
  caddy:
    image: ${edge_image}
    ports: !override
      - "127.0.0.1:${port}:443"
YAML
compose=(docker compose -p "$project" -f "$root/deploy/compose.yaml" -f "$tmp/override.yaml" --env-file "$tmp/env")
base="https://127.0.0.1:${port}"

cleanup() {
	"${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
	docker image rm "$runtime_image" "$edge_image" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

wait_ready() {
	for _ in $(seq 1 60); do
		if curl -fsSk "$base/readyz" >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	echo "readyz não respondeu em $base" >&2
	return 1
}

login() {
	local code
	code=$(curl -sSk -o "$tmp/login.json" -w '%{http_code}' -c "$tmp/cookies" \
		-H 'Content-Type: application/json' \
		--data "{\"email\":\"${admin_email}\",\"password\":\"${admin_password}\"}" \
		"$base/api/panel/session")
	if [ "$code" != 200 ]; then
		echo "login respondeu $code" >&2
		cat "$tmp/login.json" >&2
		return 1
	fi
	grep -q '__Host-session' "$tmp/cookies"
}

"${compose[@]}" config -q
"${compose[@]}" up -d --build --wait --wait-timeout 300
curl -fsSk "$base/healthz" >/dev/null
wait_ready
curl -fsSk "$base/entrar" | grep -qi '<div id="root"'
echo "subida ok: postgres, garage, migrações, api, worker e caddy ($base)"

printf '%s\n' "$admin_password" | "${compose[@]}" run --rm -T --no-deps chatisp-migrate \
	bootstrap -email "$admin_email" -superadmin
login
echo "login ok pelo Caddy com o administrador do bootstrap"

"${compose[@]}" restart
"${compose[@]}" up -d --wait --wait-timeout 300
wait_ready
login
printf '%s\n' "$admin_password" | "${compose[@]}" run --rm -T --no-deps chatisp-migrate \
	bootstrap -email "$admin_email" | grep -q 'nada foi alterado'
echo "reinício ok: o mesmo administrador entra e o bootstrap não recria nada"

"${compose[@]}" down -v --remove-orphans
docker image rm "$runtime_image" "$edge_image" >/dev/null
left=$(docker ps -aq --filter "label=com.docker.compose.project=${project}"; docker volume ls -q --filter "label=com.docker.compose.project=${project}"; docker network ls -q --filter "label=com.docker.compose.project=${project}")
if [ -n "$left" ]; then
	echo "sobraram recursos do projeto ${project}:" >&2
	echo "$left" >&2
	exit 1
fi
echo "compose ok: build, healthchecks, migrações, HTTPS, login, reinício e encerramento sem sobras ($project)"
