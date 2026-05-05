// k6 load test for /search. Targets the SDD §NF-1 budget (P95 ≤ 150 ms
// for `/search?bbox=...&datetime=...&limit=10` against a 10 M-item index).
//
// Run:
//   k6 run -e POLYSTAC_URL=http://localhost:8000 load/k6/search.js
//
// The mix matches typical client traffic: 90% reads (item search,
// collection list, item get), 10% writes (item upsert).

import http from "k6/http";
import { check, sleep } from "k6";
import { Trend } from "k6/metrics";

const BASE = __ENV.POLYSTAC_URL || "http://localhost:8000";
const COLLECTION = __ENV.POLYSTAC_COLLECTION || "smoke";

const searchLatency = new Trend("polystac_search_latency_ms");

export const options = {
  scenarios: {
    reads: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "30s", target: 20 },
        { duration: "2m",  target: 50 },
        { duration: "1m",  target: 0 },
      ],
      exec: "doRead",
    },
    writes: {
      executor: "constant-vus",
      vus: 2,
      duration: "3m30s",
      exec: "doWrite",
    },
  },
  thresholds: {
    "polystac_search_latency_ms": ["p(95)<150"],
    "http_req_failed": ["rate<0.01"],
  },
};

export function doRead() {
  const start = Date.now();
  const r = http.get(`${BASE}/search?bbox=-10,-10,10,10&datetime=2024-01-01T00:00:00Z/..&limit=10`);
  searchLatency.add(Date.now() - start);
  check(r, { "status 200": (x) => x.status === 200 });
  sleep(0.1);
}

export function doWrite() {
  const id = `k6-${Date.now()}-${__VU}`;
  const body = JSON.stringify({
    type: "Feature",
    stac_version: "1.0.0",
    id: id,
    collection: COLLECTION,
    geometry: { type: "Point", coordinates: [0, 0] },
    bbox: [0, 0, 0, 0],
    properties: { datetime: "2024-01-01T00:00:00Z", "eo:cloud_cover": 12 },
    links: [],
    assets: {},
  });
  const r = http.post(`${BASE}/collections/${COLLECTION}/items`, body, {
    headers: { "Content-Type": "application/json" },
  });
  check(r, { "status 201": (x) => x.status === 201 });
  sleep(1);
}
