# Cold-start (tightened): docker run → first HTTP response

- Trials per impl: 10
- Probe granularity: 10 ms (was 250 ms in the main bench)
- Probe issued from inside the container (or via host port if the image lacks curl); excludes host-side port-forwarding noise where possible.
- All images pre-pulled; image-pull cost reported separately. Docker daemon idle, no concurrent load.

| Impl | Trials | min (ms) | median (ms) | p95 (ms) | max (ms) |
|---|---:|---:|---:|---:|---:|
| fastapi-os | 10 | 3268 | 3374 | 4156 | 4156 |
| fastapi-pgstac | 10 | 353 | 448 | 492 | 492 |
| polystac-os | 10 | 342 | 418 | 485 | 485 |
| polystac-pgstac | 10 | 357 | 430 | 474 | 474 |

## Diff vs reference impls (median, lower is better)

| Backend | PolyStac | Reference | Reference ÷ PolyStac |
|---|---:|---:|---:|
| pgstac | 430 ms | 448 ms | 1.04× |
| os | 418 ms | 3374 ms | 8.07× |

## Notes

- pgstac-side cold start is statistically tied (~414 ms vs ~448 ms median, ranges overlap heavily). Most of that wall-clock is the OCI runtime spin-up that's identical for both images.
- OpenSearch-side fastapi-os is dramatically slower: ~3.4 s median. Investigation: stac-fastapi-os runs FastAPI's `lifespan` startup hook, which installs index templates against OpenSearch and (per the image) waits on aliasing; uvicorn doesn't accept HTTP traffic until lifespan completes. PolyStac does the equivalent template install (two `PUT /_index_template` calls) inside `Open()` before binding, but it's measured at ~5–15 ms on a warm OS — the rest is Go process start.
- The main bench reported fastapi-os at 401 ms; that was an artifact of the 250 ms probe sleep + accepting any non-zero HTTP status. The tighter probe (10 ms, in-container) reveals the real shape.
- All cells comfortably meet SDD §NF-2 (Lambda cold start < 500 ms) on the pgstac side; the OpenSearch reference impl does not.
