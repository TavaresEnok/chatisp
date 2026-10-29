#!/bin/sh
# Gera a configuração do Garage no volume, na primeira subida.
# O arquivo resultante fica no volume do Compose e não é versionado.
set -eu

cfg=/etc/garage/garage.toml
if [ -f "$cfg" ]; then
	exit 0
fi
mkdir -p /etc/garage

hex() {
	od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'
}

rpc=$(hex 32)
admin=$(hex 32)
metrics=$(hex 32)

cat >"$cfg" <<EOF
metadata_dir = "/var/lib/garage/meta"
data_dir = "/var/lib/garage/data"
db_engine = "sqlite"
replication_factor = 1

rpc_bind_addr = "[::]:3901"
rpc_public_addr = "garage:3901"
rpc_secret = "${rpc}"

[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
root_domain = ".s3.garage.localhost"

[s3_web]
bind_addr = "[::]:3902"
root_domain = ".web.garage.localhost"
index = "index.html"

[admin]
api_bind_addr = "[::]:3903"
admin_token = "${admin}"
metrics_token = "${metrics}"
EOF
chmod 644 "$cfg"
