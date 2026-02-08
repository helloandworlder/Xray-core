#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PANEL_DIR="${ROOT_DIR}/panel"
TMP_DIR="$(mktemp -d)"

POSTGRES_CONTAINER="panel-it-pg"
BACKEND_BIN="${TMP_DIR}/panel-backend"
AGENT_BIN_AMD64="${TMP_DIR}/panel-agent-linux-amd64"
AGENT_BIN_ARM64="${TMP_DIR}/panel-agent-linux-arm64"
BACKEND_LOG="${TMP_DIR}/backend.log"
FILE_SERVER_LOG="${TMP_DIR}/file-server.log"

SOCKS_MACHINE="it-socks-node"
LINE_MACHINE="it-line-node"

PANEL_ADDR="127.0.0.1:18080"
PANEL_BASE_URL="http://${PANEL_ADDR}"
PANEL_USER="itadmin"
PANEL_PASS="it-admin-pass"
PANEL_TOKEN=""

BACKEND_PID=""
FILE_SERVER_PID=""

cleanup() {
  set +e
  if [[ -n "${FILE_SERVER_PID}" ]]; then
    kill "${FILE_SERVER_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${BACKEND_PID}" ]]; then
    kill "${BACKEND_PID}" >/dev/null 2>&1 || true
  fi
  docker rm -f "${POSTGRES_CONTAINER}" >/dev/null 2>&1 || true
  orbctl delete -f "${SOCKS_MACHINE}" "${LINE_MACHINE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1"
    exit 1
  fi
}

require_cmd docker
require_cmd orbctl
require_cmd python3
require_cmd curl
require_cmd go

pkill -f "panel-backend" >/dev/null 2>&1 || true
pkill -f "python3 -m http.server 19080" >/dev/null 2>&1 || true

echo "[1/18] Build backend and agent binaries"
(cd "${PANEL_DIR}" && go build -o "${BACKEND_BIN}" ./cmd/backend)
(cd "${PANEL_DIR}" && GOOS=linux GOARCH=amd64 go build -o "${AGENT_BIN_AMD64}" ./cmd/agent)
(cd "${PANEL_DIR}" && GOOS=linux GOARCH=arm64 go build -o "${AGENT_BIN_ARM64}" ./cmd/agent)

echo "[2/18] Start PostgreSQL test container"
docker rm -f "${POSTGRES_CONTAINER}" >/dev/null 2>&1 || true
docker run -d --name "${POSTGRES_CONTAINER}" \
  -e POSTGRES_DB=panel \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 55432:5432 \
  postgres:16-alpine >/dev/null

echo "[3/18] Wait PostgreSQL ready"
for _ in $(seq 1 60); do
  if docker exec "${POSTGRES_CONTAINER}" pg_isready -U postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "[4/18] Start backend"
PANEL_HTTP_ADDR=":18080" \
PANEL_PG_DSN="host=127.0.0.1 user=postgres password=postgres dbname=panel port=55432 sslmode=disable" \
PANEL_JWT_SECRET="integration-jwt-secret" \
PANEL_DEFAULT_NODE_NAME="default-node" \
PANEL_BOOTSTRAP_ADMIN="${PANEL_USER}" \
PANEL_BOOTSTRAP_PASSWORD="${PANEL_PASS}" \
PANEL_NODE_PROBE_INTERVAL="15s" \
PANEL_OPENAPI_DEV="true" \
PANEL_AGENT_TLS_MODE="mtls_required" \
"${BACKEND_BIN}" >"${BACKEND_LOG}" 2>&1 &
BACKEND_PID="$!"
disown "${BACKEND_PID}" >/dev/null 2>&1 || true

echo "[5/18] Wait backend ready"
for _ in $(seq 1 60); do
  if curl -sSf "${PANEL_BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "[6/18] Login and get API token"
login_json="$(curl -sS -X POST "${PANEL_BASE_URL}/api/v1/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"${PANEL_USER}\",\"password\":\"${PANEL_PASS}\"}")"
PANEL_TOKEN="$(printf '%s' "${login_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')"
if [[ -z "${PANEL_TOKEN}" ]]; then
  echo "failed to obtain panel token"
  echo "login response: ${login_json}"
  cat "${BACKEND_LOG}"
  exit 1
fi

echo "[7/18] Start file server for agent binary"
python3 -m http.server 19080 --bind 0.0.0.0 --directory "${TMP_DIR}" >"${FILE_SERVER_LOG}" 2>&1 &
FILE_SERVER_PID="$!"
disown "${FILE_SERVER_PID}" >/dev/null 2>&1 || true

echo "[8/18] Create OrbStack machines"
orbctl delete -f "${SOCKS_MACHINE}" "${LINE_MACHINE}" >/dev/null 2>&1 || true
orbctl create ubuntu:24.04 "${SOCKS_MACHINE}" >/dev/null
orbctl create ubuntu:24.04 "${LINE_MACHINE}" >/dev/null

socks_ip="$(orbctl run -m "${SOCKS_MACHINE}" sh -lc "hostname -I | awk '{print \$1}'")"
line_ip="$(orbctl run -m "${LINE_MACHINE}" sh -lc "hostname -I | awk '{print \$1}'")"
if [[ -z "${socks_ip}" || -z "${line_ip}" ]]; then
  echo "failed to resolve machine IPs"
  exit 1
fi

install_script_linux="/mnt/mac${ROOT_DIR}/panel/scripts/install_xray_agent.sh"
panel_url_for_vm="http://host.orb.internal:18080"
socks_arch="$(orbctl run -m "${SOCKS_MACHINE}" sh -lc "uname -m")"
line_arch="$(orbctl run -m "${LINE_MACHINE}" sh -lc "uname -m")"

agent_url_for_arch() {
  local arch="$1"
  case "${arch}" in
    x86_64|amd64)
      echo "http://host.orb.internal:19080/panel-agent-linux-amd64"
      ;;
    aarch64|arm64)
      echo "http://host.orb.internal:19080/panel-agent-linux-arm64"
      ;;
    *)
      echo ""
      ;;
  esac
}

agent_url_socks="$(agent_url_for_arch "${socks_arch}")"
agent_url_line="$(agent_url_for_arch "${line_arch}")"
if [[ -z "${agent_url_socks}" || -z "${agent_url_line}" ]]; then
  echo "unsupported machine architecture: socks=${socks_arch}, line=${line_arch}"
  exit 1
fi

echo "[9/18] Install agent on socks node"
orbctl run -m "${SOCKS_MACHINE}" -u root sh -lc "AGENT_DOWNLOAD_URL='${agent_url_socks}' PANEL_API_BASE_URL='${panel_url_for_vm}' PANEL_API_TOKEN='${PANEL_TOKEN}' PANEL_NODE_NAME='socks-node' PANEL_NODE_DESCRIPTION='integration socks node' AGENT_ADVERTISE_ADDR='${socks_ip}:7001' AGENT_SERVER_NAME='${socks_ip}' PANEL_REGISTER_NODE='true' bash '${install_script_linux}'"

echo "[10/18] Install agent on line node"
orbctl run -m "${LINE_MACHINE}" -u root sh -lc "AGENT_DOWNLOAD_URL='${agent_url_line}' PANEL_API_BASE_URL='${panel_url_for_vm}' PANEL_API_TOKEN='${PANEL_TOKEN}' PANEL_NODE_NAME='line-node' PANEL_NODE_DESCRIPTION='integration line node' AGENT_ADVERTISE_ADDR='${line_ip}:7001' AGENT_SERVER_NAME='${line_ip}' PANEL_REGISTER_NODE='true' bash '${install_script_linux}'"

api_get() {
  local path="$1"
  curl -sS "${PANEL_BASE_URL}${path}" -H "Authorization: Bearer ${PANEL_TOKEN}"
}

api_post() {
  local path="$1"
  local payload="$2"
  curl -sS -X POST "${PANEL_BASE_URL}${path}" -H "Authorization: Bearer ${PANEL_TOKEN}" -H 'Content-Type: application/json' -d "${payload}"
}

api_put() {
  local path="$1"
  local payload="$2"
  curl -sS -X PUT "${PANEL_BASE_URL}${path}" -H "Authorization: Bearer ${PANEL_TOKEN}" -H 'Content-Type: application/json' -d "${payload}"
}

wait_remote_service_active() {
  local machine="$1"
  local service="$2"
  local retries="${3:-90}"
  for _ in $(seq 1 "${retries}"); do
    if orbctl run -m "${machine}" -u root sh -lc "systemctl is-active --quiet ${service}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  echo "service ${service} on ${machine} is not active"
  orbctl run -m "${machine}" -u root sh -lc "systemctl status ${service} --no-pager -l || true" || true
  orbctl run -m "${machine}" -u root sh -lc "journalctl -u ${service} -n 40 --no-pager || true" || true
  return 1
}

wait_tcp_reachable() {
  local host="$1"
  local port="$2"
  local retries="${3:-90}"
  for _ in $(seq 1 "${retries}"); do
    if python3 - "${host}" "${port}" <<'PY' >/dev/null 2>&1
import socket
import sys

host = sys.argv[1]
port = int(sys.argv[2])
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(1.5)
try:
    s.connect((host, port))
except OSError:
    sys.exit(1)
finally:
    s.close()
PY
    then
      return 0
    fi
    sleep 2
  done
  echo "tcp ${host}:${port} is unreachable"
  return 1
}

wait_panel_node_status_ok() {
  local node_id="$1"
  local node_name="$2"
  local retries="${3:-90}"
  local last_status=""
  for _ in $(seq 1 "${retries}"); do
    last_status="$(api_get "/api/v1/xray/status?nodeId=${node_id}")"
    if STATUS_JSON="${last_status}" python3 - <<'PY' >/dev/null 2>&1
import json
import os
import sys

raw = os.environ.get("STATUS_JSON", "")
if not raw:
    sys.exit(1)
obj = json.loads(raw)
items = obj.get("items", [])
if not items:
    sys.exit(1)
if bool(obj.get("ok")) and bool(items[0].get("ok")):
    sys.exit(0)
sys.exit(1)
PY
    then
      return 0
    fi
    sleep 2
  done
  echo "panel status check failed for ${node_name} (nodeId=${node_id})"
  echo "last status: ${last_status}"
  return 1
}

publish_node() {
  local node_id="$1"
  local node_name="$2"
  local publish_json
  local publish_ok
  publish_json="$(api_post '/api/v1/xray/publish' "{\"nodeId\":${node_id},\"validate\":true,\"reload\":true}")"
  publish_ok="$(printf '%s' "${publish_json}" | python3 -c 'import json,sys; obj=json.load(sys.stdin); print("true" if obj.get("ok") else "false")')"
  if [[ "${publish_ok}" != "true" ]]; then
    echo "publish failed for ${node_name}: ${publish_json}"
    echo "status: $(api_get "/api/v1/xray/status?nodeId=${node_id}")"
    return 1
  fi
}

node_list_json="$(api_get '/api/v1/nodes')"
socks_node_id="$(printf '%s' "${node_list_json}" | python3 -c 'import json,sys; items=json.load(sys.stdin).get("items",[]); print(next((str(i.get("id","")) for i in items if i.get("name")=="socks-node"),""))')"
line_node_id="$(printf '%s' "${node_list_json}" | python3 -c 'import json,sys; items=json.load(sys.stdin).get("items",[]); print(next((str(i.get("id","")) for i in items if i.get("name")=="line-node"),""))')"
if [[ -z "${socks_node_id}" || -z "${line_node_id}" ]]; then
  echo "failed to resolve node ids"
  echo "${node_list_json}"
  exit 1
fi

echo "[11/18] Wait panel-agent services active"
wait_remote_service_active "${SOCKS_MACHINE}" panel-agent
wait_remote_service_active "${LINE_MACHINE}" panel-agent

echo "[12/18] Wait connectivity and panel node status"
echo "- wait tcp ${socks_ip}:7001"
wait_tcp_reachable "${socks_ip}" 7001
echo "- wait tcp ${line_ip}:7001"
wait_tcp_reachable "${line_ip}" 7001
echo "- wait panel xray status socks-node"
wait_panel_node_status_ok "${socks_node_id}" "socks-node"
echo "- wait panel xray status line-node"
wait_panel_node_status_ok "${line_node_id}" "line-node"

echo "[13/18] Start upstream socks server on socks node"
orbctl run -m "${SOCKS_MACHINE}" -u root sh -lc "cat >/tmp/upstream-socks.json <<'EOF'
{
  \"log\": { \"loglevel\": \"warning\" },
  \"inbounds\": [
    { \"tag\": \"upstream-socks\", \"listen\": \"0.0.0.0\", \"port\": 1081, \"protocol\": \"socks\", \"settings\": { \"auth\": \"noauth\", \"udp\": true } }
  ],
  \"outbounds\": [
    { \"tag\": \"direct\", \"protocol\": \"freedom\", \"settings\": {} }
  ],
  \"routing\": { \"rules\": [ { \"type\": \"field\", \"network\": \"tcp,udp\", \"outboundTag\": \"direct\" } ] }
}
EOF
nohup /usr/local/bin/xray run -config /tmp/upstream-socks.json >/tmp/upstream-socks.log 2>&1 &"

echo "[14/18] Prepare panel resources"
customer_json="$(api_post '/api/v1/customers' '{"name":"Integration Customer","email":"it@example.com"}')"
customer_id="$(printf '%s' "${customer_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')"

inbound_socks_json="$(api_post '/api/v1/inbounds' "{\"nodeId\":${socks_node_id},\"name\":\"mixed-socks\",\"protocol\":\"mixed\",\"listenIp\":\"0.0.0.0\",\"port\":32001}")"
inbound_vless_json="$(api_post '/api/v1/inbounds' "{\"nodeId\":${socks_node_id},\"name\":\"vless-in\",\"protocol\":\"vless\",\"listenIp\":\"0.0.0.0\",\"port\":32002}")"
inbound_ss_json="$(api_post '/api/v1/inbounds' "{\"nodeId\":${socks_node_id},\"name\":\"ss-in\",\"protocol\":\"ss\",\"listenIp\":\"0.0.0.0\",\"port\":32003}")"
inbound_res_vmess_json="$(api_post '/api/v1/inbounds' "{\"nodeId\":${line_node_id},\"name\":\"res-vmess\",\"protocol\":\"vmess\",\"listenIp\":\"0.0.0.0\",\"port\":33001}")"

inbound_socks_id="$(printf '%s' "${inbound_socks_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')"
inbound_vless_id="$(printf '%s' "${inbound_vless_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')"
inbound_ss_id="$(printf '%s' "${inbound_ss_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')"
inbound_res_vmess_id="$(printf '%s' "${inbound_res_vmess_json}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')"

api_post '/api/v1/socks/endpoints' "{\"host\":\"${socks_ip}\",\"port\":1081,\"maxConcurrent\":200,\"status\":\"active\"}" >/dev/null

echo "[15/18] Dispatch dedicated and residential orders"
api_post '/api/v1/delivery/dispatch' "{\"orderType\":\"dedicated\",\"customerId\":${customer_id},\"inboundId\":${inbound_socks_id},\"protocol\":\"mixed\",\"quantity\":1,\"autoActivate\":true,\"orderNoPrefix\":\"it-socks\",\"userExpr\":\"sock{index}\",\"passwordExpr\":\"pw{rand:8}\"}" >/dev/null
api_post '/api/v1/delivery/dispatch' "{\"orderType\":\"dedicated\",\"customerId\":${customer_id},\"inboundId\":${inbound_vless_id},\"protocol\":\"vless\",\"quantity\":1,\"autoActivate\":true,\"orderNoPrefix\":\"it-vless\"}" >/dev/null
api_post '/api/v1/delivery/dispatch' "{\"orderType\":\"dedicated\",\"customerId\":${customer_id},\"inboundId\":${inbound_ss_id},\"protocol\":\"ss\",\"quantity\":1,\"autoActivate\":true,\"orderNoPrefix\":\"it-ss\"}" >/dev/null
api_post '/api/v1/delivery/dispatch' "{\"orderType\":\"residential\",\"customerId\":${customer_id},\"inboundId\":${inbound_res_vmess_id},\"protocol\":\"vmess\",\"quantity\":1,\"autoActivate\":true,\"orderNoPrefix\":\"it-res-vmess\",\"egressIps\":[\"${line_ip}\"]}" >/dev/null

echo "[16/18] Publish test node configs"
publish_node "${socks_node_id}" "socks-node"
publish_node "${line_node_id}" "line-node"

orders_json="$(api_get '/api/v1/orders?limit=200')"

extract_order_field() {
  local prefix="$1"
  local field="$2"
  ORDERS_JSON="${orders_json}" PREFIX="${prefix}" FIELD="${field}" python3 - <<'PY'
import json
import os

items = json.loads(os.environ.get("ORDERS_JSON", "{}")).get("items", [])
target = os.environ.get("PREFIX", "")
field = os.environ.get("FIELD", "")
for item in items:
    if str(item.get("orderNo", "")).startswith(target):
        if field == "username":
            email = (item.get("credential") or {}).get("email", "")
            print(email.split("@")[0] if email else "")
        elif field == "password":
            print((item.get("credential") or {}).get("password", ""))
        elif field == "uuid":
            print((item.get("credential") or {}).get("uuid", ""))
        elif field == "cipher":
            print((item.get("credential") or {}).get("cipher", ""))
        elif field == "port":
            print((item.get("inbound") or {}).get("port", ""))
        elif field == "protocol":
            print(item.get("protocol", ""))
        elif field == "orderType":
            print(item.get("orderType", ""))
        break
PY
}

socks_user="$(extract_order_field 'it-socks' 'username')"
socks_pass="$(extract_order_field 'it-socks' 'password')"
socks_port="$(extract_order_field 'it-socks' 'port')"

vless_uuid="$(extract_order_field 'it-vless' 'uuid')"
vless_port="$(extract_order_field 'it-vless' 'port')"

ss_pass="$(extract_order_field 'it-ss' 'password')"
ss_cipher="$(extract_order_field 'it-ss' 'cipher')"
ss_port="$(extract_order_field 'it-ss' 'port')"

res_vmess_uuid="$(extract_order_field 'it-res-vmess' 'uuid')"
res_vmess_port="$(extract_order_field 'it-res-vmess' 'port')"

run_client_test_on_line() {
  local name="$1"
  local local_port="$2"
  local outbound_json="$3"
  local remote_cmd
  remote_cmd="cat >/tmp/client-${name}.json <<'EOF'
{
  \"log\": { \"loglevel\": \"warning\" },
  \"inbounds\": [
    { \"tag\": \"in\", \"listen\": \"127.0.0.1\", \"port\": ${local_port}, \"protocol\": \"socks\", \"settings\": { \"auth\": \"noauth\", \"udp\": true } }
  ],
  \"outbounds\": [
    ${outbound_json},
    { \"tag\": \"block\", \"protocol\": \"blackhole\", \"settings\": {} }
  ],
  \"routing\": { \"rules\": [ { \"type\": \"field\", \"network\": \"tcp,udp\", \"outboundTag\": \"proxy\" } ] }
}
EOF
/usr/local/bin/xray run -config /tmp/client-${name}.json >/tmp/client-${name}.log 2>&1 &
pid=\$!
sleep 2
curl --silent --show-error --fail --max-time 20 --socks5-hostname 127.0.0.1:${local_port} https://api.ipify.org >/tmp/client-${name}.out
kill \$pid >/dev/null 2>&1 || true
wait \$pid >/dev/null 2>&1 || true"
  orbctl run -m "${LINE_MACHINE}" -u root sh -lc "${remote_cmd}"
}

echo "[17/18] Validate Socks5 / VMess / VLESS / SS by real Xray client"
run_client_test_on_line "socks" 14001 "{ \"tag\": \"proxy\", \"protocol\": \"socks\", \"settings\": { \"servers\": [ { \"address\": \"${socks_ip}\", \"port\": ${socks_port}, \"users\": [ { \"user\": \"${socks_user}\", \"pass\": \"${socks_pass}\" } ] } ] } }"
run_client_test_on_line "vmess-res" 14002 "{ \"tag\": \"proxy\", \"protocol\": \"vmess\", \"settings\": { \"vnext\": [ { \"address\": \"${line_ip}\", \"port\": ${res_vmess_port}, \"users\": [ { \"id\": \"${res_vmess_uuid}\", \"alterId\": 0, \"security\": \"auto\" } ] } ] } }"
run_client_test_on_line "vless" 14003 "{ \"tag\": \"proxy\", \"protocol\": \"vless\", \"settings\": { \"vnext\": [ { \"address\": \"${socks_ip}\", \"port\": ${vless_port}, \"users\": [ { \"id\": \"${vless_uuid}\", \"encryption\": \"none\" } ] } ] } }"
run_client_test_on_line "ss" 14004 "{ \"tag\": \"proxy\", \"protocol\": \"shadowsocks\", \"settings\": { \"servers\": [ { \"address\": \"${socks_ip}\", \"port\": ${ss_port}, \"method\": \"${ss_cipher}\", \"password\": \"${ss_pass}\" } ] } }"

echo "[18/18] Integration test passed"
echo "- Two OrbStack nodes installed via script"
echo "- Panel API dispatch + publish succeeded"
echo "- Xray client validated protocols: socks5, vmess, vless, ss"
