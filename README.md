# jobapp

Personal, single-user job ad scraper and cover-letter generator. One Go binary, SQLite, htmx UI, systemd timers. Intended to run on a home machine over Tailscale.

## Commands

```bash
jobapp serve            # web UI (socket-activated or -listen)
jobapp crawl            # one crawl pass, then exit
jobapp telegram-check   # one Telegram short-poll pass, then exit
```

## Build

```bash
go build -o jobapp ./cmd/jobapp
```

No npm/node. htmx is vendored under `internal/web/static/htmx.min.js`.

## Local development

```bash
cp .env.example .env
# fill JOBAPP_PASSWORD_HASH, JOBAPP_SESSION_SECRET (and optionally OpenRouter/Telegram)

# Generate a bcrypt password hash (example):
go run ./scripts/hashpassword YOUR_PASSWORD

# Generate a session secret:
openssl rand -hex 32

set -a && source .env && set +a
./jobapp serve -listen :8080 -db ./jobs.db
```

Open `http://127.0.0.1:8080`, sign in, add crawl sources under **Sources**, then:

```bash
./jobapp crawl -db ./jobs.db
```

## Deploy (`/opt/jobapp`)

```
/opt/jobapp/
├── jobapp      # binary
├── jobs.db     # created on first run
└── .env        # secrets (mode 600)
```

1. Build and copy the binary to `/opt/jobapp/jobapp`.
2. Copy `.env.example` → `/opt/jobapp/.env` and fill secrets.
3. Copy unit files from `systemd/` into `/etc/systemd/system/`.
4. Enable:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now jobapp.socket
sudo systemctl enable --now jobapp-crawl.timer
sudo systemctl enable --now jobapp-telegram.timer
```

Socket activation: systemd listens on the port in `jobapp.socket`; `jobapp serve` receives the listener via fd 3 (`github.com/coreos/go-systemd/v22/activation`). For local testing without systemd, use `-listen :PORT`.

Prefer binding the socket to a Tailscale IP so the UI is not on the public internet.

**Containers:** not required. If a future JobStreet/Glints adapter needs Chromium for `chromedp`, use a host Chromium install; if you containerize the browser, use **Podman** (not Docker).

## Environment variables

See [`.env.example`](.env.example). Summary:

| Variable | Purpose |
|----------|---------|
| `JOBAPP_DB` | SQLite path (default `jobs.db`) |
| `JOBAPP_LISTEN` | Dev listen address for `serve` |
| `JOBAPP_PASSWORD_HASH` | bcrypt hash of site password |
| `JOBAPP_SESSION_SECRET` | HMAC key for session cookie (random at deploy) |
| `OPENROUTER_API_KEY` / `OPENROUTER_MODEL` | Cover letter generation |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | Group short-poll checker |

## Finding a Telegram group chat ID

`TELEGRAM_CHAT_ID` is a stub you must fill in. The bot must already be a member of the group.

1. Add the bot to the group and send a message mentioning it (or any message the bot can see).
2. Call `getUpdates` once:

```bash
curl "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getUpdates" | jq .
```

3. Read `message.chat.id` for the group (typically a negative number like `-100…`).
4. Put that value in `TELEGRAM_CHAT_ID`.

You can also use a helper bot such as `@userinfobot` / `@getidsbot` in the group if you prefer.

`jobapp telegram-check` uses **short-poll** `getUpdates` with timeout 0 (not a long-lived listener or webhook), safe for a systemd timer (default every 5 minutes).

## Scrapers

Adapters are data-driven via the `sources.adapter` column:

- `static` — `net/http` + `goquery` (default)
- `jobstreet` / `glints` — stubs that fall back to `static` until JSON endpoints or selectors are confirmed (see comments in those files). `chromedp` is only for adapters that truly need JS.

## Assumptions / stubs (flagged in code)

- Profile is a flexible key/value table (`full_name`, `summary`, `work_history`, `skills`, `tone_preferences`).
- Cover-letter prompt wording is isolated in `buildPrompt` and still TODO for tuning.
- Telegram multi-link messages: process all links, one reply after (TODO to confirm).
- Telegram scrape failures get a distinct reply (`Couldn't process ⚠️`) rather than silence.
- Telegram-ingested ads use a `sources` row with `adapter=telegram`.

## Schema

SQL lives in [`internal/db/schema.sql`](internal/db/schema.sql) (embedded). A copy is kept at [`schema/schema.sql`](schema/schema.sql) for reference — keep them in sync when editing.
