// k6 load profile for the head-to-head benchmark.
//
// Same script run against every implementation. Six request shapes,
// uniformly mixed:
//
//   1. landing
//   2. /collections
//   3. GET /search?limit=10
//   4. GET /search?bbox=...&limit=10
//   5. GET /search?datetime=...&limit=10
//   6. GET /search?filter=<cql2-text>&limit=10
//
// Output is JSON summary that the run script picks apart.

import http from "k6/http";
import { check } from "k6";
import { Trend, Counter } from "k6/metrics";

const BASE = __ENV.URL || "http://localhost:8000";
const COLLECTION = __ENV.COLLECTION || "bench";

export const options = {
  scenarios: {
    main: {
      executor: "constant-vus",
      vus: parseInt(__ENV.VUS || "20"),
      duration: __ENV.DURATION || "30s",
      gracefulStop: "5s",
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.05"],
  },
  summaryTrendStats: ["min", "med", "p(90)", "p(95)", "p(99)", "max"],
};

const scenarios = [
  { name: "landing",     path: "/" },
  { name: "collections", path: "/collections" },
  { name: "search_all",  path: "/search?limit=10" },
  { name: "search_bbox", path: "/search?bbox=-10,-10,10,10&limit=10" },
  { name: "search_dt",   path: "/search?datetime=2024-01-01T00:00:00Z/2024-06-01T00:00:00Z&limit=10" },
  { name: "search_cql2", path: `/search?filter=${encodeURIComponent('"eo:cloud_cover" < 50')}&filter-lang=cql2-text&limit=10` },
];

const latencies = {};
const counts = {};
for (const s of scenarios) {
  latencies[s.name] = new Trend(`latency_${s.name}_ms`, true);
  counts[s.name]    = new Counter(`requests_${s.name}`);
}

export default function () {
  const s = scenarios[Math.floor(Math.random() * scenarios.length)];
  const start = Date.now();
  const r = http.get(BASE + s.path, { tags: { name: s.name } });
  latencies[s.name].add(Date.now() - start);
  counts[s.name].add(1);
  check(r, { "ok": (x) => x.status === 200 });
}
