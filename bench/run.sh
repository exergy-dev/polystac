#!/usr/bin/env bash
# Head-to-head performance benchmark: PolyStac vs the reference
# implementations it aims to replace, on identical data.
#
# Design notes:
#
#   * pgstac is shared across the polystac and stac-fastapi-pgstac
#     runs. Both impls call the same `pgstac.create_items` and
#     `pgstac.search` SQL functions, so a shared pgstac is genuinely
#     apples-to-apples.
#   * OpenSearch is NOT shared. Each impl manages its own index layout
#     (polystac uses `items_<col>`; stac-fastapi-os uses
#     `items_<col>-000001` aliases) and the templates collide. So we
#     spin a fresh OpenSearch per OS impl and seed it via HTTP through
#     the impl's own server (each one upserts the same N items via
#     its native write path, producing logically-equivalent fixtures).
#
# Per-impl artifacts: cold-start, idle/peak RSS, image size, k6
# latency p50/p95/p99 per scenario, throughput, error rate. Rolled up
# into bench/results/<ts>/report.md.
#
# Usage:  ./bench/run.sh [items=1000] [duration=30s] [vus=20]
# Requires: docker, k6, Go 1.23+.

set -euo pipefail

ITEMS="${1:-1000}"
DURATION="${2:-30s}"
VUS="${3:-20}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="$ROOT/bench/results/$RUN_TS"
mkdir -p "$OUT_DIR"

echo ">>> output dir: $OUT_DIR"

NET="polystac-bench-${RUN_TS}"
docker network create "$NET" >/dev/null

PGSTAC="polystac-bench-pgstac-$RUN_TS"
IMPL="polystac-bench-impl-$RUN_TS"
OS_BOX="polystac-bench-os-$RUN_TS"

cleanup() {
  echo ">>> tearing down all containers..."
  docker rm -f "$PGSTAC" "$OS_BOX" "$IMPL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---- shared pgstac -----------------------------------------------------

echo ">>> starting shared pgstac..."
docker run -d --rm --name "$PGSTAC" --network "$NET" \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgis \
  -p 25432:5432 \
  ghcr.io/stac-utils/pgstac:v0.8.5 >/dev/null

wait_for() {
  local name="$1" probe="$2" timeout="${3:-90}"
  echo -n "    waiting for $name "
  for i in $(seq 1 "$timeout"); do
    if eval "$probe" >/dev/null 2>&1; then echo "ready ($i s)"; return 0; fi
    echo -n "."; sleep 1
  done
  echo " TIMEOUT"; return 1
}

wait_for "pgstac" "PGPASSWORD=postgres docker exec '$PGSTAC' psql -U postgres -d postgis -c 'SELECT pgstac.get_version();'" 120

# ---- build seeders + polystac image ------------------------------------

echo ">>> building seeders + polystac binary + image..."
( cd "$ROOT" \
  && go build -o "$OUT_DIR/bench-seed" ./bench/seed \
  && go build -o "$OUT_DIR/bench-seed-http" ./bench/seed/http \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -trimpath -ldflags='-s -w' -o "$ROOT/bench/polystac" ./cmd/polystac \
  && docker build -q -f bench/Dockerfile.polystac -t "polystac:bench-$RUN_TS" bench/ \
  && rm -f "$ROOT/bench/polystac" \
) >/dev/null

echo ">>> seeding shared pgstac with $ITEMS items..."
"$OUT_DIR/bench-seed" -backend pgstac -n "$ITEMS" \
  -dsn "postgresql://postgres:postgres@127.0.0.1:25432/postgis?sslmode=disable" 2>&1 | tail -1

# ---- run_impl helper ---------------------------------------------------
#
# Args: label image port cport [env-args...] [--cmd "..."] [--seed-http]
#
# `--seed-http` seeds the impl via its own HTTP API after it comes up.
# Without it, the harness assumes data is already present.
run_impl() {
  local label="$1" image="$2" port="$3" cport="${4:-8080}"; shift 4
  local env_args=()
  local cmd_override=()
  local seed_http=0
  while (( $# > 0 )); do
    case "$1" in
      --cmd)        shift; read -r -a cmd_override <<<"$1"; shift ;;
      --seed-http)  seed_http=1; shift ;;
      *)            env_args+=("$1"); shift ;;
    esac
  done
  local out="$OUT_DIR/$label"
  mkdir -p "$out"

  echo
  echo ">>> $label  ($image)"

  local start_ms ready_ms cold_ms="N/A"
  start_ms="$(date +%s%3N)"
  docker run -d --rm --name "$IMPL" --network "$NET" -p "$port:$cport" \
    "${env_args[@]}" "$image" "${cmd_override[@]}" >/dev/null
  for i in $(seq 1 240); do
    local s
    s="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$port/" 2>/dev/null || echo 000)"
    if [ "$s" != "000" ]; then
      ready_ms="$(date +%s%3N)"
      cold_ms=$((ready_ms - start_ms))
      break
    fi
    sleep 0.25
  done
  echo "    cold-start: ${cold_ms} ms"

  local img_bytes
  img_bytes="$(docker image inspect "$image" --format '{{.Size}}')"

  if [ "$seed_http" = "1" ]; then
    echo "    seeding $label with $ITEMS items via HTTP..."
    if ! "$OUT_DIR/bench-seed-http" -url "http://127.0.0.1:$port" -n "$ITEMS" 2>&1 | tail -1 ; then
      echo "    seed failed; skipping" >&2
      printf '%s,%s,SEED_FAIL,—,—,%s\n' "$label" "$image" "$img_bytes" >>"$OUT_DIR/raw.csv"
      docker rm -f "$IMPL" >/dev/null 2>&1 || true
      return 0
    fi
  fi

  sleep 5
  if ! docker ps -q -f "name=$IMPL" | grep -q .; then
    echo "    !!! container died during settle; logs follow:" >&2
    docker logs "$IMPL" 2>&1 | tail -40 | tee "$out/container-died.log" | sed 's/^/      /' >&2 || true
    printf '%s,%s,DIED,—,—,%s\n' "$label" "$image" "$img_bytes" >>"$OUT_DIR/raw.csv"
    return 0
  fi
  local idle_rss
  idle_rss="$(docker stats --no-stream --format '{{.MemUsage}}' "$IMPL" | awk '{print $1}')"

  echo "    running k6 (${VUS} VUs, ${DURATION})..."
  k6 run --quiet \
    --summary-export "$out/k6-summary.json" \
    -e "URL=http://127.0.0.1:$port" -e "VUS=$VUS" -e "DURATION=$DURATION" \
    "$ROOT/bench/k6/search.js" >"$out/k6.log" 2>&1 &
  local k6pid=$!

  local peak_kb=0
  while kill -0 "$k6pid" 2>/dev/null; do
    local rss
    rss="$(docker stats --no-stream --format '{{.MemUsage}}' "$IMPL" 2>/dev/null | awk '{print $1}')"
    local kb
    kb="$(printf '%s' "$rss" | python3 -c '
import sys, re
s = sys.stdin.read().strip()
m = re.match(r"([0-9.]+)\s*([KMG]i?B)", s)
if not m: print(0); sys.exit()
v=float(m.group(1)); u=m.group(2)
mult={"KiB":1,"KB":1,"MiB":1024,"MB":1000,"GiB":1024*1024,"GB":1000*1000}.get(u,1)
print(int(v*mult))
' 2>/dev/null)"
    [ "${kb:-0}" -gt "$peak_kb" ] && peak_kb="$kb"
    sleep 1
  done
  wait "$k6pid" || true
  local peak_rss
  peak_rss="$(awk -v k="$peak_kb" 'BEGIN{ if (k<1024) printf "%d KiB", k; else if (k<1048576) printf "%.1f MiB", k/1024; else printf "%.2f GiB", k/1048576 }')"

  printf '%s,%s,%s,%s,%s,%s\n' \
    "$label" "$image" "$cold_ms" "$idle_rss" "$peak_rss" "$img_bytes" \
    >>"$OUT_DIR/raw.csv"

  docker rm -f "$IMPL" >/dev/null 2>&1 || true
}

start_os() {
  docker run -d --rm --name "$OS_BOX" --network "$NET" \
    -e discovery.type=single-node -e plugins.security.disabled=true \
    -e OPENSEARCH_INITIAL_ADMIN_PASSWORD='ChangeMe!23' \
    -e DISABLE_INSTALL_DEMO_CONFIG=true \
    opensearchproject/opensearch:2.13.0 >/dev/null
  wait_for "opensearch" "docker exec '$OS_BOX' curl -fsS http://localhost:9200/" 120
}

stop_os() { docker rm -f "$OS_BOX" >/dev/null 2>&1 || true; }

echo "label,image,cold_ms,idle_rss,peak_rss,image_size_bytes" >"$OUT_DIR/raw.csv"

# ---- pgstac trio (shared backend) --------------------------------------

run_impl "polystac-pgstac" "polystac:bench-$RUN_TS" 18080 8080 \
  -e POLYSTAC_BACKEND=pgstac \
  -e "POLYSTAC_PG_DSN=postgresql://postgres:postgres@$PGSTAC:5432/postgis?sslmode=disable" \
  -e POLYSTAC_LISTEN=":8080" -e POLYSTAC_LOG_FORMAT=text -e POLYSTAC_LOG_LEVEL=warn

run_impl "fastapi-pgstac" "ghcr.io/stac-utils/stac-fastapi-pgstac:latest" 18081 8080 \
  -e "POSTGRES_HOST_READER=$PGSTAC" -e "POSTGRES_HOST_WRITER=$PGSTAC" \
  -e POSTGRES_PORT=5432 -e POSTGRES_USER=postgres -e POSTGRES_PASS=postgres \
  -e POSTGRES_DBNAME=postgis -e DB_MIN_CONN_SIZE=2 -e DB_MAX_CONN_SIZE=20

# ---- OS pair (private OS cluster per impl, HTTP-seeded) ----------------

echo
echo ">>> starting fresh OpenSearch for polystac-os..."
start_os
run_impl "polystac-os" "polystac:bench-$RUN_TS" 18082 8080 \
  -e POLYSTAC_BACKEND=opensearch \
  -e "POLYSTAC_ES_HOSTS=http://$OS_BOX:9200" \
  -e POLYSTAC_ES_VERIFY_CERTS=false \
  -e POLYSTAC_ES_REFRESH=false \
  -e POLYSTAC_LISTEN=":8080" -e POLYSTAC_LOG_FORMAT=text -e POLYSTAC_LOG_LEVEL=warn \
  --seed-http
stop_os

echo
echo ">>> starting fresh OpenSearch for fastapi-os..."
start_os
run_impl "fastapi-os" "ghcr.io/stac-utils/stac-fastapi-os:latest" 18083 8000 \
  -e "ES_HOST=$OS_BOX" -e ES_PORT=9200 -e ES_USE_SSL=false -e ES_VERIFY_CERTS=false \
  -e RUN_LOCAL_ES=false \
  --cmd "uvicorn stac_fastapi.opensearch.app:app --host 0.0.0.0 --port 8000 --workers 1 --no-access-log" \
  --seed-http
stop_os

# ---- aggregate ----------------------------------------------------------

echo
echo ">>> writing report..."
go run "$ROOT/bench/report" -dir "$OUT_DIR" -items "$ITEMS" -duration "$DURATION" -vus "$VUS"
echo ">>> done: $OUT_DIR/report.md"
