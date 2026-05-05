# PolyStac vs reference implementations — performance diff

Generated: 2026-05-05T01:39:56Z

**Configuration**

- Items seeded per backend: **1000**
- k6 duration: **30s**
- k6 VUs: **20**
- Mix: 6 endpoints uniformly sampled — landing, /collections, search (all/bbox/datetime/cql2-text).
- pgstac (v0.8.5) is shared across both pgstac impls (both call the same `pgstac.search`/`create_items` SQL — apples-to-apples).
- OpenSearch (2.13.0) gets a fresh container per impl (impls use incompatible index layouts) and each impl HTTP-seeds its own data via its native write path.

## pgstac backend

### Static cost

| Impl | Image size | Cold start | Idle RSS | Peak RSS (under load) |
|---|---:|---:|---:|---:|
| polystac-pgstac | 15 MiB | 365 ms | 7.457MiB | 27.1 MiB |
| fastapi-pgstac | 588 MiB | 442 ms | 54.31MiB | 63.8 MiB |

### Throughput & error rate

| Impl | Total requests | Req/sec | Error rate |
|---|---:|---:|---:|
| polystac-pgstac | 71467 | 2381.4 | 0.01% |
| fastapi-pgstac | 15306 | 509.7 | 0.00% |

### Per-scenario p95 latency (ms, lower is better)

| Scenario | polystac-pgstac | fastapi-pgstac | ratio (ref ÷ polystac) |
|---|---:|---:|---:|
| landing | 3.00 | 23.0 | 7.67× |
| collections | 4.00 | 45.0 | 11.25× |
| search_all | 18.0 | 51.0 | 2.83× |
| search_bbox | 14.0 | 49.0 | 3.50× |
| search_dt | 18.0 | 51.0 | 2.83× |
| search_cql2 | 19.0 | 51.0 | 2.68× |

### Per-scenario median latency (ms)

| Scenario | polystac-pgstac | fastapi-pgstac |
|---|---:|---:|
| landing | 1.00 | 18.0 |
| collections | 1.00 | 38.0 |
| search_all | 12.0 | 44.0 |
| search_bbox | 9.00 | 42.0 |
| search_dt | 12.0 | 44.0 |
| search_cql2 | 13.0 | 44.0 |

## opensearch backend

### Static cost

| Impl | Image size | Cold start | Idle RSS | Peak RSS (under load) |
|---|---:|---:|---:|---:|
| polystac-os | 15 MiB | 423 ms | 10.76MiB | 50.3 MiB |
| fastapi-os | 401 MiB | 401 ms | 80.03MiB | 84.9 MiB |

### Throughput & error rate

| Impl | Total requests | Req/sec | Error rate |
|---|---:|---:|---:|
| polystac-os | 60618 | 2018.9 | 0.07% |
| fastapi-os | 9111 | 303.3 | 0.00% |

### Per-scenario p95 latency (ms, lower is better)

| Scenario | polystac-os | fastapi-os | ratio (ref ÷ polystac) |
|---|---:|---:|---:|
| landing | 5.00 | 41.0 | 8.20× |
| collections | 38.0 | 94.0 | 2.47× |
| search_all | 39.3 | 109.0 | 2.77× |
| search_bbox | 40.0 | 107.5 | 2.69× |
| search_dt | 41.0 | 114.0 | 2.78× |
| search_cql2 | 42.0 | 158.0 | 3.76× |

### Per-scenario median latency (ms)

| Scenario | polystac-os | fastapi-os |
|---|---:|---:|
| landing | 1.00 | 33.0 |
| collections | 5.00 | 46.0 |
| search_all | 8.00 | 61.0 |
| search_bbox | 7.00 | 60.0 |
| search_dt | 8.00 | 61.0 |
| search_cql2 | 8.00 | 96.0 |

## Methodology

- pgstac side: the same Postgres+pgstac container feeds both PolyStac and stac-fastapi-pgstac; data is bulk-seeded once via `pgstac.create_items` and read by both impls. The only thing that changes between rows is the API server.
- OpenSearch side: each impl gets its own fresh OpenSearch and seeds itself by ingesting the same N items through its own POST /collections/{id}/items endpoint. Data is logically identical but stored in each impl's native index layout.
- Cold start: wall-clock from `docker run` to the first 200 on `/`.
- Idle RSS: `docker stats` snapshot 5 s after the impl reports ready, no traffic.
- Peak RSS: max `docker stats` sample taken once per second during the k6 run.
- Latency includes one localhost network hop, JSON marshal, and (for /search) one round-trip to the backend service.
- All requests in the mix have `limit=10` so payload size is comparable.

Run with `bench/run.sh [items] [duration] [vus]` (defaults: 1000 / 30s / 20).
