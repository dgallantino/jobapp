# jobapp

Personal, single-user job ad scraper and cover-letter generator. One Go binary, SQLite, htmx UI, systemd timers. Intended to run on a home machine over Tailscale.

## Commands

```bash
jobapp serve            # web UI (socket-activated or -listen)
jobapp crawl            # one crawl pass, then exit
jobapp telegram-check   # one Telegram short-poll pass, then exit
jobapp scrape-check URL # scrape one URL to stdout (no database)
```

## Build

For deploy, use the Makefile (output: `build/jobapp`):

```bash
make build
```

Quick local build:

```bash
go build -o jobapp ./cmd/jobapp
```

No npm/node. htmx is vendored under `internal/web/static/htmx.min.js`.

## Local development

```bash
./scripts/configure.sh
# fill remaining secrets in .env (OpenRouter, Telegram, etc.)

# Or manually: cp .env.example .env, then:
#   go run ./scripts/hashpassword YOUR_PASSWORD
#   openssl rand -hex 32

set -a && source .env && set +a
./jobapp serve -listen :8080 -db ./jobs.db
# Optional: exit after idle (default off). Production unit uses -idle-timeout 30m.
# ./jobapp serve -listen :8080 -db ./jobs.db -idle-timeout 30m
```

Open `http://127.0.0.1:8080`, sign in, add crawl sources under **Sources**, then:

```bash
./jobapp crawl -db ./jobs.db
```

## Deploy (user domain)

No root. Binary in Go’s bin dir, symlink on the user `PATH`, data and config under XDG, units under `systemctl --user`.

```
$GOBIN/jobapp                          # real binary (go env GOBIN, else $GOPATH/bin)
~/.local/bin/jobapp                    # symlink (created if needed)
~/.local/share/jobapp/jobs.db          # SQLite (created on first run)
~/.config/jobapp/.env                  # secrets (mode 600)
~/.config/systemd/user/jobapp*.{service,socket,timer}
```

Paths are detected by `./scripts/configure.sh` (env vars first: `GOBIN`, `XDG_BIN_HOME`, `XDG_DATA_HOME`, `XDG_CONFIG_HOME`) and written to `.install-paths` for the Makefile.

### Automatic install

```bash
./scripts/configure.sh          # creates ./.env (mode 600); detects install paths; prompts for site password
# edit ./.env — add OpenRouter / Telegram secrets

make build
make install                    # binary, symlink, ~/.config/jobapp/.env, user units + daemon-reload
make enable                     # jobapp.socket + crawl/telegram timers
```

If you still have a database from the old `/opt/jobapp` install:

```bash
mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/jobapp"
cp /opt/jobapp/jobs.db "${XDG_DATA_HOME:-$HOME/.local/share}/jobapp/jobs.db"
```

| Step | Effect |
|------|--------|
| `configure.sh` | Copies `.env.example` → `.env`, sets `JOBAPP_SESSION_SECRET`, `JOBAPP_PASSWORD_HASH`, and `JOBAPP_DB`; writes `.install-paths` |
| `make build` | Builds `build/jobapp` (incremental via timestamps) |
| `make install` | Installs the binary to the Go bin dir, symlinks into the user bin, copies `.env` to `$XDG_CONFIG_HOME/jobapp/.env`, generates user systemd units, runs `systemctl --user daemon-reload` |
| `make enable` | `systemctl --user enable --now` for socket + both timers |

`configure.sh --paths-only` refreshes `.install-paths` without touching `.env`. `configure.sh` also accepts a path — e.g. `./scripts/configure.sh ~/.config/jobapp/.env` — if you want secrets created directly at the install location.

Timers and the socket stop after logout unless lingering is on: `loginctl enable-linger $USER`.

Other Makefile targets: `make disable`, `make uninstall`, `make clean`. Uninstall leaves `.env` and `jobs.db` in place.

### Manual install

If you are not using the Makefile:

1. Build and copy the binary to `$(go env GOBIN)` or `$(go env GOPATH)/bin`.
2. Symlink it into `~/.local/bin` (or `$XDG_BIN_HOME`).
3. Copy `.env.example` → `~/.config/jobapp/.env` and fill secrets; set `JOBAPP_DB` to `~/.local/share/jobapp/jobs.db`.
4. Substitute `@JOBAPP_BIN@`, `@JOBAPP_DATADIR@`, and `@JOBAPP_ENV@` in `systemd/*.service.in`, then copy units into `~/.config/systemd/user/`.
5. Enable:

```bash
systemctl --user daemon-reload
systemctl --user enable --now jobapp.socket
systemctl --user enable --now jobapp-crawl.timer
systemctl --user enable --now jobapp-telegram.timer
```

Socket activation: systemd listens on the port in `jobapp.socket`; `jobapp serve` receives the listener via fd 3 (`github.com/coreos/go-systemd/v22/activation`). For local testing without systemd, use `-listen :PORT`.

The `jobapp.service` unit passes `-idle-timeout 30m`: after thirty minutes with no HTTP activity the process drains requests, closes SQLite, and exits. The next connection socket-activates a new process; sessions are process-scoped so you must sign in again.

Prefer binding the socket to a Tailscale IP so the UI is not on the public internet.

**Containers:** not required. Glints listing crawl needs a host Chromium/Chrome install for `chromedp`; if you containerize the browser, use **Podman** (not Docker).

## Environment variables

See [`.env.example`](.env.example). Summary:

| Variable | Purpose |
|----------|---------|
| `JOBAPP_DB` | SQLite path (cwd `jobs.db` if unset; configure/install sets `~/.local/share/jobapp/jobs.db`) |
| `JOBAPP_LISTEN` | Dev listen address for `serve` |
| `JOBAPP_PASSWORD_HASH` | bcrypt hash of site password |
| `JOBAPP_SESSION_SECRET` | HMAC key for session cookie (random at deploy) |
| `JOBAPP_SCRAPE_CONCURRENCY` | Max concurrent detail fetches per listing scrape (default `5`) |
| `JOBAPP_CHROME_PATH` | Chromium/Chrome binary for Glints listing chromedp (optional; PATH lookup if empty) |
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
- `jobstreet` — SSR HTML listing cards with `rel=next` pagination (cap 100) + concurrent detail enrichment (`data-automation` selectors)
- `glints` — chromedp renders explore listings, scrolls until 100 jobs / login nudge / card-count stall (anonymous ceiling ~30), then HTTP + goquery detail enrichment (`textWithBreaks`). Host Chromium/Chrome must be on `PATH` for crawl sources using this adapter. Detail / telegram-check URLs do not need chromedp.
- `dealls` — SSR `__NEXT_DATA__` page 1 + anonymous `api.sejutacita.id` explore pages until 100, then concurrent detail enrichment (Deskripsi Pekerjaan + Kualifikasi via `textWithBreaks`). No chromedp. Example source URL: `https://dealls.com/?searchJob=developer`.
- `kalibrr` — anonymous `/kjs/job_board/search` pagination (same API as “Load more jobs”, cap 100; stop at first `count`) mapped directly from API JSON; detail URLs parse `__NEXT_DATA__` job. No chromedp. Example source URL: `https://www.kalibrr.id/id-ID/home/te/developer`.

## Assumptions / stubs (flagged in code)

- Profile is a flexible key/value table (`full_name`, `summary`, `work_history`, `skills`, `tone_preferences`).
- Cover-letter prompt wording is isolated in `buildPrompt` and still TODO for tuning.
- Telegram multi-link messages: process all links, one reply after (TODO to confirm).
- Telegram scrape failures get a distinct reply (`Couldn't process ⚠️`) rather than silence.
- Telegram-ingested ads use a `sources` row with `adapter=telegram`.

## Schema

SQL lives in [`internal/db/schema.sql`](internal/db/schema.sql) (embedded). A copy is kept at [`schema/schema.sql`](schema/schema.sql) for reference — keep them in sync when editing.
