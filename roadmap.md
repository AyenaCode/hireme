# Roadmap

## ✅ MVP (done)

- [x] **1. Go CLI** — call JSearch, filter, surface jobs.
- [x] **2. SQLite + dedup** by `job_id`; only act on jobs never seen before.
- [x] **3. Ticker loop** with `POLL_INTERVAL` from config (self-scheduling, no OS cron).
- [x] **4. Telegram push** — each new match sent to the chat, with retry-on-fail.
- [x] **5. Dockerfile** — multi-stage, `CGO_ENABLED=0`, distroless/static nonroot.
- [x] Config via env + `.env`, validation, graceful shutdown (SIGINT/SIGTERM).
- [x] Unit tests for filter, store dedup, and Telegram formatting/escaping.

## 🔧 Hardening (do before relying on it daily)

These are small, high-value items the MVP intentionally left out.

- [x] **Verify the live JSearch response shape.** Done — corrected against the
      official OpenAPI spec and a live response. Jobs are nested under
      `data.jobs` (an object, not an array). The enrichment fields
      (`work_arrangement`, `seniority_level`, `required_experience_years`,
      `required_technologies`, `job_function`) do **not** exist in `search-v2` —
      they are `/job-details` only — and were removed from the `Job` model.
- [x] **Retry/backoff** on JSearch 429/5xx. The `search-v2` client now retries
      transport errors, 5xx and 429 with exponential backoff + jitter (3 retries),
      honours a delta-seconds `Retry-After` capped at 60s (a longer hint bails to
      the next poll cycle rather than blocking), and never retries on other 4xx
      or context cancellation.
- [x] **Pagination**: `SearchAll` follows the `data.cursor` up to `MAX_PAGES`
      pages per cycle (default `1` = first page only, quota-safe). A partial page
      failure returns the pages already fetched rather than dropping them. Note:
      `MAX_PAGES>1` exceeds the free tier at the default interval — see
      `.env.example`; the Quota guard below is what makes higher values safe.
- [x] **Quota guard**: JSearch requests are counted per calendar (UTC) month in
      SQLite (survives restarts). When usage reaches `MONTHLY_REQUEST_BUDGET`
      (default 200 = free tier; `0` disables) the cycle is skipped and one
      warning is pushed to Telegram — once per month, persisted so a restart
      doesn't re-spam. Usage is recorded even on failed cycles (those requests
      still cost quota); the month boundary is an approximation of the provider's
      reset, which is fine for runaway-prevention.
- [x] **Multiple queries** per cycle (e.g. one for DevOps, one for React Native)
      with per-query keyword sets. Configured via numbered `JOB_QUERY_n` /
      `KEYWORDS_n` (contiguous from 1; falls back to the single `JOB_QUERY` /
      `KEYWORDS` for backward compatibility). The quota guard is checked before
      every query, so a multi-query cycle stops the moment the budget is reached.
- [x] **Healthcheck / liveness** endpoint. `HEALTH_ADDR` (e.g. `:8080`) enables a
      `/healthz` endpoint that returns 200 while cycles keep completing and 503 if
      the loop is wedged (no cycle within `2×POLL_INTERVAL`). Liveness reflects
      only that the loop turns — it stays green during JSearch/quota failures so a
      probe never crash-loops the process on an upstream outage. The distroless
      image self-probes via `jobalert -healthcheck` (no shell needed).
- [x] **Structured config for keywords per role** instead of one flat list.
      Covered by the numbered `JOB_QUERY_n` / `KEYWORDS_n` pairs above — each role
      now carries its own keyword set.

## 🚀 V2 — more sources

Reduce dependence on a single API and get fresher data.

- [ ] Add **RemoteOK** and **Himalayas** as additional sources behind a common
      `Source` interface (the app already depends on small interfaces).
- [ ] Add **ATS endpoints** (Greenhouse, Lever, Ashby) for target companies.
- [ ] Cross-source dedup (same role surfaced by two providers).

## 📱 V3 — the app (portfolio piece)

- [ ] **React Native + Expo** client with Expo push notifications.
- [ ] Push channel becomes pluggable (Telegram **and/or** Expo) — the `notifier`
      interface is already in place.
- [ ] Saved searches, mark-as-applied, basic job history UI.

## ⚙️ DevOps layer (portfolio proof)

- [ ] **GitHub Actions**: build + test + vet on PR; build and push image on tag.
- [ ] Trivy/govulncheck scan in CI.
- [ ] Deploy to the VPS via the pipeline (compose or systemd), then to a real
      **Kubernetes** cluster (Deployment + PVC for SQLite, or migrate to Postgres).
- [ ] Metrics (Prometheus) + alerting on cycle failures.
