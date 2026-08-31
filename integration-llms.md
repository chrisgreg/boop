# Building a Boop client

This file is a prompt. Point your LLM at it ("build me a Boop client for <language> following integration-llms.md") and it has everything it needs. It is also a readable spec for humans.

## What Boop is

Boop is a self-hosted notification inbox. Applications POST small JSON **events** to a Boop server over HTTPS; the server stores them and pushes a notification to the owner's iPhone. A client library's only job is to send events reliably and get out of the way. Boop is not an error tracker, a logger, or a queue.

There is exactly one endpoint a client needs: `POST /api/v1/events`.

## Configuration a client must accept

| Setting | Env var (conventional) | Notes |
| --- | --- | --- |
| Server URL | `BOOP_URL` | e.g. `https://boop.example.com`. Strip a trailing slash. HTTPS in production; plain `http://` only for local development. |
| API key | `BOOP_API_KEY` | Starts with `boop_proj_`. One key per project; it can only create events for that project. Treat it as a secret: never log it. |
| Timeout | | Default 5 s connect + 10 s total is sensible. Must be configurable. |
| Enabled flag | | Optional but useful: lets test/dev environments no-op instead of hitting a server. |

Clients should read config from the host application's normal config system and fall back to the env vars above.

## Authentication

```
Authorization: Bearer boop_proj_...
Content-Type: application/json
```

No other auth. `401` means the key is missing or wrong. There is no key refresh; the user rotates keys in the web UI.

## The event

Send a JSON object. Only `title` is required.

| Field | Type | Rules |
| --- | --- | --- |
| `title` | string | Required. 1–200 characters. What happened, in a few words. |
| `body` | string | Optional, ≤ 4000 characters. Detail. Becomes the notification body. |
| `level` | string | One of `info` (default), `success`, `warning`, `error`, `critical`. Anything else is rejected. `critical` produces a prominent (time-sensitive) push. |
| `source` | string | Optional, ≤ 200. What produced the event: `"cron"`, `"error_tracker"`, `"github_actions"`, your library's name. Used for filtering. |
| `type` | string | Optional, ≤ 200. A category within the source, e.g. `"error"`, `"deploy"`. |
| `external_id` | string | Optional, ≤ 200. Your own id for the event (for cross-referencing). Not used for deduplication. |
| `fingerprint` | string | Optional, ≤ 200. A stable grouping key for "the same thing happening again". The inbox collapses events sharing a fingerprint into one row with a count and first/last seen, and silence rules can match on it. Every occurrence is still stored and pushed; it is not deduplication. |
| `occurred_at` | string | Optional RFC 3339 timestamp (`2026-08-28T12:51:44Z`). Defaults to receipt time. |
| `data` | object | Optional free-form JSON object, ≤ 256 KB. Anything you like; unknown keys are preserved verbatim. Arrays at the top level are rejected — it must be an object. |
| `actions` | array | Optional, at most 3 of `{"label": string, "url": string}`. Buttons on the notification and in the event detail that open the URL. Label 1–40 characters; URL absolute with a scheme (`https://…` or an app scheme like `myapp://…`; `javascript:`, `data:`, `file:` are rejected). A client should expose this as a simple list, e.g. `actions: [{label: "Open deploy", url: run_url}]`. |

### `data` conventions the UI renders specially

If present, these keys get a rich rendering on the web and in the iOS app. Use them when they fit; ignore them otherwise.

```json
{
  "exception":   { "type": "KeyError", "message": "key :x not found" },
  "stacktrace":  [ { "file": "lib/a.ex", "line": 49, "function": "A.b/3", "in_app": true } ],
  "tags":        { "environment": "production", "runtime": "elixir 1.19" },
  "context":     { "user_id": "123", "request": { "path": "/x", "method": "POST" } },
  "breadcrumbs": [ { "timestamp": "12:51:40", "category": "navigation", "message": "GET /sites/1" } ]
}
```

Stack frames: `file`, `line`, `function`, `in_app` (bool). Frames are shown in the order given (put the innermost frame first). Breadcrumbs: `timestamp`, `category`, `message`.

### Redaction

The server replaces the values of these keys anywhere inside `data` with `"[REDACTED]"` before storing: `password`, `password_confirmation`, `secret`, `token`, `access_token`, `refresh_token`, `api_key`, `authorization`, `cookie`, `set-cookie`, `private_key` (case-insensitive; `-` and `_` are equivalent), plus any the user configured. **Clients should still redact the same keys before sending** — the wire is the first place a secret can leak. Do not rely on the server alone.

## Request and response

```
POST {BOOP_URL}/api/v1/events
Authorization: Bearer {BOOP_API_KEY}
Content-Type: application/json

{"title": "Deploy complete", "body": "uini deployed", "level": "success", "source": "deploy"}
```

Success: `201 Created`

```json
{"id": "evt_w4zosnovd6huj6dh", "created_at": "2026-08-28T14:10:46.716098000Z"}
```

Errors are JSON with the same shape everywhere:

```json
{"error": "invalid", "message": "level must be one of info, success, warning, error, critical"}
```

| Status | `error` | Meaning | Client action |
| --- | --- | --- | --- |
| 400 | `invalid_json` | Body is not valid JSON | Bug in the client; do not retry |
| 401 | `unauthorized` | Missing or wrong API key | Do not retry; surface clearly |
| 413 | `too_large` | Body over 1 MB | Trim `data`; do not retry |
| 422 | `invalid` | Validation failed (see `message`) | Do not retry |
| 5xx | `internal` | Server fault | Retry with backoff |
| — | (network error / timeout) | Server unreachable | Retry with backoff |

Delivery happens asynchronously after the 201; the response does not tell you whether a phone or a project-configured webhook received it. The user may have silence rules (by fingerprint, title, or source) that store an event without delivering it — a stable `fingerprint` per distinct problem is what makes those rules useful, so send one when you can.

## Behaviour a good client has

1. **Never crash or block the host application.** Sending a notification is a side effect; a failure to send must degrade to a returned error (or a log line for the async variant), never an exception that takes the caller down. Catch everything.
2. **Sync and async sends.** `send(...)` returns `{ok, %{id, created_at}}` / `{error, reason}` (or the language's equivalent). `send_async(...)` fires and forgets on a background task/thread/pool and never raises. Make the async variant the one you'd recommend for hot paths.
3. **Retries: small and bounded.** Retry only network errors and 5xx, at most 2–3 attempts with jittered backoff (e.g. 200 ms, 800 ms). Never retry 4xx.
4. **Timeouts on everything.** No unbounded waits.
5. **Redact before transmit** (list above). Provide a way to add keys.
6. **Truncate rather than fail** when a string exceeds its limit (title 200, body 4000); when `data` is over 256 KB, drop the largest values or the whole `data` and note it in `body`, rather than dropping the event.
7. **Ergonomic levels.** Accept the language's natural form (`:success`, `"success"`, an enum) and serialise to the string.
8. **Defaults that read well.** A one-argument call `send("Backup complete")` must work. Let callers set a default `source` once in config so every event is tagged with their app name.
9. **Do not log the API key or full event payloads** by default. Log ids and statuses.
10. **Idiomatic packaging.** Use the ecosystem's standard HTTP client, config, and test tooling. Ship a README with install, config, and three examples (minimum, rich, async).
11. **Tests.** Unit-test serialisation (levels, timestamps, redaction, truncation), and the HTTP layer against a stub server: 201 parsing, 401/422 errors surfaced without retry, 5xx retried, network failure returned as an error, async never raising.

## A reference shape (adapt to the language)

```
Boop.send("Backup complete")
Boop.send(title: "Payment received", body: "£19.99", level: :success, source: "polar",
          data: %{customer_id: "123", amount: 19.99, currency: "GBP"})
Boop.send(%Boop.Event{title: "Backup failed", level: :error, data: %{host: "db-01"}})
Boop.send_async(...)          # same arguments, returns immediately, never raises
Boop.Event.new(...)           # build/validate without sending
```

Configuration in the host app, with env fallback:

```
config :boop,
  url: System.fetch_env!("BOOP_URL"),
  api_key: System.fetch_env!("BOOP_API_KEY"),
  source: "uini",            # optional default for every event
  timeout: 10_000,           # ms
  enabled: config_env() == :prod
```

## Other endpoints (not needed for a sender)

Project keys can only call `POST /api/v1/events`. Reading events, managing projects and devices, and pairing phones are done in the web UI or by the iOS app with their own credentials. Do not build those into a sending client.

`GET /health` (no auth) returns `{"status":"ok"}` and is fine to use for a "is Boop reachable" check.

## Quick check with curl

```bash
curl -i "$BOOP_URL/api/v1/events" \
  -H "Authorization: Bearer $BOOP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title": "Hello from curl", "level": "info", "source": "curl"}'
```

Expect `HTTP/1.1 201 Created` and the event in the Boop inbox. Every client you build should be able to reproduce exactly this request.
