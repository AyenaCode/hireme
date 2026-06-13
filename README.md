# hireme — Job Alert

A small, single-binary Go service that polls [JSearch](https://www.openwebninja.com/api/jsearch)
for new remote **DevOps / React Native** jobs, dedups them in SQLite, and pushes
new matches to a **Telegram** chat.

One process. One config file. No external cron, no database server, no app to install.

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

## Run locally.

```bash
cp .env.example .env        # fill in the three secrets
go build -o jobalert ./cmd/jobalert

# one cycle then exit — best for the first smoke test:
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
