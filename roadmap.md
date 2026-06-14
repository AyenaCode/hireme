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
- [ ] **Pagination**: follow the `cursor` when a query returns multiple pages
      (MVP fetches the first page only — fine at current volume).
- [ ] **Quota guard**: count requests/month and refuse to exceed the free-tier
      budget, with a warning push.
- [ ] **Multiple queries** per cycle (e.g. one for DevOps, one for React Native)
      with per-query keyword sets — mind the request budget.
- [ ] **Healthcheck / liveness** endpoint or heartbeat log for the VPS.
- [ ] **Structured config for keywords per role** instead of one flat list.

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
