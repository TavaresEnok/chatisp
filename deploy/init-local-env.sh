#!/bin/sh
# Cria uma configuração persistente para o laboratório sem substituir uma instalação.
set -eu
umask 077
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
destination=${1:-"$dir/.env"}
if [ -e "$destination" ] || [ -L "$destination" ]; then
	echo "Configuração já existe; não foi alterada: $destination" >&2
	exit 1
fi
command -v openssl >/dev/null 2>&1 || { echo "Instale openssl para gerar os segredos locais." >&2; exit 1; }
tmp=$(mktemp "${destination}.tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
pg=$(openssl rand -hex 32)
owner=$(openssl rand -hex 32)
app=$(openssl rand -hex 32)
platform=$(openssl rand -hex 32)
ingress=$(openssl rand -hex 32)
jobs=$(openssl rand -hex 32)
data=$(openssl rand -base64 32)
access=$(openssl rand -hex 12)
secret=$(openssl rand -hex 32)
cat >"$tmp" <<EOF
# Gerado para laboratório local; mantenha este arquivo privado e preserve-o com os backups.
POSTGRES_PASSWORD=$pg
CHATISP_OWNER_PASSWORD=$owner
CHATISP_APP_PASSWORD=$app
CHATISP_PLATFORM_PASSWORD=$platform
CHATISP_INGRESS_PASSWORD=$ingress
CHATISP_JOBS_PASSWORD=$jobs
CHATISP_DATA_KEYS=1:$data
CHATISP_DATA_KEY_VERSION=1
CHATISP_ATTACHMENTS_S3_ACCESS_KEY=GK$access
CHATISP_ATTACHMENTS_S3_SECRET_KEY=$secret
EOF
# link é atômico e falha se outro processo criar o destino durante a geração.
if ! ln "$tmp" "$destination"; then
	echo "Não foi possível criar $destination sem substituir um arquivo existente." >&2
	exit 1
fi
echo "Configuração local criada com permissão restrita em $destination."
