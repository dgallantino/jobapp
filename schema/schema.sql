-- Idempotent schema: applied on startup if tables do not exist.

CREATE TABLE IF NOT EXISTS sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    adapter TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS job_ads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER REFERENCES sources(id),
    source_url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    salary TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    posted_at DATETIME,
    scraped_at DATETIME NOT NULL DEFAULT (datetime('now')),
    status TEXT NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'applied', 'rejected', 'ignored'))
);

CREATE TABLE IF NOT EXISTS profile (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- Seed default profile keys (flexible key/value stub; exact fields undecided).
-- TODO: revisit whether work_history/skills need structured storage once real LLM usage shows what is needed.
INSERT OR IGNORE INTO profile (key, value) VALUES
    ('full_name', ''),
    ('summary', ''),
    ('work_history', ''),
    ('skills', ''),
    ('tone_preferences', '');

CREATE TABLE IF NOT EXISTS cover_letters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_ad_id INTEGER NOT NULL REFERENCES job_ads(id),
    content TEXT NOT NULL,
    model TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS telegram_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_update_id INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO telegram_state (id, last_update_id) VALUES (1, 0);
