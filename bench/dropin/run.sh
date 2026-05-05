#!/usr/bin/env bash
# Drop-in validation harness: stand up PolyStac and stac-server (Node)
# side-by-side, each backed by its own fresh OpenSearch and seeded with
# identical fixture items via its own POST /collections/{id}/items, then
# diff every endpoint with bench/dropin/main.go.
#
# Both servers manage their own index layouts (incompatible) so the
# OpenSearch instances are not shared.

set -euo pipefail

ITEMS="${1:-50}"
COL="${2:-compat}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="$ROOT/bench/results/dropin-$RUN_TS"
mkdir -p "$OUT_DIR"

NET="polystac-dropin-${RUN_TS}"
docker network create "$NET" >/dev/null

# Fixed names so cleanup is reliable.
OS_A="dropin-os-a-$RUN_TS"   # for PolyStac
OS_B="dropin-os-b-$RUN_TS"   # for stac-server
SRV_A="dropin-polystac-$RUN_TS"
SRV_B="dropin-stacserver-$RUN_TS"

cleanup() {
  echo ">>> cleanup"
  docker rm -f "$SRV_A" "$SRV_B" "$OS_A" "$OS_B" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

start_os() {
  local name="$1"
  docker run -d --rm --name "$name" --network "$NET" \
    -e discovery.type=single-node -e plugins.security.disabled=true \
    -e OPENSEARCH_INITIAL_ADMIN_PASSWORD='ChangeMe!23' \
    -e DISABLE_INSTALL_DEMO_CONFIG=true \
    opensearchproject/opensearch:2.13.0 >/dev/null
  echo -n "    $name "
  for i in $(seq 1 120); do
    if docker exec "$name" curl -fsS http://localhost:9200/ >/dev/null 2>&1 ; then
      echo "ready ($i s)"; return 0
    fi
    echo -n "."; sleep 1
  done
  echo " TIMEOUT"; return 1
}

wait_http() {
  local label="$1" url="$2" timeout="${3:-90}"
  echo -n "    waiting for $label "
  for i in $(seq 1 "$timeout"); do
    local s
    s="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$url" 2>/dev/null || echo 000)"
    if [ "$s" != "000" ] && [ "$s" != "503" ] && [ "$s" != "502" ]; then
      echo "ready ($i s, status=$s)"; return 0
    fi
    echo -n "."; sleep 1
  done
  echo " TIMEOUT"; return 1
}

# ---- start OpenSearch instances ----------------------------------------

echo ">>> starting OpenSearch instances..."
start_os "$OS_A"
start_os "$OS_B"

# ---- build & start polystac --------------------------------------------

echo ">>> building polystac image..."
( cd "$ROOT" \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -trimpath -ldflags='-s -w' -o "$ROOT/bench/polystac" ./cmd/polystac \
  && docker build -q -f bench/Dockerfile.polystac -t "polystac:dropin-$RUN_TS" bench/ \
  && rm -f "$ROOT/bench/polystac" \
) >/dev/null
echo ">>> starting PolyStac..."
docker run -d --rm --name "$SRV_A" --network "$NET" -p 18080:8080 \
  -e POLYSTAC_BACKEND=opensearch \
  -e "POLYSTAC_ES_HOSTS=http://$OS_A:9200" \
  -e POLYSTAC_ES_VERIFY_CERTS=false \
  -e POLYSTAC_ES_REFRESH=false \
  -e POLYSTAC_LISTEN=":8080" \
  -e POLYSTAC_LOG_FORMAT=text -e POLYSTAC_LOG_LEVEL=warn \
  "polystac:dropin-$RUN_TS" >/dev/null
wait_http "polystac" "http://127.0.0.1:18080/" 60

# ---- build & start stac-server -----------------------------------------

if ! docker image inspect stac-server:dropin >/dev/null 2>&1; then
  echo ">>> building stac-server image (one-time)..."
  ( cd /tmp/stac-server && docker build -q -t stac-server:dropin . ) >/dev/null
fi
echo ">>> starting stac-server..."
docker run -d --rm --name "$SRV_B" --network "$NET" -p 13000:3000 \
  -e ENABLE_TRANSACTIONS_EXTENSION=true \
  -e "OPENSEARCH_HOST=http://$OS_B:9200" \
  -e STAC_API_URL=http://localhost:13000 \
  -e LOG_LEVEL=warn \
  -e AWS_ACCESS_KEY_ID=none -e AWS_SECRET_ACCESS_KEY=none \
  stac-server:dropin >/dev/null
wait_http "stac-server" "http://127.0.0.1:13000/" 90

# ---- seed both via HTTP -----------------------------------------------

echo ">>> building HTTP seeder..."
( cd "$ROOT" && go build -o "$OUT_DIR/bench-seed-http" ./bench/seed/http ) >/dev/null

echo ">>> seeding PolyStac with $ITEMS items..."
"$OUT_DIR/bench-seed-http" -url http://127.0.0.1:18080 -collection "$COL" -n "$ITEMS" 2>&1 | tail -1

echo ">>> seeding stac-server with $ITEMS items..."
"$OUT_DIR/bench-seed-http" -url http://127.0.0.1:13000 -collection "$COL" -n "$ITEMS" 2>&1 | tail -1

# OpenSearch has eventual consistency — give it 2s to refresh both indices.
sleep 2

# ---- run the diff ------------------------------------------------------

echo ">>> running compatibility diff..."
( cd "$ROOT" && go run ./bench/dropin \
  -a http://127.0.0.1:18080 \
  -b http://127.0.0.1:13000 \
  -collection "$COL" \
  -out "$OUT_DIR/report.md" \
  -v 2>&1 | tee "$OUT_DIR/diff.log"
) || true

echo
echo ">>> report: $OUT_DIR/report.md"
