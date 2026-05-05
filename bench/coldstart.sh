#!/usr/bin/env bash
# Tight cold-start benchmark: per-impl wall-clock from `docker run` to
# the first responsive HTTP request, polling at 10 ms, repeated N times.
# Reports min / median / p95 along with the image-pull cost (measured
# separately so it doesn't pollute the steady-state cold-start number).
#
# What "responsive" means here: the first curl that returns a non-zero
# HTTP status. Both impls under test only complete their listen-and-
# accept after their startup work (index templates etc.) finishes, so
# the response is a real readiness signal, not a half-started state.
#
# Usage: ./bench/coldstart.sh [trials=10]

set -euo pipefail

TRIALS="${1:-10}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="$ROOT/bench/results/coldstart-$RUN_TS"
mkdir -p "$OUT_DIR"

NET="polystac-cold-${RUN_TS}"
docker network create "$NET" >/dev/null

PGSTAC="polystac-cold-pgstac-$RUN_TS"
OS_BOX="polystac-cold-os-$RUN_TS"
IMPL="polystac-cold-impl-$RUN_TS"

cleanup() {
  docker rm -f "$PGSTAC" "$OS_BOX" "$IMPL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---- shared services ---------------------------------------------------

echo ">>> starting shared pgstac..."
docker run -d --rm --name "$PGSTAC" --network "$NET" \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgis \
  ghcr.io/stac-utils/pgstac:v0.8.5 >/dev/null
for i in $(seq 1 120); do
  PGPASSWORD=postgres docker exec "$PGSTAC" psql -U postgres -d postgis -c "SELECT pgstac.get_version();" >/dev/null 2>&1 && break
  sleep 1
done
echo "    pgstac ready"

echo ">>> starting shared OpenSearch..."
docker run -d --rm --name "$OS_BOX" --network "$NET" \
  -e discovery.type=single-node -e plugins.security.disabled=true \
  -e OPENSEARCH_INITIAL_ADMIN_PASSWORD='ChangeMe!23' -e DISABLE_INSTALL_DEMO_CONFIG=true \
  opensearchproject/opensearch:2.13.0 >/dev/null
for i in $(seq 1 120); do
  docker exec "$OS_BOX" curl -fsS http://localhost:9200/ >/dev/null 2>&1 && break
  sleep 1
done
echo "    opensearch ready"

# ---- build polystac image ----------------------------------------------

echo ">>> building polystac image..."
( cd "$ROOT" \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -trimpath -ldflags='-s -w' -o "$ROOT/bench/polystac" ./cmd/polystac \
  && docker build -q -f bench/Dockerfile.polystac -t "polystac:cold-$RUN_TS" bench/ \
  && rm -f "$ROOT/bench/polystac" \
) >/dev/null

# ---- image pull / inspect cost separately ------------------------------

pull_cost() {
  local image="$1"
  # Already-cached pull — measures only the manifest + layer-check
  # round-trip, never an actual byte download. So this number is a
  # lower bound for "first user pull"; for that measure on a fresh
  # node, run with the local cache cleared.
  local t0 t1
  t0="$(date +%s%3N)"
  docker pull --quiet "$image" >/dev/null 2>&1 || true
  t1="$(date +%s%3N)"
  echo $((t1 - t0))
}

# ---- one trial: docker run → first non-000 status ----------------------

trial() {
  local image="$1" cport="$2"; shift 2
  local args=("$@")
  # Boundary timer: docker create is reasonably cheap, but we want
  # *just* the runtime+app start. Time from docker run to first reply.
  local start ready
  start="$(date +%s%6N)"
  docker run -d --rm --name "$IMPL" --network "$NET" "${args[@]}" "$image" >/dev/null
  # Find the assigned host port we'd need... we don't bother — exec
  # into the container with curl instead, eliminating host-side port
  # forwarding latency from the measurement.
  local elapsed_us=""
  for i in $(seq 1 6000); do
    if docker exec "$IMPL" curl -s -o /dev/null -w '%{http_code}' --max-time 0.2 "http://127.0.0.1:$cport/" 2>/dev/null | grep -q '^[1-9]'; then
      ready="$(date +%s%6N)"
      elapsed_us=$((ready - start))
      break
    fi
    sleep 0.01
  done
  docker rm -f "$IMPL" >/dev/null 2>&1 || true
  if [ -z "$elapsed_us" ]; then echo "TIMEOUT"; return 1; fi
  printf '%.0f\n' "$(echo "scale=3; $elapsed_us / 1000" | bc)"
}

# Some images don't ship curl. Detect once and fall back to a TCP probe.
trial_or_tcp() {
  local image="$1" cport="$2"; shift 2
  local args=("$@")
  local has_curl=0
  if docker run --rm --entrypoint sh "$image" -c 'command -v curl' >/dev/null 2>&1; then
    has_curl=1
  fi
  local start ready
  start="$(date +%s%6N)"
  docker run -d --rm --name "$IMPL" --network "$NET" "${args[@]}" "$image" >/dev/null
  local elapsed_us=""
  for i in $(seq 1 6000); do
    local ok=1
    if [ "$has_curl" = "1" ]; then
      docker exec "$IMPL" sh -c "curl -s -o /dev/null -w '%{http_code}' --max-time 0.2 http://127.0.0.1:$cport/ 2>/dev/null | grep -q '^[1-9]'" || ok=0
    else
      # /dev/tcp is bash-only; sh in distroless lacks it. Use a tiny
      # external probe instead: docker port + curl from host.
      ok=0
      local hp
      hp="$(docker port "$IMPL" "$cport" 2>/dev/null | head -1 | awk -F: '{print $NF}')"
      if [ -n "$hp" ]; then
        s="$(curl -s -o /dev/null -w '%{http_code}' --max-time 0.2 "http://127.0.0.1:$hp/" 2>/dev/null || echo 000)"
        [ "$s" != "000" ] && ok=1
      fi
    fi
    if [ "$ok" = "1" ]; then
      ready="$(date +%s%6N)"
      elapsed_us=$((ready - start))
      break
    fi
    sleep 0.01
  done
  docker rm -f "$IMPL" >/dev/null 2>&1 || true
  if [ -z "$elapsed_us" ]; then echo "TIMEOUT"; return 1; fi
  echo $((elapsed_us / 1000))
}

# ---- per-impl runner ---------------------------------------------------

run_cell() {
  local label="$1" image="$2" cport="$3"; shift 3
  local args=("$@")
  local pcost
  pcost="$(pull_cost "$image")"
  echo
  echo ">>> $label  ($image)"
  echo "    image manifest re-check: ${pcost} ms"
  local out="$OUT_DIR/$label.csv"
  printf 'trial,cold_ms\n' >"$out"
  for t in $(seq 1 "$TRIALS"); do
    local ms
    if ! ms="$(trial_or_tcp "$image" "$cport" "${args[@]}")"; then
      echo "    trial $t: TIMEOUT"
      printf '%d,TIMEOUT\n' "$t" >>"$out"
      continue
    fi
    printf '    trial %2d: %5d ms\n' "$t" "$ms"
    printf '%d,%d\n' "$t" "$ms" >>"$out"
    # Brief pause so the OCI / containerd state quiesces between trials.
    sleep 0.5
  done
}

# ---- run cells ---------------------------------------------------------

run_cell "polystac-pgstac" "polystac:cold-$RUN_TS" 8080 \
  -p 18080:8080 \
  -e POLYSTAC_BACKEND=pgstac \
  -e "POLYSTAC_PG_DSN=postgresql://postgres:postgres@$PGSTAC:5432/postgis?sslmode=disable" \
  -e POLYSTAC_LISTEN=":8080" -e POLYSTAC_LOG_FORMAT=text -e POLYSTAC_LOG_LEVEL=warn

run_cell "fastapi-pgstac" "ghcr.io/stac-utils/stac-fastapi-pgstac:latest" 8080 \
  -p 18081:8080 \
  -e "POSTGRES_HOST_READER=$PGSTAC" -e "POSTGRES_HOST_WRITER=$PGSTAC" \
  -e POSTGRES_PORT=5432 -e POSTGRES_USER=postgres -e POSTGRES_PASS=postgres \
  -e POSTGRES_DBNAME=postgis -e DB_MIN_CONN_SIZE=2 -e DB_MAX_CONN_SIZE=20

# OS impls share OS host since cold-start doesn't write anything we care
# about beyond template installation; both impls install their own
# templates. Note: polystac-os refresh=false doesn't matter for cold start.
run_cell "polystac-os" "polystac:cold-$RUN_TS" 8080 \
  -p 18082:8080 \
  -e POLYSTAC_BACKEND=opensearch \
  -e "POLYSTAC_ES_HOSTS=http://$OS_BOX:9200" \
  -e POLYSTAC_ES_VERIFY_CERTS=false \
  -e POLYSTAC_LISTEN=":8080" -e POLYSTAC_LOG_FORMAT=text -e POLYSTAC_LOG_LEVEL=warn

# fastapi-os has no curl in the image but exposes 8000.
run_cell "fastapi-os" "ghcr.io/stac-utils/stac-fastapi-os:latest" 8000 \
  -p 18083:8000 \
  -e "ES_HOST=$OS_BOX" -e ES_PORT=9200 -e ES_USE_SSL=false -e ES_VERIFY_CERTS=false \
  -e RUN_LOCAL_ES=false

# ---- aggregate ---------------------------------------------------------

echo
echo ">>> aggregating..."
python3 <<EOF | tee "$OUT_DIR/report.md"
import csv, glob, os, statistics

print("# Cold-start (tightened): docker run → first HTTP response")
print()
print("- Trials per impl: ${TRIALS}")
print("- Probe granularity: 10 ms (was 250 ms in the main bench)")
print("- Probe issued from inside the container (or via host port if the image lacks curl); excludes host-side port-forwarding noise where possible.")
print("- All images pre-pulled; image-pull cost reported separately.")
print()
print("| impl | trials | min ms | median ms | p95 ms | max ms |")
print("|---|---:|---:|---:|---:|---:|")

for path in sorted(glob.glob("$OUT_DIR/*.csv")):
    label = os.path.basename(path)[:-4]
    vals = []
    with open(path) as f:
        for row in csv.DictReader(f):
            try: vals.append(int(row["cold_ms"]))
            except (ValueError, KeyError): pass
    if not vals:
        print(f"| {label} | 0 | — | — | — | — |")
        continue
    vals.sort()
    p95 = vals[max(0, int(round(0.95 * (len(vals)-1))))]
    print(f"| {label} | {len(vals)} | {min(vals)} | {statistics.median(vals):.0f} | {p95} | {max(vals)} |")
EOF

echo
echo ">>> done: $OUT_DIR/report.md"
