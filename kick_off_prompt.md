# Build Prompt: Job Ad Scraper + Cover Letter Generator (Go)

You are building a personal, single-user job ad scraper and application-letter
generator in Go. Follow the spec below exactly. Where something is marked
`STUB` / `TODO`, implement a clearly documented placeholder rather than
guessing — do not invent behavior for unresolved decisions.

## Project summary

A self-hosted tool that:
1. Crawls configured job listing pages on a schedule and stores new job ads in SQLite.
2. Checks a specific Telegram group (via an existing bot account) on a schedule for messages containing job links, scrapes those links, and replies in-chat once processed.
3. Serves a small web UI (over Tailscale, single user) to browse ads, track their status, and generate a tailored cover letter per ad using an LLM.

Priorities, in order: **lean/lightweight, low operational overhead, fast cold start, minimal moving parts.** This is not a high-traffic app — traffic is one person, bursty, often over slow home internet.

---

## Tech stack (fixed — do not substitute)

- **Language**: Go (latest stable)
- **HTTP server**: `net/http` (stdlib only, no web framework)
- **Templating**: `html/template` (stdlib, auto-escaping — never disable escaping)
- **Frontend interactivity**: [htmx](https://htmx.org) — vendor the single JS file, no build step, no npm/node involved anywhere in this project
- **DB**: SQLite via `modernc.org/sqlite` (pure Go driver, no cgo) — WAL mode enabled
- **Scraping**: `goquery` (`github.com/PuerkitoBio/goquery`) as the primary HTML parser; `chromedp` (`github.com/chromedp/chromedp`) only where a specific site requires JS-rendered content
- **LLM**: OpenRouter.ai — OpenAI-compatible `/chat/completions` endpoint, called via plain `net/http`, no SDK
- **Embedding**: `go:embed` for HTML templates, static assets (CSS, htmx.js), and SQL schema/migration files

---

## Binary & deployment shape

Single binary, subcommand-driven:

```
jobapp serve            # runs the socket-activated web frontend
jobapp crawl            # runs one crawl pass over all configured sources, then exits
jobapp telegram-check   # runs one poll pass over the configured Telegram group, then exits
```

Deploy target:

```
/opt/jobapp/
├── jobapp          # single binary, everything else embedded
└── jobs.db         # runtime SQLite file (WAL mode) — created on first run if absent
```

Also produce two pairs of systemd unit files (as separate files in a `systemd/` directory in the repo, not installed automatically):

- `jobapp.socket` + `jobapp.service` — socket-activated frontend. Use `github.com/coreos/go-systemd/v22/activation` to receive the listener from systemd on fd 3 (`serve` must support both socket-activated mode and a plain `-listen :PORT` flag for local dev/testing).
- `jobapp-crawl.timer` + `jobapp-crawl.service` — runs `jobapp crawl` on a schedule. Default the timer to hourly; leave a comment showing how to change it.
- `jobapp-telegram.timer` + `jobapp-telegram.service` — runs `jobapp telegram-check` on a schedule. Default the timer to every 5 minutes; leave a comment showing how to change it. This is deliberately **not** a long-running `getUpdates` long-poll listener or webhook server — it's a short-lived process that runs, checks for new messages once, and exits, consistent with the "no long-living listener" requirement.

Secrets (site password, OpenRouter API key) are read from environment variables, documented in a `.env.example` file and referenced via `EnvironmentFile=` in the systemd service units. Never embed secrets in the binary or commit real values.

---

## Data model

Implement as SQL schema files embedded via `go:embed`, applied automatically on first run (simple "if tables don't exist, create them" migration — no migration framework needed at this scale).

### `sources`
Configured crawl targets — data-driven, not hardcoded per site.
| column | type | notes |
|---|---|---|
| id | INTEGER PK | |
| name | TEXT | e.g. "JobStreet - Backend Engineer SG" |
| url | TEXT | fixed listing/category URL to crawl |
| adapter | TEXT | which scraper adapter to use (see Scraper design) |
| enabled | BOOLEAN | default true |
| created_at | DATETIME | |

### `job_ads`
| column | type | notes |
|---|---|---|
| id | INTEGER PK | |
| source_id | INTEGER FK -> sources.id | |
| source_url | TEXT | the specific job ad URL |
| title | TEXT | |
| company | TEXT | |
| description | TEXT | raw extracted text of the ad |
| posted_at | DATETIME NULL | nullable — not all sites expose this cleanly |
| scraped_at | DATETIME | |
| status | TEXT | enum-like: `new`, `applied`, `rejected`, `ignored` — default `new` |
| UNIQUE(source_url) | | prevent duplicate inserts on re-crawl |

### `profile`
Single-row (or small key/value) table holding the user's info for cover letter generation, editable via the web UI. `STUB`: exact fields are undecided — implement as a flexible key/value table for now:

| column | type | notes |
|---|---|---|
| key | TEXT PK | e.g. `full_name`, `summary`, `work_history`, `skills`, `tone_preferences` |
| value | TEXT | free text; work_history/skills can be stored as plain text blocks for now |

`TODO`: revisit whether `work_history`/`skills` need to become structured (repeated rows / JSON) once real usage shows what the LLM prompt actually needs.

### `cover_letters`
| column | type | notes |
|---|---|---|
| id | INTEGER PK | |
| job_ad_id | INTEGER FK -> job_ads.id | |
| content | TEXT | generated letter |
| model | TEXT | OpenRouter model string used |
| created_at | DATETIME | |

### `telegram_state`
Single-row table tracking poll progress so `telegram-check` never needs a long-lived connection.
| column | type | notes |
|---|---|---|
| id | INTEGER PK | always row id = 1 |
| last_update_id | INTEGER | highest Telegram `update_id` processed so far; used as the `offset` on the next `getUpdates` call |
| updated_at | DATETIME | |

Job ads discovered via Telegram are inserted into the existing `job_ads` table like any other source — add a `sources` row with `adapter = "telegram"` (or leave `source_id` nullable and set `source_url` to the link found in the message; see `STUB` note below) so they show up in the same list/UI with no special-casing needed downstream.

---

## Telegram checker design

Purpose: periodically check one specific Telegram group (via an existing bot account already added to that group) for messages containing job links, scrape each link, store it as a `job_ads` row, and reply in-chat once processed. Triggered by a `systemd` timer — **not** a persistent `getUpdates` long-poll loop and **not** a webhook server, per the "no long-living listener" requirement.

### Flow (`jobapp telegram-check`)
1. Read `last_update_id` from `telegram_state` (default 0 if table empty).
2. Call Telegram Bot API `getUpdates` with `offset = last_update_id + 1` and a short/zero timeout (short-poll, not long-poll — the call returns immediately with whatever's pending, it does not block waiting for new messages). This is what makes it safe to run from a `systemd` timer instead of a listener.
3. Filter updates to messages from the configured group chat ID only (`TELEGRAM_CHAT_ID` env var — `STUB`: exact value must be filled in by the user; document how to find a group's chat ID, e.g. via `getUpdates` output or a helper bot, in the README).
4. Extract URLs from each matching message's text (simple regex URL extraction is enough — no need for a full HTML/markdown parser).
5. For each URL found: scrape it — reuse the existing adapter registry (`staticAdapter` as default, matching per-site adapters where applicable — same registry used by `crawl`, no separate scraping logic) and insert into `job_ads` (same `UNIQUE(source_url)` dedup behavior as `crawl`).
6. Reply to the originating message via `sendMessage` with `reply_to_message_id` set to the original message ID, with a short confirmation (e.g. "Processed ✅"). `STUB`: exact reply text/format not finalized — implement as a single constant/config so wording can change later.
7. Update `telegram_state.last_update_id` to the highest `update_id` seen this run, regardless of whether every message contained a usable link (so already-seen messages are never reprocessed).
8. Log a summary (messages checked, links found, links scraped, errors) to stdout for journald.

### Notes / stubs
- Bot token: `TELEGRAM_BOT_TOKEN` env var, same pattern as other secrets (never embedded, documented in `.env.example`).
- Group chat ID: `TELEGRAM_CHAT_ID` env var — `STUB`, must be supplied by the user; the bot must already be a member of that group.
- Messages with no URL: skip silently (no reply), only reply when a link was actually found and scraped.
- Messages with multiple URLs: `STUB` — current spec doesn't say. Default to processing all links found in the message and sending one reply after all are done, but leave a `// TODO: confirm desired behavior for multi-link messages` comment since this wasn't explicitly decided.
- Scrape failures (bad/dead link, unsupported site): `STUB` — default to replying with a distinct failure message (e.g. "Couldn't process ⚠️") rather than staying silent, so the user isn't left wondering if the bot saw the message at all. Flag this default clearly as an assumption.

---

## Scraper design

Implement a per-site adapter pattern so sites can be added/changed independently:

```go
type Adapter interface {
    // Name must match the `adapter` column value in `sources`.
    Name() string
    // Scrape fetches the listing page at url and returns discovered job ads.
    Scrape(ctx context.Context, url string) ([]JobAd, error)
}
```

Implement:
- `staticAdapter` — uses `net/http` + `goquery`. This is the default/fallback adapter for plain server-rendered listing pages.
- `jobstreetAdapter` and `glintsAdapter` — `STUB`. Before writing real selectors, inspect whether either site exposes an underlying JSON/XHR endpoint that the listing page calls client-side (very common — check Network tab for `/api/` or `/graphql` calls returning JSON job data). Document findings as comments at the top of each adapter file. If no JSON endpoint is found and content is genuinely only available after JS execution, fall back to `chromedp` for that adapter specifically — do not add `chromedp` as a blanket dependency for sites that don't need it.
- A registry mapping `adapter` name (string) to `Adapter` implementation, used by the crawl command to dispatch per `sources` row.

The `crawl` command: load enabled `sources`, dispatch to the right adapter, upsert results into `job_ads` (skip on `UNIQUE(source_url)` conflict), log a summary (new ads found, errors per source) to stdout (journald will capture it via systemd).

---

## Frontend (web UI)

Pages, server-rendered with `html/template`, htmx for partial updates (no full page reloads for status changes / letter generation):

1. **Login** — single password field, checked against a hashed password from env var (`bcrypt`), sets a signed session cookie on success. `STUB`: session secret/cookie signing key — read from env var, document that a random one should be generated at deploy time.
2. **Job list** — table/list of `job_ads`, filterable by `status`, newest first. Each row: title, company, source, status (as an htmx-updatable dropdown/buttons that PATCH status without full reload), link to detail view.
3. **Job detail** — full ad text, status control, "Generate cover letter" button (htmx POST, swaps in the result without reload), history of previously generated letters for this ad if any.
4. **Profile / settings** — form to view/edit the `profile` key/value fields. `STUB`: exact form layout undecided since profile fields themselves are still a stub — build a simple generic form (label + textarea per key) that works whatever fields end up in the table.
5. **Sources / settings** — simple CRUD list for the `sources` table (add/edit/disable a crawl target), since these are configured by the user, not hardcoded.

Auth middleware: every route except `/login` requires a valid session cookie.

---

## LLM integration (OpenRouter)

- Plain `net/http` POST to `https://openrouter.ai/api/v1/chat/completions`, OpenAI-compatible body (`model`, `messages`).
- API key from `OPENROUTER_API_KEY` env var.
- Model string configurable via env var (`OPENROUTER_MODEL`), not hardcoded, since OpenRouter supports many models.
- Prompt construction: system/user message combining the `profile` data + the selected `job_ads.description`. `STUB`: exact prompt wording/structure is not finalized — implement a clearly isolated `buildPrompt(profile, jobAd) string` function so it can be iterated on without touching the HTTP call logic. Leave a `// TODO: tune prompt wording once we see real output quality` comment.
- Store the result in `cover_letters` linked to the job ad.

---

## Explicitly out of scope / not to build

- No user accounts / multi-user support (single password gate only)
- No JS build pipeline, no npm, no bundler — htmx is the only JS, vendored as a static file
- No migration framework — schema is applied idempotently on startup
- No keyword-search-based crawling — only fixed configured URLs
- No persistent Telegram listener, no webhook server — `telegram-check` is a short-lived, timer-triggered process only
- No handling of multiple Telegram groups/chats — single configured group only

---

## Suggested build order

1. Project scaffold: `go.mod`, subcommand dispatch (`serve`/`crawl`), `go:embed` layout for templates/static/schema
2. SQLite schema + connection setup (WAL mode), applied on startup
3. `sources` + `job_ads` models and basic CRUD
4. `staticAdapter` scraper + `crawl` command working end-to-end against one real static source
5. Web UI: login, job list, job detail (no LLM yet)
6. Profile page (stub key/value form)
7. OpenRouter integration + cover letter generation wired into job detail page
8. JobStreet/Glints adapters (investigate JSON endpoints first, `chromedp` fallback only if needed)
9. Telegram checker: `telegram_state` table, short-poll `getUpdates` call, URL extraction, reuse of the adapter registry, `sendMessage` reply
10. systemd unit files (including `jobapp-telegram.timer`/`.service`) + `.env.example` + README with deploy steps (including how to find a Telegram group's chat ID)

Flag clearly in code comments and in a final summary anywhere you had to make an assumption instead of following an explicit spec above.