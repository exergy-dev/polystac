# Drop-in compatibility — PolyStac vs stac-server (Node)

Generated: 2026-05-05T02:41:49Z

- A = http://127.0.0.1:18080 (PolyStac)
- B = http://127.0.0.1:13000 (stac-server)

Cases: 28. Per-endpoint verdicts:

| Verdict | Count |
|---|---:|
| match | 23 |
| expected-diff | 5 |
| diff | 0 |
| endpoint-only-on-A | 0 |
| endpoint-only-on-B | 0 |
| both-fail | 0 |

## expected-diff (5)

| Method | Path | A status | B status | Notes |
|---|---|---:|---:|---|
| GET | `/api` | 200 | 200 | B body not JSON: invalid character 'o' looking for beginning of value; expected: spec-allows-both: PolyStac returns OpenAPI as JSON; stac-server returns it as YAML. The STAC API spec accepts either media type. |
| GET | `/queryables` | 200 | 200 | key "description" missing in B; key "additionalProperties" missing in A; expected: schema-cosmetic: PolyStac includes `description`; stac-server includes `additionalProperties`. Both are valid JSON-Schema fields and clients typically read `properties`. |
| GET | `/collections/compat/queryables` | 200 | 200 | key "description" missing in B; key "additionalProperties" missing in A; expected: schema-cosmetic: same as queryables-global. |
| GET | `/search?filter-lang=cql2-text&filter=%22eo:cloud_cover%22%20%3C%2050&limit=5` | 200 | 400 | status: A=200 B=400; expected: polystac-superset: stac-server's /conformance does not declare cql2-text. PolyStac is strictly more capable here; clients written against stac-server only send cql2-json. |
| POST | `/search *(POST body)*` | 200 | 400 | status: A=200 B=400; expected: polystac-superset: same as search-cql2-text. |

## match (23)

| Method | Path | A status | B status | Notes |
|---|---|---:|---:|---|
| GET | `/` | 200 | 200 | — |
| GET | `/conformance` | 200 | 200 | — |
| GET | `/collections` | 200 | 200 | — |
| GET | `/collections/compat` | 200 | 200 | — |
| GET | `/collections/__nope__` | 404 | 404 | — |
| GET | `/collections/compat/items?limit=5` | 200 | 200 | — |
| GET | `/collections/compat/items?limit=5&page=2` | 200 | 200 | — |
| GET | `/collections/compat/items/item-0000001` | 200 | 200 | — |
| GET | `/collections/compat/items/__nope__` | 404 | 404 | — |
| GET | `/search?limit=5` | 200 | 200 | — |
| GET | `/search?collections=compat&limit=5` | 200 | 200 | — |
| GET | `/search?ids=item-0000001` | 200 | 200 | — |
| GET | `/search?bbox=-10,-10,10,10&limit=5` | 200 | 200 | — |
| GET | `/search?datetime=2024-01-01T00:00:00Z/2024-06-01T00:00:00Z&limit=5` | 200 | 200 | — |
| GET | `/search?sortby=id&limit=5` | 200 | 200 | — |
| GET | `/search?sortby=-properties.eo:cloud_cover&limit=5` | 200 | 200 | — |
| GET | `/search?fields=id,properties.datetime&limit=5` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |
| POST | `/search *(POST body)*` | 200 | 200 | — |

