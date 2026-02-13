# Xray User Rate Limit Docker Test

This harness spins up a local VMess client/server path using this repository's Xray build,
applies a user rate limit over gRPC, and measures observed throughput before and after.

## What it starts

- `xray-server`: VMess inbound + gRPC API (`UserRateLimitService`)
- `xray-client`: Socks inbound, VMess outbound to server
- `traffic`: local HTTP service used for download/upload throughput tests

## Run

```bash
chmod +x testing/rate-limit/run_test.sh
testing/rate-limit/run_test.sh
```

To keep containers running after the script ends:

```bash
KEEP_UP=1 testing/rate-limit/run_test.sh
```

Then manually stop:

```bash
docker compose --project-name xray-rate-test -f testing/rate-limit/docker-compose.yml down -v
```
