#!/usr/bin/env bash
# Snapshot local específico; contém segredos em texto claro. Uso manual em janela coordenada.
set -Eeuo pipefail
umask 077
[[ $# -eq 0 || ( $# -eq 1 && $1 == --check ) ]] || { echo "Uso: $0 [--check]" >&2; exit 2; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
cd -- "$root"
private=/home/enok/.local/share/chatisp/pronto-hoje-20261106
project=chatisp-pronto-hoje
ctl=(python3 "$private/controle.py")
outroot=/home/enok/.local/share/chatisp/backups-publicacao
containers=("$project-postgres-1" "$project-garage-1" "$project-chatisp-api-1" "$project-chatisp-worker-1")
volumes=(garage-config garage-meta garage-data)
images=("$project:runtime" "$project:edge" dxflrs/garage:v2.5.0 postgres:18.6-alpine)
# A identidade e os caminhos são verificados antes de qualquer interrupção.
python3 - "$private/estado.json" "$root" "$project" <<'PY'
import json,sys,os
with open(sys.argv[1]) as f: state=json.load(f)
assert os.path.realpath(state['source_root'])==sys.argv[2] and state['project']==sys.argv[3], 'instalação ou código divergente'
PY
for container in "${containers[@]}"; do
  [[ $(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}} {{.State.Status}} {{.State.Health.Status}}' "$container") == "$project running healthy" ]] || { echo "Contêiner não saudável ou projeto divergente: $container" >&2; exit 1; }
done
for volume in "${volumes[@]}"; do
  [[ $(docker volume inspect -f '{{index .Labels "com.docker.compose.project"}} {{index .Labels "com.docker.compose.volume"}}' "${project}_${volume}") == "$project $volume" ]] || { echo "Volume ausente ou projeto divergente: $volume" >&2; exit 1; }
done
for image in "${images[@]}"; do docker image inspect "$image" >/dev/null; done
mkdir -p -m 700 "$outroot"
# Estimativa conservadora: tamanhos descompactados das imagens, volumes e 1 GiB para dump/config.
image_bytes=$(docker image inspect -f '{{.Size}}' "${images[@]}" | awk '{n+=$1} END {printf "%.0f",n}')
volume_kb=$(docker run --rm --network none --entrypoint sh \
  -v "${project}_garage-config:/backup/config:ro" -v "${project}_garage-meta:/backup/meta:ro" \
  -v "${project}_garage-data:/backup/data:ro" postgres:18.6-alpine -c 'du -sk /backup | cut -f1')
postgres_kb=$(docker exec "$project-postgres-1" du -sk /var/lib/postgresql | awk '{print $1}')
available_kb=$(df -Pk "$outroot" | awk 'NR==2 {print $4}')
(( available_kb > volume_kb + postgres_kb + image_bytes / 1024 + 1048576 )) || { echo 'Espaço livre insuficiente para estimativa conservadora.' >&2; exit 1; }
if [[ ${1:-} == --check ]]; then echo "Pré-verificações concluídas; serviços intactos."; exit 0; fi
out=$(mktemp -d "$outroot/snapshot-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXXXX")
chmod 700 "$out"
needs_restart=0
restart() {
  (( needs_restart )) || return 0
  local failed=0 healthy=0
  docker start "$project-garage-1" >/dev/null || failed=1
  for _ in {1..60}; do
    if [[ $(docker inspect -f '{{.State.Health.Status}}' "$project-garage-1" 2>/dev/null) == healthy ]]; then healthy=1; break; fi
    sleep 1
  done
  (( healthy )) || { echo 'Garage não ficou saudável em 60 segundos.' >&2; failed=1; }
  # Mesmo em erro, tentativa de retornar ambos os serviços ao estado anterior.
  docker start "$project-chatisp-api-1" "$project-chatisp-worker-1" >/dev/null || failed=1
  needs_restart=0
  return "$failed"
}
trap restart EXIT
needs_restart=1
"${ctl[@]}" stop chatisp-api chatisp-worker
docker exec "$project-postgres-1" pg_dump -U chatisp -d chatisp -Fc > "$out/postgres.dump"
docker exec "$project-postgres-1" pg_dumpall -U chatisp --globals-only > "$out/postgres-globals.sql"
"${ctl[@]}" stop garage
docker run --rm --network none --entrypoint sh \
  -v "${project}_garage-config:/backup/config:ro" -v "${project}_garage-meta:/backup/meta:ro" \
  -v "${project}_garage-data:/backup/data:ro" postgres:18.6-alpine -c 'tar -C /backup -czf - config meta data' > "$out/garage.tar.gz"
tar -C "$(dirname "$private")" -czf "$out/config-privada.tar.gz" "$(basename "$private")"
# Inclui overrides e Caddyfiles, mesmo se não selecionados na instalação.
tar -C "$root" -czf "$out/compose-fontes.tar.gz" deploy/compose*.yaml deploy/Caddyfile* deploy/garage deploy/postgres/init
{
  git -C "$root" rev-parse HEAD
  git -C "$root" status --short
  docker image inspect -f '{{.Id}} {{join .RepoTags ","}}' "${images[@]}"
} > "$out/versoes.txt"
restart
trap - EXIT
# Aguarda saúde final antes de liberar a instalação.
for container in "${containers[@]}"; do
  healthy=0
  for _ in {1..60}; do
    if [[ $(docker inspect -f '{{.State.Status}} {{.State.Health.Status}}' "$container") == 'running healthy' ]]; then healthy=1; break; fi
    sleep 1
  done
  (( healthy )) || { echo "Contêiner não saudável: $container" >&2; exit 1; }
done
# Salva versões exatas locais, sem baixar imagens.
docker image save "${images[@]}" | gzip > "$out/imagens.tar.gz"
(cd "$out" && sha256sum postgres.dump postgres-globals.sql garage.tar.gz config-privada.tar.gz compose-fontes.tar.gz versoes.txt imagens.tar.gz > SHA256SUMS)
chmod 600 "$out"/*
printf '%s\n' "$out"
