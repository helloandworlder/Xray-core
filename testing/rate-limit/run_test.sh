#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

compose() {
  docker compose --project-name xray-rate-test -f "$SCRIPT_DIR/docker-compose.yml" "$@"
}

to_mbps() {
  python3 - "$1" <<'PY'
import sys
v = float(sys.argv[1])
print(f"{v * 8 / 1_000_000:.2f}")
PY
}

measure_down() {
  compose exec -T xray-client sh -lc "curl -sS --max-time 180 -x socks5h://127.0.0.1:10808 'http://traffic:8080/download?bytes=8388608' -o /dev/null -w '%{speed_download}'"
}

measure_up() {
  compose exec -T xray-client sh -lc "head -c 8388608 /dev/zero | curl -sS --max-time 180 -x socks5h://127.0.0.1:10808 -X POST --data-binary @- 'http://traffic:8080/upload' -o /dev/null -w '%{speed_upload}'"
}

wait_ready() {
  local i
  for i in $(seq 1 40); do
    if compose exec -T xray-client sh -lc "curl -sS --max-time 2 -x socks5h://127.0.0.1:10808 'http://traffic:8080/ping' >/dev/null"; then
      return 0
    fi
    sleep 1
  done
  echo "xray test stack did not become ready in time" >&2
  return 1
}

cleanup() {
  compose down -v >/dev/null 2>&1 || true
}

if [ "${KEEP_UP:-0}" != "1" ]; then
  trap cleanup EXIT
fi

echo "[1/5] Building and starting containers..."
compose up -d --build xray-server xray-client traffic >/dev/null

echo "[2/5] Waiting for proxy path to become ready..."
wait_ready

echo "[3/5] Measuring baseline throughput (no user limit)..."
BASE_DOWN_BPS="$(measure_down)"
BASE_UP_BPS="$(measure_up)"

echo "[4/5] Applying user rate limit 1,000,000/1,000,000 via gRPC..."
compose run --rm --no-deps grpcurl \
  -plaintext \
  -import-path /protos/userratelimit \
  -proto command.proto \
  -d '{"email":"user@test.local","uplink_bps":1000000,"downlink_bps":1000000}' \
  xray-server:10085 \
  xray.app.userratelimit.command.UserRateLimitService/SetUserRateLimit >/dev/null

LIMIT_STATE="$(compose run --rm --no-deps grpcurl \
  -plaintext \
  -import-path /protos/userratelimit \
  -proto command.proto \
  -d '{"email":"user@test.local"}' \
  xray-server:10085 \
  xray.app.userratelimit.command.UserRateLimitService/GetUserRateLimit)"

echo "[5/5] Measuring throughput after limit..."
LIMIT_DOWN_BPS="$(measure_down)"
LIMIT_UP_BPS="$(measure_up)"

echo
echo "Applied limit response:"
echo "$LIMIT_STATE"
echo
echo "Measured throughput (curl average):"
echo "- baseline downlink: ${BASE_DOWN_BPS} B/s ($(to_mbps "$BASE_DOWN_BPS") Mbps)"
echo "- baseline uplink:   ${BASE_UP_BPS} B/s ($(to_mbps "$BASE_UP_BPS") Mbps)"
echo "- limited downlink:  ${LIMIT_DOWN_BPS} B/s ($(to_mbps "$LIMIT_DOWN_BPS") Mbps)"
echo "- limited uplink:    ${LIMIT_UP_BPS} B/s ($(to_mbps "$LIMIT_UP_BPS") Mbps)"
echo
echo "Note: this implementation currently throttles by bytes/second internally."
