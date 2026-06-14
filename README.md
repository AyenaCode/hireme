# hireme : Job Alert

A small, single-binary Go service that polls [JSearch](https://www.openwebninja.com/api/jsearch)
for new remote jobs, dedups them in SQLite, and pushes new matches to a
**Telegram** chat. Tuned for **DevOps / React Native** out of the box, but driven
entirely by config: set it to any role JSearch supports.

One process. One config file. No external cron, no database server, no app to install.

> **See it live, no setup.** A running instance posts remote **DevOps / React
> Native** jobs to a public Telegram channel. Join to watch the alerts come in:
> **[t.me/hiremeDevOps](https://t.me/hiremeDevOps)**.

## Try it yourself

Everything is free (no credit card) and takes about 5 minutes.

1. **JSearch API key**: sign up at [openwebninja.com](https://app.openwebninja.com/signup)
   (free tier: 200 requests/month).
2. **Telegram bot**: message [@BotFather](https://t.me/BotFather), send `/newbot`,
   copy the **bot token**.
3. **Chat id**: message your bot, then open
   `https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates` and read
   `result[].message.chat.id`.

```bash
cp .env.example .env                  # paste the key, token, and chat id
RUN_ONCE=true go run ./cmd/jobalert   # one cycle, instant smoke test
go run ./cmd/jobalert                 # the long-running loop
```

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

Each tick runs one cycle: fetch, filter on title/description keywords, insert new
`job_id`s (dedup is the SQLite primary key), push each genuinely new match, mark
it notified. A failed push leaves the job un-notified so it retries next cycle.
JSearch calls retry transient errors with backoff; the loop never dies on a
transient fault and shuts down cleanly on SIGINT/SIGTERM.

## Configuration

All settings are environment variables (a local `.env` is auto-loaded). Three
secrets are required: `JSEARCH_API_KEY`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`.

See **[docs/features.md](docs/features.md)** for every knob (search, multiple
roles, pagination, quota guard, liveness) with examples, and
[`.env.example`](.env.example) for the full list.

> Free tier is 200 requests/month. `POLL_INTERVAL=5h` (about 150/mo) stays inside it.

## Project layout

```
cmd/jobalert/       entrypoint: config, wiring, signal-aware shutdown
internal/config/    env + .env loading, defaults, validation
internal/jsearch/   JSearch search-v2 client + Job model
internal/filter/    case-insensitive keyword matching
internal/store/     SQLite persistence + dedup (pure-Go driver)
internal/notify/    Telegram Bot API push
internal/health/    /healthz liveness reporter
internal/app/       the polling loop that ties it together
```

## Docker

```bash
docker build -t hireme .
docker run -d --name hireme --env-file .env -v hireme-data:/data \
  --restart unless-stopped hireme
```

Distroless static image (no shell, CA certs included). It exposes `/healthz` on
`:8080` with a self-probing `HEALTHCHECK`, and the SQLite file lives on the
`/data` volume. See [docs/features.md](docs/features.md#liveness) for details.

## Test

```bash
go test ./...
```

## Roadmap

See [roadmap.md](roadmap.md).
