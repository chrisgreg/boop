CREATE TABLE web_push_configuration (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    keys_json  TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE web_push_subscriptions (
    id              TEXT PRIMARY KEY,
    endpoint        TEXT NOT NULL UNIQUE,
    p256dh           TEXT NOT NULL,
    auth             TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT 'Web app',
    user_agent       TEXT NOT NULL DEFAULT '',
    last_success_at  TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX web_push_subscriptions_updated_at ON web_push_subscriptions(updated_at);

-- Web Push has a different target lifecycle from APNs devices. Keeping its
-- attempts in a sibling table preserves existing APNs rows while retaining
-- delivery history after an expired browser subscription is removed.
CREATE TABLE web_push_deliveries (
    id              TEXT PRIMARY KEY,
    event_id        TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subscription_id TEXT REFERENCES web_push_subscriptions(id) ON DELETE SET NULL,
    target_name     TEXT NOT NULL,
    status          TEXT NOT NULL,
    message_id      TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    attempted_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
CREATE INDEX web_push_deliveries_event_id ON web_push_deliveries(event_id);
CREATE INDEX web_push_deliveries_attempted_at ON web_push_deliveries(attempted_at);
