#!/usr/bin/env bash
set -euo pipefail

# One-click installer for Xray-core + panel-agent (systemd + mTLS + optional auto register)
#
# Required:
#   AGENT_DOWNLOAD_URL="https://example.com/panel-agent-linux-amd64"
#
# Optional auto registration:
#   PANEL_API_BASE_URL="http://panel.local"
#   PANEL_API_TOKEN="<jwt token from panel login>"
#   PANEL_NODE_NAME="node-1"
#   AGENT_ADVERTISE_ADDR="10.0.0.8:7001"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Please run as root (use sudo)."
  exit 1
fi

XRAY_VERSION="${XRAY_VERSION:-latest}"
AGENT_DOWNLOAD_URL="${AGENT_DOWNLOAD_URL:-}"
AGENT_INSTALL_PATH="${AGENT_INSTALL_PATH:-/usr/local/bin/panel-agent}"
AGENT_USER="${AGENT_USER:-panel-agent}"
AGENT_GROUP="${AGENT_GROUP:-panel-agent}"
AGENT_PORT="${AGENT_PORT:-7001}"
XRAY_CONFIG_PATH="${XRAY_CONFIG_PATH:-/usr/local/etc/xray/config.json}"
XRAY_BIN="${XRAY_BIN:-/usr/local/bin/xray}"
CERT_DIR="${CERT_DIR:-/etc/panel-agent/certs}"

AGENT_TLS_MODE="${AGENT_TLS_MODE:-mtls_required}"
AGENT_SERVER_NAME="${AGENT_SERVER_NAME:-$(hostname -f 2>/dev/null || hostname)}"

PANEL_API_BASE_URL="${PANEL_API_BASE_URL:-}"
PANEL_API_TOKEN="${PANEL_API_TOKEN:-}"
PANEL_NODE_NAME="${PANEL_NODE_NAME:-$(hostname)}"
PANEL_NODE_DESCRIPTION="${PANEL_NODE_DESCRIPTION:-installed by install_xray_agent.sh}"
PANEL_REGISTER_NODE="${PANEL_REGISTER_NODE:-true}"
AGENT_ADVERTISE_ADDR="${AGENT_ADVERTISE_ADDR:-}"

if [[ -z "${AGENT_DOWNLOAD_URL}" ]]; then
  echo "AGENT_DOWNLOAD_URL is required."
  exit 1
fi

if [[ -z "${AGENT_ADVERTISE_ADDR}" ]]; then
  if command -v hostname >/dev/null 2>&1; then
    AGENT_ADVERTISE_ADDR="$(hostname -I 2>/dev/null | awk '{print $1}'):${AGENT_PORT}"
  fi
fi
if [[ -z "${AGENT_ADVERTISE_ADDR}" || "${AGENT_ADVERTISE_ADDR}" == ":${AGENT_PORT}" ]]; then
  AGENT_ADVERTISE_ADDR="127.0.0.1:${AGENT_PORT}"
fi

echo "[1/8] Install dependencies"
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y curl ca-certificates tar openssl python3
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y curl ca-certificates tar openssl python3
elif command -v yum >/dev/null 2>&1; then
  yum install -y curl ca-certificates tar openssl python3
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache curl ca-certificates tar openssl python3
else
  echo "Unsupported package manager. Please install curl/openssl/python3 manually."
  exit 1
fi

echo "[2/8] Install Xray-core"
if [[ "${XRAY_VERSION}" == "latest" ]]; then
  bash <(curl -Ls https://github.com/XTLS/Xray-install/raw/main/install-release.sh) install
else
  bash <(curl -Ls https://github.com/XTLS/Xray-install/raw/main/install-release.sh) install --version "${XRAY_VERSION}"
fi

if [[ ! -x "${XRAY_BIN}" ]]; then
  echo "Expected Xray binary not found at ${XRAY_BIN}"
  exit 1
fi

echo "[3/8] Create runtime user and folders"
if ! getent group "${AGENT_GROUP}" >/dev/null 2>&1; then
  groupadd --system "${AGENT_GROUP}"
fi
if ! id -u "${AGENT_USER}" >/dev/null 2>&1; then
  useradd --system --no-create-home --gid "${AGENT_GROUP}" --shell /usr/sbin/nologin "${AGENT_USER}"
fi

mkdir -p /etc/panel-agent
mkdir -p /var/lib/panel-agent
mkdir -p "${CERT_DIR}"
chown -R "${AGENT_USER}:${AGENT_GROUP}" /etc/panel-agent /var/lib/panel-agent "${CERT_DIR}"

mkdir -p "$(dirname "${XRAY_CONFIG_PATH}")"
if [[ ! -f "${XRAY_CONFIG_PATH}" ]]; then
  printf '{}\n' > "${XRAY_CONFIG_PATH}"
fi
chown "${AGENT_USER}:${AGENT_GROUP}" "${XRAY_CONFIG_PATH}"
chmod 644 "${XRAY_CONFIG_PATH}"

echo "[4/8] Install panel-agent binary"
curl -fL "${AGENT_DOWNLOAD_URL}" -o "${AGENT_INSTALL_PATH}"
chmod +x "${AGENT_INSTALL_PATH}"
chown "${AGENT_USER}:${AGENT_GROUP}" "${AGENT_INSTALL_PATH}"

echo "[5/8] Generate mTLS certs"
CA_CERT="${CERT_DIR}/ca.crt"
CA_KEY="${CERT_DIR}/ca.key"
SERVER_CERT="${CERT_DIR}/server.crt"
SERVER_KEY="${CERT_DIR}/server.key"
CLIENT_CERT="${CERT_DIR}/client.crt"
CLIENT_KEY="${CERT_DIR}/client.key"

server_san="$(python3 - <<PY
import ipaddress
name = "${AGENT_SERVER_NAME}".strip()
try:
    ipaddress.ip_address(name)
    print(f"IP:{name}")
except ValueError:
    print(f"DNS:{name}")
PY
)"
SERVER_EXT="${CERT_DIR}/server.ext"
cat > "${SERVER_EXT}" <<EOF
[v3_req]
subjectAltName=${server_san}
extendedKeyUsage=serverAuth
keyUsage=digitalSignature,keyEncipherment
basicConstraints=CA:FALSE
EOF

if [[ ! -f "${CA_CERT}" || ! -f "${CA_KEY}" ]]; then
  openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
    -keyout "${CA_KEY}" -out "${CA_CERT}" \
    -subj "/CN=panel-agent-ca"
fi

if [[ ! -f "${SERVER_CERT}" || ! -f "${SERVER_KEY}" ]]; then
  openssl req -newkey rsa:2048 -nodes \
    -keyout "${SERVER_KEY}" -out "${CERT_DIR}/server.csr" \
    -subj "/CN=${AGENT_SERVER_NAME}" \
    -addext "subjectAltName=DNS:${AGENT_SERVER_NAME}"
  openssl x509 -req -sha256 -days 1825 \
    -in "${CERT_DIR}/server.csr" \
    -CA "${CA_CERT}" -CAkey "${CA_KEY}" -CAcreateserial \
    -out "${SERVER_CERT}" \
    -extfile "${SERVER_EXT}" -extensions v3_req
fi

if [[ ! -f "${CLIENT_CERT}" || ! -f "${CLIENT_KEY}" ]]; then
  openssl req -newkey rsa:2048 -nodes \
    -keyout "${CLIENT_KEY}" -out "${CERT_DIR}/client.csr" \
    -subj "/CN=panel-backend-client"
  openssl x509 -req -sha256 -days 1825 \
    -in "${CERT_DIR}/client.csr" \
    -CA "${CA_CERT}" -CAkey "${CA_KEY}" -CAcreateserial \
    -out "${CLIENT_CERT}"
fi

chmod 600 "${CA_KEY}" "${SERVER_KEY}" "${CLIENT_KEY}"
chmod 644 "${CA_CERT}" "${SERVER_CERT}" "${CLIENT_CERT}"
chown -R "${AGENT_USER}:${AGENT_GROUP}" "${CERT_DIR}"

echo "[6/8] Write env and systemd unit"
cat > /etc/panel-agent/panel-agent.env <<EOF
AGENT_GRPC_ADDR=:${AGENT_PORT}
XRAY_CONFIG_PATH=${XRAY_CONFIG_PATH}
XRAY_BIN=${XRAY_BIN}
XRAY_RELOAD_CMD=/bin/systemctl restart xray
AGENT_TLS_MODE=${AGENT_TLS_MODE}
AGENT_TLS_CA_CERT_FILE=${CA_CERT}
AGENT_TLS_SERVER_CERT_FILE=${SERVER_CERT}
AGENT_TLS_SERVER_KEY_FILE=${SERVER_KEY}
EOF

cat > /etc/systemd/system/panel-agent.service <<'EOF'
[Unit]
Description=Panel Agent for Xray-core
After=network-online.target xray.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/panel-agent/panel-agent.env
ExecStart=/usr/local/bin/panel-agent
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

echo "[7/8] Enable and start services"
systemctl daemon-reload
systemctl enable xray >/dev/null 2>&1 || true
systemctl restart xray
systemctl enable panel-agent
systemctl restart panel-agent

echo "[8/8] Register node to panel (optional)"
register_enabled="$(echo "${PANEL_REGISTER_NODE}" | tr '[:upper:]' '[:lower:]')"
if [[ "${register_enabled}" == "true" ]]; then
  if [[ -z "${PANEL_API_BASE_URL}" || -z "${PANEL_API_TOKEN}" ]]; then
    echo "Skip register: PANEL_API_BASE_URL or PANEL_API_TOKEN missing"
  else
    set +e
    node_create_payload="$(python3 - <<PY
import json
print(json.dumps({
  "name": "${PANEL_NODE_NAME}",
  "agentAddr": "${AGENT_ADVERTISE_ADDR}",
  "enabled": True,
  "description": "${PANEL_NODE_DESCRIPTION}",
}))
PY
)"
    create_resp="$(curl -sS -X POST "${PANEL_API_BASE_URL%/}/api/v1/nodes" \
      -H "Authorization: Bearer ${PANEL_API_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "${node_create_payload}")"

    node_id="$(python3 - <<PY
import json
resp = '''${create_resp}'''
try:
    obj = json.loads(resp)
    print(obj.get("id", ""))
except Exception:
    print("")
PY
)"

    if [[ -z "${node_id}" ]]; then
      list_resp="$(curl -sS -X GET "${PANEL_API_BASE_URL%/}/api/v1/nodes" -H "Authorization: Bearer ${PANEL_API_TOKEN}")"
      node_id="$(python3 - <<PY
import json
target = "${PANEL_NODE_NAME}"
resp = '''${list_resp}'''
try:
    obj = json.loads(resp)
    for item in obj.get("items", []):
        if item.get("name") == target:
            print(item.get("id", ""))
            break
    else:
        print("")
except Exception:
    print("")
PY
)"
    fi

    if [[ -n "${node_id}" ]]; then
      cert_payload="$(python3 - <<PY
import json
def r(path):
    with open(path, 'r', encoding='utf-8') as f:
        return f.read()
print(json.dumps({
    "mtlsEnabled": True,
    "serverName": "${AGENT_SERVER_NAME}",
    "caCertPem": r("${CA_CERT}"),
    "clientCertPem": r("${CLIENT_CERT}"),
    "clientKeyPem": r("${CLIENT_KEY}"),
}))
PY
)"
      curl -sS -X PUT "${PANEL_API_BASE_URL%/}/api/v1/nodes/${node_id}/certs" \
        -H "Authorization: Bearer ${PANEL_API_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "${cert_payload}" >/dev/null
      echo "Node registered/updated, nodeId=${node_id}"
    else
      echo "Node register failed: unable to resolve node id"
    fi
    set -e
  fi
else
  echo "Skip register: PANEL_REGISTER_NODE=${PANEL_REGISTER_NODE}"
fi

echo
echo "Install complete."
echo "- Xray service:      systemctl status xray"
echo "- Panel agent:       systemctl status panel-agent"
echo "- Agent gRPC listen: :${AGENT_PORT}"
echo "- Cert dir:          ${CERT_DIR}"
echo "- Advertise addr:    ${AGENT_ADVERTISE_ADDR}"
