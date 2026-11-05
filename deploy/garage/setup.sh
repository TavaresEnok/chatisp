#!/bin/sh
# Provisiona, de forma idempotente, o bucket privado e a chave da aplicação no
# Garage do Compose, pela API administrativa v2 (rede interna do Compose).
# Roda a cada subida: cria só o que falta e confere o que existe.
# Não imprime o token administrativo nem o segredo da chave.
set -eu

: "${S3_BUCKET:?defina CHATISP_ATTACHMENTS_S3_BUCKET}"
: "${S3_ACCESS_KEY:?defina CHATISP_ATTACHMENTS_S3_ACCESS_KEY}"
: "${S3_SECRET_KEY:?defina CHATISP_ATTACHMENTS_S3_SECRET_KEY}"

case "$S3_ACCESS_KEY" in
GK[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
*) echo "CHATISP_ATTACHMENTS_S3_ACCESS_KEY deve ser GK seguido de 24 hexadecimais (gere com: echo GK\$(openssl rand -hex 12))" >&2; exit 1 ;;
esac
if ! printf '%s' "$S3_SECRET_KEY" | grep -Eq '^[0-9a-f]{64}$'; then
	echo "CHATISP_ATTACHMENTS_S3_SECRET_KEY deve ter 64 hexadecimais (gere com: openssl rand -hex 32)" >&2
	exit 1
fi

cfg=${GARAGE_CONFIG:-/etc/garage/garage.toml}
token=$(sed -n 's/^admin_token = "\(.*\)"$/\1/p' "$cfg")
if [ -z "$token" ]; then
	echo "token administrativo do Garage ausente em $cfg" >&2
	exit 1
fi
api=${GARAGE_ADMIN_URL:-http://garage:3903}
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# request MÉTODO CAMINHO [CORPO]: grava a resposta em $tmp e imprime o status HTTP.
request() {
	if [ $# -ge 3 ]; then
		curl -sS -o "$tmp" -w '%{http_code}' -X "$1" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' --data "$3" "$api$2"
	else
		curl -sS -o "$tmp" -w '%{http_code}' -X "$1" -H "Authorization: Bearer $token" "$api$2"
	fi
}

healthy=""
for _ in $(seq 1 60); do
	if [ "$(request GET /v2/GetClusterHealth 2>/dev/null || true)" = 200 ] && grep -q '"status": *"healthy"' "$tmp"; then
		healthy=1
		break
	fi
	sleep 1
done
if [ -z "$healthy" ]; then
	echo "Garage não ficou saudável" >&2
	exit 1
fi

status=$(request GET "/v2/GetBucketInfo?globalAlias=$S3_BUCKET")
if [ "$status" != 200 ]; then
	status=$(request POST /v2/CreateBucket "{\"globalAlias\":\"$S3_BUCKET\"}")
	if [ "$status" != 200 ]; then
		echo "falha ao criar o bucket ($status)" >&2
		exit 1
	fi
	echo "bucket criado"
else
	echo "bucket já existe"
fi
bucket_id=$(grep -o '"id": *"[0-9a-f]*"' "$tmp" | head -1 | sed 's/.*"\([0-9a-f]*\)"$/\1/')
if [ -z "$bucket_id" ]; then
	echo "id do bucket ausente na resposta do Garage" >&2
	exit 1
fi
if grep -q '"websiteAccess": *true' "$tmp"; then
	echo "o bucket está com acesso web público; recusado" >&2
	exit 1
fi

status=$(request GET "/v2/GetKeyInfo?id=$S3_ACCESS_KEY&showSecretKey=true")
if [ "$status" = 200 ]; then
	if ! grep -q "\"secretAccessKey\": *\"$S3_SECRET_KEY\"" "$tmp"; then
		echo "a chave $S3_ACCESS_KEY já existe com outro segredo; recusado" >&2
		exit 1
	fi
	echo "chave já existe"
else
	status=$(request POST /v2/ImportKey "{\"accessKeyId\":\"$S3_ACCESS_KEY\",\"secretAccessKey\":\"$S3_SECRET_KEY\",\"name\":\"chatisp-app\"}")
	if [ "$status" != 200 ]; then
		echo "falha ao importar a chave ($status)" >&2
		exit 1
	fi
	echo "chave importada"
fi

status=$(request POST /v2/AllowBucketKey "{\"bucketId\":\"$bucket_id\",\"accessKeyId\":\"$S3_ACCESS_KEY\",\"permissions\":{\"read\":true,\"write\":true,\"owner\":false}}")
if [ "$status" != 200 ]; then
	echo "falha ao conceder leitura e escrita no bucket ($status)" >&2
	exit 1
fi
echo "permissões conferidas: leitura e escrita, sem owner"
