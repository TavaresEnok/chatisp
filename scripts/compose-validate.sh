#!/usr/bin/env bash
# Valida deploy/compose.yaml e o Caddy em ambiente isolado: projeto próprio, volumes próprios,
# imagens com etiqueta própria, porta de loopback livre e segredos descartáveis gerados na hora.
# Prova: build, subida com healthchecks, migrações, HTTPS, primeiro administrador (bootstrap),
# login pelo Caddy, reinício com dados preservados e encerramento sem sobras do próprio projeto.
# Não toca em outros projetos e não faz limpeza global.
# Requer Docker >= 20.10.10 com rede de build funcional e Compose >= 2.24.4. No Docker 20.10.9
# sem docker0 o build Debian falha; CHATISP_VALIDATE_ALPINE=1 valida com base musl e rede do host
# (veja docs/medicoes/verificacao-entrega-20261105.md).
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
build_extra=""
if [ "${CHATISP_VALIDATE_ALPINE:-0}" = 1 ]; then
	# Docker 20.10.9: sem docker0 e com seccomp que nega clone3. Build na rede do host e
	# com base musl (deploy/validacao/Dockerfile.alpine). Não prova a imagem Debian.
	build_extra="
    build:
      context: ${root}
      dockerfile: deploy/validacao/Dockerfile.alpine
      network: host"
fi
postgres_extra=""
pulled_images=()
if [ -n "$build_extra" ]; then
	docker image inspect postgres:18.6-alpine >/dev/null 2>&1 || pulled_images+=(postgres:18.6-alpine)
	# O initdb do postgres:18.6-trixie também falha nesse host (clone3 negado); mesma versão em musl.
	postgres_extra="
  postgres:
    image: postgres:18.6-alpine"
fi
cat >"$tmp/override.yaml" <<YAML
services:${postgres_extra}
  chatisp-migrate:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  chatisp-api:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  chatisp-worker:
    image: ${runtime_image}${build_extra:+${build_extra}
      target: runtime}
  caddy:
    image: ${edge_image}${build_extra:+${build_extra}
      target: edge}
    ports: !override
      - "127.0.0.1:${port}:443"
YAML
# Sub-rede própria: hosts cujo default-address-pools já está esgotado não criam a rede padrão.
# CHATISP_VALIDATE_SUBNET escolhe uma; sem ela, a primeira 172.31.N.0/24 sem rota nem rede Docker.
subnet=${CHATISP_VALIDATE_SUBNET:-$(python3 - <<'PY'
import ipaddress, json, subprocess
used = []
for line in subprocess.run(["ip", "-4", "route"], capture_output=True, text=True).stdout.splitlines():
    first = line.split()[0] if line.split() else ""
    try:
        used.append(ipaddress.ip_network(first, strict=False))
    except ValueError:
        pass
ids = subprocess.run(["docker", "network", "ls", "-q"], capture_output=True, text=True).stdout.split()
if ids:
    out = subprocess.run(["docker", "network", "inspect", *ids], capture_output=True, text=True).stdout
    for net in json.loads(out or "[]"):
        for cfg in (net.get("IPAM") or {}).get("Config") or []:
            if cfg.get("Subnet"):
                used.append(ipaddress.ip_network(cfg["Subnet"], strict=False))
for n in range(240, 255):
    cand = ipaddress.ip_network(f"172.31.{n}.0/24")
    if not any(cand.overlaps(u) for u in used):
        print(cand)
        break
PY
)}
if [ -z "$subnet" ]; then
	echo "nenhuma sub-rede livre; defina CHATISP_VALIDATE_SUBNET" >&2
	exit 1
fi
cat >>"$tmp/override.yaml" <<YAML
networks:
  default:
    ipam:
      config:
        - subnet: ${subnet}
YAML
compose=(docker compose -p "$project" -f "$root/deploy/compose.yaml" -f "$tmp/override.yaml" --env-file "$tmp/env")
base="https://127.0.0.1:${port}"

cleanup() {
	local status=$?
	if [ "$status" -ne 0 ]; then
		echo "falha ($status); logs do projeto ${project}:" >&2
		"${compose[@]}" ps -a >&2 || true
		"${compose[@]}" logs --no-color --tail 60 >&2 || true
	fi
	"${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
	docker image rm "$runtime_image" "$edge_image" >/dev/null 2>&1 || true
	# Só as imagens que este script baixou (ausentes antes da execução).
	if [ "${#pulled_images[@]}" -gt 0 ]; then
		docker image rm "${pulled_images[@]}" >/dev/null 2>&1 || true
	fi
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
# Uma construção por etiqueta: api e worker reutilizam a imagem do migrate (sem sobras sem etiqueta).
"${compose[@]}" build chatisp-migrate caddy
"${compose[@]}" up -d --no-build --wait --wait-timeout 300
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
