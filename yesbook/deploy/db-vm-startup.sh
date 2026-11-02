#!/bin/bash
# Startup script for the free-tier PostgreSQL VM (Debian 13, PostgreSQL 17).
# The first boot needs outbound internet to install packages and the
# yesbook-db-password metadata value to create the role; later boots only
# make sure the server runs. Clients connect from the VPC subnet over TLS.
set -euo pipefail
MD=http://metadata.google.internal/computeMetadata/v1/instance/attributes
SUBNET="$(curl -sf -H 'Metadata-Flavor: Google' "${MD}/yesbook-db-subnet" || echo 10.128.0.0/20)"

if ! command -v pg_lsclusters >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y postgresql
fi

CONF="$(find /etc/postgresql -mindepth 2 -maxdepth 2 -type d -name main -print -quit)"
if [[ ! -f "${CONF}/conf.d/yesbook.conf" ]]; then
  cat > "${CONF}/conf.d/yesbook.conf" <<EOF
listen_addresses = '*'
ssl = on
max_connections = 50
EOF
  echo "hostssl yesbook yesbook ${SUBNET} scram-sha-256" >> "${CONF}/pg_hba.conf"
  systemctl restart postgresql
fi
systemctl enable --now postgresql

PASS="$(curl -sf -H 'Metadata-Flavor: Google' "${MD}/yesbook-db-password" || true)"
if [[ -n "${PASS}" ]] && [[ -z "$(sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'yesbook'")" ]]; then
  sudo -u postgres psql -v ON_ERROR_STOP=1 -v pass="${PASS}" <<'SQL'
CREATE ROLE yesbook LOGIN PASSWORD :'pass';
CREATE DATABASE yesbook OWNER yesbook;
SQL
fi
echo "yesbook-db-ready"
