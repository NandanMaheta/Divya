---

paths:

- "apps/worker/**"

---

# Python worker rules

- Python 3.13, uv only (`uv add`, `uv run`). Ruff + pyright strict must pass.
- The worker fetches and enriches external data (IPO details, subscription, RHP briefs, GMP suggestions) and hands results to the Go API; it never computes or stores money.
- Respect source terms: no captcha bypass, no scraping of NSE pages; prefer documented APIs. Record source + fetched-at time with every datum.
- HTTP via `httpx` with timeouts and retries; tests use recorded fixtures, never live network.
