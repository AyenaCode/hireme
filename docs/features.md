# Hardening — usage guide

Everything is driven by environment variables (`.env`). No code changes.

## Retry / backoff (automatic)

JSearch calls retry network errors, 5xx and 429 on their own (3 attempts,
exponential backoff + jitter, 60 s cap, `Retry-After` honoured). Nothing to set.

## Pagination — `MAX_PAGES`

Pages followed per cycle via the JSearch cursor. `1` = first page only (default).

```bash
MAX_PAGES=2   # ⚠ each extra page = one more request → mind the budget
```

## Quota guard — `MONTHLY_REQUEST_BUDGET`

Counts requests/month (persisted in SQLite). When the budget is reached: the
cycle is skipped and one Telegram warning is pushed (once per month). `0` disables.

```bash
MONTHLY_REQUEST_BUDGET=200   # default = free tier
```

## Multiple queries per cycle — `JOB_QUERY_n` / `KEYWORDS_n`

Numbered from `1` (contiguous). Each query has its own keyword set. When
`JOB_QUERY_1` is set, these pairs replace `JOB_QUERY`/`KEYWORDS`.

```bash
JOB_QUERY_1=devops engineer remote
KEYWORDS_1=devops,kubernetes,platform engineer
JOB_QUERY_2=react native developer remote
KEYWORDS_2=react native,expo
```

> N queries ≈ N× the requests consumed → the quota guard above keeps that safe.

## Liveness — `HEALTH_ADDR`

Enables `/healthz` (200 while the loop is turning, 503 if it's wedged). Empty = off.

```bash
HEALTH_ADDR=:8080
curl http://localhost:8080/healthz
# {"healthy":true,"last_cycle_age_seconds":2,"stale_after_seconds":36000}
```

The Docker image enables it on `:8080` and self-probes (distroless, no shell):

```bash
jobalert -healthcheck   # exit 0 = healthy, 1 = not
```

## Full `.env` example

```bash
JSEARCH_API_KEY=...
TELEGRAM_BOT_TOKEN=...
TELEGRAM_CHAT_ID=...

JOB_QUERY_1=devops engineer remote
KEYWORDS_1=devops,kubernetes,terraform
JOB_QUERY_2=react native developer remote
KEYWORDS_2=react native,expo

POLL_INTERVAL=5h
MAX_PAGES=1
MONTHLY_REQUEST_BUDGET=200
HEALTH_ADDR=:8080
```
