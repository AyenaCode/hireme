# Usage guide

One process polls JSearch for jobs, filters them by keyword, dedups in SQLite,
and pushes new matches to Telegram. Everything is configured with environment
variables (`.env`). No code changes.

## Quick start

```bash
cp .env.example .env        # fill in the three secrets below
RUN_ONCE=true go run ./cmd/jobalert   # one cycle, instant smoke test
go run ./cmd/jobalert                 # the long-running loop
```

Required secrets:

| Variable | What |
|----------|------|
| `JSEARCH_API_KEY` | JSearch key (openwebninja.com) |
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Your chat id |

## Search

`JOB_QUERY` is sent to JSearch (which jobs come back). `KEYWORDS` is the local
filter (a job is kept if its title or description contains any of them,
case insensitive).

```bash
JOB_QUERY=data engineer remote
KEYWORDS=python,airflow,spark,dbt
```

Optional: `DATE_POSTED` (`all` / `today` / `3days` / `week` / `month`),
`REMOTE_ONLY` (default `true`), `JOB_COUNTRY` + `JOB_LANGUAGE` (e.g. `fr` / `fr`).

### Several roles at once

Numbered pairs from `1` (contiguous), each with its own keyword set. When
`JOB_QUERY_1` is set, these replace `JOB_QUERY` / `KEYWORDS`.

```bash
JOB_QUERY_1=devops engineer remote
KEYWORDS_1=devops,kubernetes,platform engineer
JOB_QUERY_2=react native developer remote
KEYWORDS_2=react native,expo
```

## Scheduling

```bash
POLL_INTERVAL=5h     # how often a cycle runs (default 5h, about 150 req/month)
RUN_ONCE=false       # true: one cycle then exit (testing / cron)
```

## Pagination

Pages followed per cycle via the JSearch cursor. `1` = first page only.

```bash
MAX_PAGES=2          # each extra page is one more request, mind the budget
```

## Quota guard

Counts requests per month (persisted in SQLite). At the budget, the cycle is
skipped and one Telegram warning is pushed (once per month). `0` disables it.

```bash
MONTHLY_REQUEST_BUDGET=200   # default = free tier
```

## Reliability (automatic)

JSearch calls retry network errors, 5xx and 429 on their own (3 attempts,
exponential backoff with jitter, 60 s cap, `Retry-After` honoured). The loop
never dies on a transient fault, and SIGINT / SIGTERM shut it down cleanly.

## Liveness

Enables `/healthz`: 200 while the loop keeps completing cycles, 503 if it is
wedged. Empty = off. It stays green during a JSearch outage or quota pause.

```bash
HEALTH_ADDR=:8080
curl http://localhost:8080/healthz
# {"healthy":true,"last_cycle_age_seconds":2,"stale_after_seconds":36000}
```

## Storage

```bash
DB_PATH=data/jobs.db   # SQLite file (dedup state lives here)
```

## Docker

```bash
docker build -t hireme .
docker run -d --name hireme --env-file .env -v hireme-data:/data \
  --restart unless-stopped hireme
```

Distroless static image (no shell, CA certs included). It exposes `/healthz` on
`:8080` and self-probes via the binary (`jobalert -healthcheck`), so the Docker
`HEALTHCHECK` works without curl. The SQLite file lives on the `/data` volume.

## Full `.env` example

```bash
JSEARCH_API_KEY=...
TELEGRAM_BOT_TOKEN=...
TELEGRAM_CHAT_ID=...

JOB_QUERY_1=devops engineer remote
KEYWORDS_1=devops,kubernetes,terraform
JOB_QUERY_2=react native developer remote
KEYWORDS_2=react native,expo

DATE_POSTED=today
REMOTE_ONLY=true
POLL_INTERVAL=5h
MAX_PAGES=1
MONTHLY_REQUEST_BUDGET=200
HEALTH_ADDR=:8080
DB_PATH=data/jobs.db
```
