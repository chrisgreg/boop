CREATE TABLE webhooks (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    url           TEXT NOT NULL,
    payload_mode  TEXT NOT NULL DEFAULT 'json',
    body_template TEXT NOT NULL DEFAULT '',
    headers_json  TEXT NOT NULL DEFAULT '{}',
    min_level     TEXT NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX webhooks_project_id ON webhooks(project_id);

CREATE TABLE deliveries_new (
    id           TEXT PRIMARY KEY,
    event_id     TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    device_id    TEXT REFERENCES devices(id) ON DELETE CASCADE,
    target_type  TEXT NOT NULL DEFAULT 'device',
    webhook_id   TEXT REFERENCES webhooks(id) ON DELETE SET NULL,
    webhook_host TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    apns_id      TEXT NOT NULL DEFAULT '',
    http_status  INTEGER,
    error        TEXT NOT NULL DEFAULT '',
    attempted_at TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
INSERT INTO deliveries_new (id, event_id, device_id, target_type, status, apns_id, error, attempted_at, created_at)
SELECT id, event_id, device_id, 'device', status, apns_id, error, attempted_at, created_at FROM deliveries;
DROP TABLE deliveries;
ALTER TABLE deliveries_new RENAME TO deliveries;
CREATE INDEX deliveries_event_id ON deliveries(event_id);
CREATE INDEX deliveries_attempted_at ON deliveries(attempted_at);
CREATE INDEX deliveries_webhook_id ON deliveries(webhook_id);
