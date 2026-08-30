<p align="center">
  <img src="https://github.com/chrisgreg/boop/raw/main/docs/boop.png" width="160" alt="Boop logo" />

  <h1 align="center">Boop</h1>

<p align="center">
  <img src="https://img.shields.io/github/actions/workflow/status/chrisgreg/boop/ci.yml?branch=main" alt="CI" />
  <img src="https://img.shields.io/github/go-mod/go-version/chrisgreg/boop?filename=server%2Fgo.mod" alt="Go version" />
  <img src="https://img.shields.io/github/license/chrisgreg/boop" alt="License" />
</p>

A tiny, self-hosted notification inbox for developers. Something happened in one of your apps; Boop tells you on your phone.

One Go binary, one SQLite file, one Docker container. Pushes go straight from your server to Apple's APNs. There is no hosted relay, account system, or telemetry.

</p>

```bash
curl https://boop.example.com/api/v1/events \
  -H "Authorization: Bearer $BOOP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title": "Backup complete", "level": "success"}'
```

## Architecture

<p align="center">
  <img src="docs/architecture.png" width="900" alt="How Boop works: apps POST events to the Go server, which stores them in SQLite and pushes to APNs; the iOS app fetches full detail from the server; the web UI manages projects and shows the pairing QR." />
</p>

Your apps POST events with a project API key. The Go server redacts and stores them in SQLite, then pushes straight to Apple's APNs using your `.p8` key. The push carries only the title, body and event id; the iOS app fetches the full event from your server with its own device credential. The embedded web UI manages projects and devices and shows the pairing QR the phone scans. An interactive version lives in [`docs/architecture/index.html`](docs/architecture/index.html) (open it locally; the source is `boop.architecture.json`).

## What is in the box

| Part | Where |
| --- | --- |
| Go server (API, SQLite, APNs, embedded web UI) | `server/` |
| Web UI (Svelte, built into the binary) | `server/web/` |
| iOS app (SwiftUI, iOS 26, you build and sign it) + notification service extension | `ios/` — see [ios/README.md](ios/README.md) |
| Client libraries | separate repos — see [Integrations](#integrations) |
| Native desktop client | planned |

## Quick start (Docker)

```bash
git clone https://github.com/chrisgreg/boop && cd boop
cp .env.example .env          # optional: BOOP_BASE_URL and APNS_* values
mkdir -p data && chown 1000:1000 data   # Linux hosts only; the container runs as uid 1000
docker compose up -d --build
open http://localhost:8080
```

The first visit opens a setup wizard: server check, APNs, pairing, first project, test notification. APNs credentials are optional; without them events are stored and shown in the UI but pushes are skipped, and the settings page says so.

Data lives in `./data/boop.db`. Back up by copying that file (use `sqlite3 data/boop.db ".backup backup.db"` for a consistent copy while running). Back up your `.p8` key separately.

## Send an event

Create a project in the web UI and copy its API key (shown once). Then:

```bash
# minimum
curl http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer boop_proj_..." -H "Content-Type: application/json" \
  -d '{"title": "Deploy complete"}'

# rich
curl http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer boop_proj_..." -H "Content-Type: application/json" \
  -d '{
    "title": "KeyError", "body": "key :can_palette? not found",
    "level": "error", "source": "error_tracker", "fingerprint": "uini-keyerror",
    "data": {
      "exception": {"type": "KeyError", "message": "key :can_palette? not found"},
      "stacktrace": [{"file": "lib/uini_web/live/widget_settings_live.ex", "line": 49, "function": "handle_event/3", "in_app": true}],
      "tags": {"environment": "production"},
      "context": {"user_id": "123"}
    }
  }'
```

Levels: `info`, `success`, `warning`, `error`, `critical`. Anything in `data` is kept as-is (recognised sections such as `exception`, `stacktrace`, `tags`, `context` and `breadcrumbs` get a nicer rendering) after sensitive keys are redacted.

**Actions.** Up to three buttons that open a URL — on the notification itself (long-press or pull down) and in the event detail:

```bash
curl http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer boop_proj_..." -H "Content-Type: application/json" \
  -d '{"title": "Payment received", "body": "£19.99", "level": "success",
       "actions": [{"label": "Open in Stripe", "url": "https://dashboard.stripe.com/payments/pi_1"},
                   {"label": "Open order", "url": "myshop://orders/42"}]}'
```

Labels are 40 characters or fewer; URLs must be absolute (`https://…` or an app scheme). Tapping the notification body still opens the event in Boop.

**Grouping.** Send the same `fingerprint` for the same problem and the inbox (web and phone) shows one row per fingerprint — `KeyError ×47 · First seen 09:31 · Last seen 10:42` — that opens the individual occurrences. Toggle it with "Group repeats". `GET /api/v1/events?grouped=true` is the API behind it. Grouping only affects display; every occurrence is stored and pushed (use a [silence](#silences) to stop pushes).

**Copy for an agent.** On the phone, the share button on an event offers *Copy*, *Copy as Markdown* and *Share*. The Markdown version lays the event out as sections — exception, environment, stack trace, context, breadcrumbs, data, links — ready to paste into Claude, ChatGPT or whatever you use. *Share* hands it straight to the assistant's app without the clipboard.

From a shell script:

```bash
boop() { curl -fsS "$BOOP_URL/api/v1/events" -H "Authorization: Bearer $BOOP_API_KEY" \
  -H "Content-Type: application/json" -d "{\"title\": \"$1\", \"body\": \"${2:-}\", \"level\": \"${3:-info}\"}"; }
pg_dump mydb > backup.sql && boop "Backup complete" "" success || boop "Backup failed" "$(tail -1 backup.log)" error
```

From GitHub Actions:

```yaml
- name: Boop
  if: always()
  run: |
    curl -fsS "${{ secrets.BOOP_URL }}/api/v1/events" \
      -H "Authorization: Bearer ${{ secrets.BOOP_API_KEY }}" \
      -H "Content-Type: application/json" \
      -d '{"title": "${{ github.workflow }} ${{ job.status }}", "body": "${{ github.repository }}@${{ github.ref_name }}", "level": "${{ job.status == 'success' && 'success' || 'error' }}", "source": "github_actions"}'
```

## Integrations

Official clients live in their own repos. They all speak the same one endpoint (`POST /api/v1/events`), redact sensitive keys before sending, truncate rather than reject, retry only network errors and 5xx, and never crash the host application.

### Elixir — [`boop_ex`](https://github.com/chrisgreg/boop_ex)

```elixir
{:boop_ex, "~> 1.1"}
```

```elixir
config :boop_ex, url: System.fetch_env!("BOOP_URL"), api_key: System.fetch_env!("BOOP_API_KEY"), source: "my_app"

Boop.send("Deploy complete")
Boop.send(title: "Payment received", body: "£19.99", level: :success, data: %{customer_id: id})
Boop.send_async(title: "Cron finished")          # :ok immediately, never raises
Boop.send(title: "Deploy failed", level: :error, actions: [{"Open run", run_url}])   # button on the push
Boop.Event.exception(e, __STACKTRACE__, tags: %{env: "prod"})   # rich error data
```

`send/2` returns `{:ok, %{id, created_at}}` or `{:error, %Boop.Error{code: …}}`; `send_async/2` runs on a supervised task. Ships a `usage-rules.md` for AI agents.

### Elixir + ErrorTracker — [`boop_error_tracker`](https://github.com/chrisgreg/boop_error_tracker)

```elixir
{:error_tracker, "~> 0.9"}, {:boop_ex, "~> 1.1"}, {:boop_error_tracker, "~> 1.1"}
```

```elixir
config :boop_error_tracker, environment: config_env(), source: "my_app",
  error_tracker_url: "https://my-app.com/dev/errors"   # "Open in ErrorTracker" button on every push
```

Attaches to [ErrorTracker](https://github.com/elixir-error-tracker/error-tracker)'s telemetry events and pushes new errors and resolved-errors-that-came-back to your phone, with the exception, stacktrace (your frames highlighted), context and breadcrumbs. Optional per-occurrence pushes with per-error throttling; muted errors are never sent. It installs *next to* ErrorTracker and never touches its database or config.

### Node.js — [`@boop/node`](https://github.com/chrisgreg/boop-node)

```bash
pnpm add @boop/node
```

```ts
import boop, { Boop } from '@boop/node'   // default client reads BOOP_URL / BOOP_API_KEY

await boop.send('Deploy complete')
const client = new Boop({ url, apiKey, source: 'my_app' })
const result = await client.send({ title: 'Payment received', level: 'success', data: { customerId } })
if (!result.ok) console.warn(result.error.code)
client.sendAsync({ title: 'Cron finished' })            // fire and forget, never throws
client.exception(err, { tags: { env: 'prod' } })       // rich error data
```

TypeScript, ESM + CJS, zero runtime dependencies, Node 18+.

### Anything else

`curl` is a first-class client (see [Send an event](#send-an-event)), and [`integration-llms.md`](integration-llms.md) is a prompt you can hand to an LLM to generate a client for any other language that behaves like the ones above.

## API

All endpoints are under `/api/v1`. Errors are JSON: `{"error": "code", "message": "..."}`.

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/health` | none | `{"status":"ok"}` |
| POST | `/api/v1/events` | project key | Create event, returns `{id, created_at}` |
| GET | `/api/v1/events?project=&level=&source=&fingerprint=&since=&until=&silenced=&grouped=&before=&limit=` | device or none | List, newest first; `next_cursor` feeds `before`; `silenced=true\|false` filters; `grouped=true` collapses repeated fingerprints into one row with `group: {count, first_seen, last_seen}` |
| GET | `/api/v1/events/:id` | device or none | Full event |
| GET | `/api/v1/events/:id/deliveries` | device or none | Push attempts for an event |
| GET/POST | `/api/v1/projects` | admin | List / create (returns `api_key` once) |
| GET/PATCH/DELETE | `/api/v1/projects/:id` | admin | Manage |
| POST | `/api/v1/projects/:id/rotate-key` | admin | New key, old one stops working |
| POST | `/api/v1/pairing` | admin | One-time pairing token + QR payload (10 min, single use) |
| DELETE | `/api/v1/pairing/:id` | admin | Revoke |
| POST | `/api/v1/pairing/exchange` | none | `{token, name, platform}` → `{device, credential}` |
| POST | `/api/v1/devices` | device | Register APNs token `{device_token, name, app_bundle_id}` |
| PATCH/DELETE | `/api/v1/devices/:id` | device (self) or admin | Update / remove |
| GET | `/api/v1/devices` | admin | List paired devices |
| GET | `/api/v1/status` | admin | Health, APNs state, counts, last push |
| GET/PATCH | `/api/v1/settings` | admin | `retention_days`, `redact_keys`, `setup_completed`, `mcp_enabled` (read-only `mcp_token_set` says whether `BOOP_MCP_TOKEN` is configured) |
| GET/POST | `/api/v1/silences` | admin | Rules that stop matching events from being pushed: `{field: fingerprint\|title\|source, value, project_id?, note?}` |
| GET | `/api/v1/silences/:id` | admin | One rule |
| DELETE | `/api/v1/silences/:id` | admin | Remove a rule (already-silenced events keep their flag) |
| POST | `/api/v1/events/:id/unsilence` | admin | Clear the flag and push the event now |
| POST | `/api/v1/test` | admin | Create a test event and push it |
| POST | `/mcp` | MCP token, device, or admin | Read-only [MCP](#mcp-for-ai-agents) endpoint (Streamable HTTP); `404 mcp_disabled` when switched off in Settings |

Event fields on `POST /api/v1/events`: `title` (required), `body`, `level`, `source`, `type`, `external_id`, `fingerprint`, `occurred_at`, `data`, `actions` — see [`integration-llms.md`](integration-llms.md) for limits.

Credentials: project keys (`boop_proj_...`) can only create events; device credentials (`boop_dev_...`) can only read events and manage their own device. Only SHA-256 hashes are stored.

**Admin auth.** Set `BOOP_ADMIN_USER` and `BOOP_ADMIN_PASSWORD` and the web UI shows a sign-in screen; admin endpoints then need the session cookie it sets (`POST /api/v1/auth/login`) or HTTP Basic credentials (`curl -u user:pass …`). Sessions last 30 days and live in memory, so a restart signs everyone out. Leave both unset and everything is open — only do that behind your own proxy, Tailscale, or VPN. Either way, project and device credentials are refused on admin endpoints, so a leaked client secret never grants admin rights.

## MCP (for AI agents)

Boop speaks the [Model Context Protocol](https://modelcontextprotocol.io) at `/mcp` (Streamable HTTP), read-only. Point the agent you already use at it and ask things like *"what errors happened overnight?"*, *"show me critical events from Infra"*, *"what started failing after 14:00?"* or *"get the full context for the latest KeyError"*. There is no LLM inside Boop; it just serves structured context.

Tools: `list_projects`, `list_events` (filters, time window, `grouped`), `search_events`, `get_event` (full payload), `get_event_group` (every occurrence of a fingerprint).

Set `BOOP_MCP_TOKEN` (16+ characters) and use it as a bearer token. A device credential works too, and so does the admin login; with admin auth off and no token the endpoint is open, like the rest of the read API. Project keys are refused. **Settings → MCP** has a switch that turns the endpoint off entirely.

```bash
claude mcp add --transport http boop https://boop.example.com/mcp --header "Authorization: Bearer $BOOP_MCP_TOKEN"
```

Any client that supports Streamable HTTP with a custom header can connect the same way.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `BOOP_PORT` | `8080` | |
| `BOOP_BASE_URL` | request origin | Public URL your phone can reach; used in the pairing QR |
| `BOOP_DATABASE_PATH` | `/data/boop.db` | WAL mode, migrations applied on start |
| `BOOP_RETENTION_DAYS` | `90` | Days of history to keep; `0` = forever. When set it overrides the value saved in the web UI on every start; leave unset to manage it from Settings |
| `BOOP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `BOOP_ADMIN_USER` | | Web UI / admin API username; set together with the password |
| `BOOP_ADMIN_PASSWORD` | | 8+ characters. Unset = no login |
| `BOOP_MCP_TOKEN` | | Bearer token for the read-only [MCP endpoint](#mcp-for-ai-agents); 16+ characters |
| `APNS_TEAM_ID` | | Apple Developer team id |
| `APNS_KEY_ID` | | Id of the APNs auth key |
| `APNS_BUNDLE_ID` | | Bundle id of your Boop iOS build |
| `APNS_PRIVATE_KEY_PATH` | | Path to the mounted `.p8` (preferred) |
| `APNS_PRIVATE_KEY` | | Alternative: the `.p8` contents, as PEM text or base64 (`base64 -i key.p8 \| tr -d '\n'`) |
| `APNS_ENVIRONMENT` | `production` | `sandbox` for Xcode debug builds |

## Apple setup

1. In the Apple Developer portal, create an App identifier for your Boop iOS build and enable Push Notifications.
2. Under Keys, create an APNs authentication key. Download the `.p8` (only possible once). Note the Key id.
3. Note your Team id (top right of the portal).
4. Put the `.p8` at `./secrets/apns.p8`, uncomment the secrets volume in `docker-compose.yml`, set `APNS_PRIVATE_KEY_PATH=/run/secrets/apns.p8`, and fill the other `APNS_*` values in `.env`.
5. Restart: `docker compose up -d`. Settings should show APNs as configured.
6. Build the iOS app (`open ios/Boop.xcodeproj`, set your team and the same bundle id — see [ios/README.md](ios/README.md)), install it on your phone, open Devices → Pair iPhone, and scan the QR.
7. Settings → Send test notification.

## Web Push on iPhone

1. Deploy Boop at a public HTTPS URL and set `BOOP_BASE_URL` to it.
2. Open that URL in Safari on iOS 16.4 or newer and sign in.
3. Choose **Share → Add to Home Screen**.
4. Open Boop from its Home Screen icon and select **Enable notifications** in Settings.
5. Send a test notification. iOS presents it on the Lock Screen and in Notification Center according to the device's Focus settings.

Boop generates its VAPID keypair once and stores it in SQLite. Restarts keep existing subscriptions valid; restoring the database restores the same push identity. An expired browser subscription is removed automatically after a push service returns HTTP 404 or 410.

iOS Home Screen apps keep storage separate from Safari. If admin authentication is enabled, sign in again after opening the installed Home Screen app. Set `BOOP_BASE_URL` to the public HTTPS origin; when it is unset or not HTTPS, the VAPID subject falls back to `mailto:boop@localhost`.

## Deploying with Dokploy (or any compose host)

Use `docker-compose.dokploy.yml`, not `docker-compose.yml`. It swaps the `./data` bind mount for a named volume (the bind mount is created root-owned on the host, and the image runs as uid 1000, so SQLite cannot write `/data/boop.db`), joins the external `dokploy-network` so Traefik can route to it, and drops the published port so the server is reachable only through your HTTPS proxy.

1. New **Compose** application → your repo, compose path `docker-compose.dokploy.yml`.
2. **Environment** tab: `BOOP_BASE_URL`, `BOOP_ADMIN_USER`, `BOOP_ADMIN_PASSWORD`, `APNS_TEAM_ID`, `APNS_KEY_ID`, `APNS_BUNDLE_ID`, `APNS_ENVIRONMENT`. Dokploy writes these to a `.env` beside the compose file, which is what `env_file` picks up.
3. The `.p8` key, either:
   - `APNS_PRIVATE_KEY` = `base64 -i AuthKey_XXXXXX.p8 | tr -d '\n'` (one line, easiest). **Leave `APNS_PRIVATE_KEY_PATH` unset** — if it has a value it overrides the inline key, and the resulting missing-file error is the usual reason APNs looks unconfigured after a deploy. Copying `.env.example` wholesale is how it ends up set.
   - Or Dokploy **Mounts → File mount**: paste the `.p8` contents, mount path `/run/secrets/apns.p8`, and set `APNS_PRIVATE_KEY_PATH=/run/secrets/apns.p8`.
4. Add a domain with HTTPS in Dokploy pointing at port `8080`; deploy. Settings in the web UI should show **APNs · Configured**.

Editing the compose file in Dokploy's UI only works for **Raw** compose apps; when the source is Git, commit changes and redeploy.

## Pairing

The web UI generates a one-time token (10 minutes, single use, revocable) and shows it as a QR code containing:

```json
{"version": 1, "server": "https://boop.example.com", "token": "pair_..."}
```

The app posts the token to `/api/v1/pairing/exchange`, stores the returned device credential, registers for APNs, and posts its token to `/api/v1/devices`. Registering the same APNs token twice updates the existing device instead of creating a duplicate.

## Silences

Some events you want stored but not on your phone: a known flaky job, a noisy warning. On any event's page click **Silence events like this** and pick what to match — its fingerprint, its title, or its source — for that project or every project. Matching events still arrive in the inbox (marked *silenced*) and in the iOS app, but no push is sent. Filter the inbox to **Silenced only** to review them; a silenced event's page shows the rule that caught it with **Remove rule** and **Unsilence and push now**. Manage all rules under **Settings → Silences**. Fingerprint and source match exactly; title ignores case.

## Grouping and actions

Events that share a `fingerprint` within a project are shown as one row (`KeyError ×47 · First seen 09:31 · Last seen 10:42`) in the web inbox and on the phone; open it for the individual occurrences, or untick **Group repeats** to see every row. Events can also carry up to three `actions` (`{label, url}`) that appear as buttons on the push notification and in the event detail — open the deploy, the Stripe payment, the error in your tracker. See [Send an event](#send-an-event).

## Redaction

Values under these keys are replaced with `[REDACTED]` anywhere in `data` before storage: `password`, `password_confirmation`, `secret`, `token`, `access_token`, `refresh_token`, `api_key`, `authorization`, `cookie`, `set-cookie`, `private_key`. Matching is case-insensitive and treats `-` and `_` alike. Add your own keys in Settings.

## Development

```bash
cd server && BOOP_DATABASE_PATH=./data/boop.db go run ./cmd/boop   # API on :8080
cd server/web && npm install && npm run dev                         # UI on :5173, proxies /api
make test                                                           # Go + web tests
make build                                                          # bin/boop with the UI embedded
```

Requires Go 1.27 and Node 24 (see `.tool-versions`). The SQLite driver is pure Go, so `CGO_ENABLED=0` builds work everywhere.

## Changelog

See [CHANGELOG.md](CHANGELOG.md). Releases are tagged `vX.Y.Z`; the server, web UI and iOS app share the version.

## Licence

MIT.
