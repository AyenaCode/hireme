# hireme : Job Alert

A small, single-binary Go service that polls [JSearch](https://www.openwebninja.com/api/jsearch)
for new remote jobs, dedups them in SQLite, and pushes new matches to a
**Telegram** chat. It ships tuned for **DevOps / React Native**, but the search
is fully driven by config : set it to any role JSearch supports (see
[Search any job type](#search-any-job-type)).

One process. One config file. No external cron, no database server, no app to install.

> **See it live, no setup.** A running instance posts remote **DevOps / React
> Native** jobs to a public Telegram channel : just join to watch the alerts
> come in: **[t.me/hiremeDevOps](https://t.me/hiremeDevOps)**.
>
> **Want your own filters?** This repo is open : clone it, plug in two free
> credentials, and you'll get personalised job alerts on your own phone in a few
> minutes. See [Try it yourself](#try-it-yourself).

## Try it yourself

Everything you need is free (no credit card) and takes ~5 minutes.

**1. Get a JSearch API key** : sign up at
[openwebninja.com](https://app.openwebninja.com/signup) (free tier: 200
requests/month). Copy your key.

**2. Create a Telegram bot** : message [@BotFather](https://t.me/BotFather),
send `/newbot`, follow the prompts, and copy the **bot token**.

**3. Get your chat id** : send any message to your new bot, then open
`https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates` in a browser and read
`result[].message.chat.id`.

**4. Configure and run:**

```bash
git clone https://github.com/<your-username>/hireme.git
cd hireme
cp .env.example .env        # paste the key, token, and chat id

# one cycle then exit : instant smoke test, no waiting:
RUN_ONCE=true go run ./cmd/jobalert
```

If your filters match anything posted today, the bot messages you on the spot.
Drop the `RUN_ONCE` to keep it running on the loop.

## How it works

```
            ┌─────────────────────────────────────────────┐
            │   one long-running process (time.Ticker)     │
            │                                              │
  every     │   JSearch search-v2 ──► filter by keyword ──►│
POLL_INTERVAL│        │                                    │
  (5h)      │   save new job_id to SQLite (dedup)          │
            │        │                                     │
            │   push new match ──► Telegram bot ──► phone  │
            └─────────────────────────────────────────────┘
```

Each tick runs one cycle: fetch → filter on title/description keywords → insert
new `job_id`s (dedup is the SQLite primary key) → push each genuinely new match
to Telegram → mark it notified. A failed push leaves the job un-notified so it
retries next cycle. Errors are logged; the loop never dies on a transient fault.

Transient JSearch failures (network errors, 5xx, 429) are retried in-process
with exponential backoff + jitter, honouring a `Retry-After` header up to 60s; a
longer wait or a non-transient 4xx is left for the next poll cycle.

## Project layout

```
cmd/jobalert/       entrypoint: config, wiring, signal-aware shutdown
internal/config/    env + .env loading, defaults, validation
internal/jsearch/   JSearch search-v2 client + Job model
internal/filter/    case-insensitive keyword matching
internal/store/     SQLite persistence + dedup (pure-Go driver)
internal/notify/    Telegram Bot API push
internal/app/       the polling loop that ties it together
```

## Configuration

All config is via environment variables (a local `.env` is auto-loaded if
present). See [`.env.example`](.env.example) for the full list. Required:

| Variable | Description |
|----------|-------------|
| `JSEARCH_API_KEY` | JSearch API key (openwebninja.com) |
| `TELEGRAM_BOT_TOKEN` | Bot token from @BotFather |
| `TELEGRAM_CHAT_ID` | Your chat id |

Key optional knobs: `JOB_QUERY`, `KEYWORDS`, `DATE_POSTED`, `POLL_INTERVAL`
(default `5h`), `RUN_ONCE`, `DB_PATH`.

> **Free-tier note:** JSearch free = 200 requests/month. `POLL_INTERVAL=5h`
> (~150/mo) stays inside it. Hourly polling needs the Pro plan.

### Search any job type

The service isn't locked to DevOps. Filtering happens in two stages, both
config-only : no code change:

1. **`JOB_QUERY`** is sent to JSearch and decides *which* jobs the API returns.
   Put any role here (`data engineer remote`, `product designer remote`, …).
2. **`KEYWORDS`** is a local filter: a job is kept only if its title or
   description contains at least one of these (comma-separated, case-insensitive).

Switching roles is two lines in `.env`:

```bash
JOB_QUERY=data engineer remote
KEYWORDS=python,airflow,spark,dbt
```

> Keep the two in sync : if `JOB_QUERY` and `KEYWORDS` describe different jobs,
> the local filter discards almost everything and you'll get no alerts.

Other knobs: `DATE_POSTED` (`all`/`today`/`3days`/`week`/`month`), `REMOTE_ONLY`,
`JOB_COUNTRY` + `JOB_LANGUAGE` (e.g. `fr`/`fr`). Today it runs **one query per
cycle** : searching two unrelated roles at once is on the [roadmap](roadmap.md).

## Run locally.

```bash
cp .env.example .env        # fill in the three secrets
go build -o jobalert ./cmd/jobalert

# one cycle then exit : best for the first smoke test:
RUN_ONCE=true ./jobalert

# or run the long-lived loop:
./jobalert
```

## Run with Docker

```bash
docker build -t hireme .
docker run -d --name hireme \
  --env-file .env \
  -v hireme-data:/data \
  --restart unless-stopped \
  hireme
```

The image is built `CGO_ENABLED=0` on `distroless/static` (nonroot): no shell,
CA certs included, a few MB. The SQLite file lives on the `/data` volume.

## Test

```bash
go test ./...
```

## Roadmap

See [roadmap.md](roadmap.md).
