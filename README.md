# neverskip-sync

Background Go service that polls the Neverskip parent portal and:

- pushes new lounge + dailynotice items to [ntfy.sh](https://ntfy.sh) as
  iOS push notifications;
- serves an ICS calendar feed at `/school/calendar.ics?token=...` for iOS,
  macOS, and Google Calendar to subscribe to.

Design rationale and full architecture live in
[`neverskip-sync-implementation-plan.md`](neverskip-sync-implementation-plan.md).
This README covers what's actually built and how the pieces fit together.

**Looking for a step-by-step install guide?** See
[`SETUP.md`](SETUP.md) — it walks through the whole thing from "log in to
Chrome" through "iPhone is buzzing with school notifications", end to end.

## What changed vs the plan

The plan assumed login = mobile + password. Reality: Neverskip OTPs every
login, but it issues a long-lived `token` cookie (~11 months) that the Angular
SPA re-sends as an `token` header on every API call to `nskapi.neverskip.com`.
This service does the same — minus the browser. No mobile, no password, no
session juggling.

Operational cost: roughly once a year you re-pair the token. The service
pushes a notification via ntfy when re-auth is needed.

## API surface (what we found)

| | |
|---|---|
| API host | `https://nskapi.neverskip.com` |
| Auth | single header `token: <value>` (same value as the `token` cookie on `parent.neverskip.com`) |
| Lounge | `POST /parentweb/connect/fetchloungeinfo` body `{"values":"","page":"","filter_date":"0"}` |
| Daily notice | `POST /parentweb/connect/fetchdailynoticeinfo` body `{"values":"","page":"","filter_date":"0"}` |
| Auth probe | `POST /parentweb/auth/hasauth` body `{}` |

All endpoints return `{"S":bool, "D":..., "F":"S"}`. `S=false` means the token
was rejected.

## Local development

Prereqs:

- Go 1.25+
- Python 3.10+ (only for the `extract_token.py` helper)
- ntfy app on your phone, subscribed to a topic name only you know
- You're logged in to `parent.neverskip.com` in Chrome on this machine

One-time setup of the Python helper venv (used by `make token-env`):

```bash
python3 -m venv .venv
.venv/bin/pip install browser_cookie3 requests
```

Create a `.env` file (not committed):

```bash
make token-env >> .env
echo "NTFY_TOPIC=$(openssl rand -hex 16)" >> .env  # remember this — it goes into the iOS app
echo "ICS_TOKEN=$(openssl rand -hex 32)"  >> .env  # remember this — it goes into the calendar URL
echo "POLL_INTERVAL=15m" >> .env
echo "SQLITE_PATH=./state.db" >> .env
echo "LOG_LEVEL=info" >> .env
```

Subscribe to your `NTFY_TOPIC` value in the ntfy iOS app (App Store → ntfy →
Add subscription).

Smoke test — one tick, no HTTP server, no daemon:

```bash
make run-once
```

The first run is a **bootstrap**: every existing item gets recorded in SQLite
without notifications, so you don't get push-spammed with a year of history.
Subsequent runs notify only newly-discovered items.

Run the full service locally:

```bash
make build && SQLITE_PATH=./state.db bin/neverskip-sync   # plus envs from .env
```

## Configuration

All via env vars; loaded once at startup. Required:

- `NEVERSKIP_TOKEN` — value of the `token` cookie from your logged-in Chrome
- `NTFY_TOPIC` — long random string (anyone who guesses it can spam your phone)

Optional:

- `ICS_TOKEN` — query-param secret for the calendar feed. **Unset disables the
  endpoint** (returns 501) — handy in pure-Phase-1 setups.
- `CALENDAR_HOST` — default `spectretrade.in`; used in VEVENT UIDs to keep
  them globally unique
- `NTFY_URL` — default `https://ntfy.sh`
- `SQLITE_PATH` — default `/var/lib/neverskip-sync/state.db`
- `POLL_INTERVAL` — default `15m`, minimum `1m`
- `LISTEN_ADDR` — default `127.0.0.1:8080`
- `LOG_LEVEL` — `debug` | `info` (default) | `warn` | `error`
- `QUIET_HOURS` — `true` to skip polls between 23:00–06:00 IST

## HTTP endpoints

The HTTP server is internal-only (`127.0.0.1`); nginx publishes them under
`/school/...` once deployed.

- `GET /school/healthz` — returns `ok`. For monitoring.
- `GET /school/debug/recent` — last 20 seen items as JSON.
- `GET /school/calendar.ics?token=<ICS_TOKEN>` — the ICS feed. iOS Calendar,
  macOS Calendar, Google Calendar can all subscribe. Returns 501 if
  `ICS_TOKEN` is unset, 401 on wrong/missing token, 304 on conditional GET
  matching the previous ETag, 200 with `text/calendar` otherwise.

The calendar caches its rendered output in memory for 60 seconds, and the
poll loop invalidates the cache the moment a new item is inserted — so a
fresh notice shows up on your phone's calendar within one fetch cycle after
the poll discovers it.

## Re-pairing the token (when ntfy alerts you)

When the cookie expires or you log out of Chrome, the service starts seeing
401s and pushes a notification titled **"Neverskip token expired"**. To
recover:

1. Log in to `parent.neverskip.com` in Chrome on the laptop. Complete OTP.
2. `make token-env >> .env` (or pipe its output into your deploy env file on
   the server).
3. Restart the service (`systemctl restart neverskip-sync` once deployed).

## Project layout

```
cmd/server/        wiring, lifecycle, http.Server, flags
internal/
  config/          env loading + validation
  neverskip/       API client + response types, fixture-based tests
  parser/          turn raw responses into uniform state.Item
  state/           SQLite via modernc.org/sqlite (no CGO)
  notifier/        ntfy.sh POSTs
  calendar/        ICS feed + token check + 60s cache + ETag/304
  poll/            the loop: fetch → parse → dedup → notify + invalidate cal
systemd/
  neverskip-sync.service       drop-in unit (hardened, runs as `neverskip` user)
  neverskip-sync.env.example   template for /etc/neverskip-sync.env
nginx/
  neverskip-sync.conf          location /school/ → 127.0.0.1:8080
scripts/
  extract_token.py     read the token cookie from local Chrome
  probe_auth.py        one-off curl-style probe
  capture_api.py       playwright-based API discovery (one-off, not used at runtime)
```

## Tests

```bash
make test
```

The `parser` and `neverskip` packages have tests that run against real
(sanitised) fixtures committed under `internal/neverskip/testdata/`. The
fixtures are personal-info-scrubbed copies of live responses from the API.

## Deployment (Spectre box)

One-time setup on the server:

```bash
sudo useradd --system --home /var/lib/neverskip-sync --shell /usr/sbin/nologin neverskip
sudo install -d -o neverskip -g neverskip /var/lib/neverskip-sync
sudo install -d -o root      -g root      /opt/neverskip-sync/bin
```

Build and ship the binary from the laptop:

```bash
make build-linux
scp bin/neverskip-sync.linux-amd64 spectre:/tmp/server
ssh spectre 'sudo install -m 0755 /tmp/server /opt/neverskip-sync/bin/server && rm /tmp/server'
```

Drop in the systemd unit + nginx fragment:

```bash
scp systemd/neverskip-sync.service spectre:/tmp/
scp systemd/neverskip-sync.env.example spectre:/tmp/
scp nginx/neverskip-sync.conf spectre:/tmp/
ssh spectre 'sudo install -m 0644 /tmp/neverskip-sync.service /etc/systemd/system/'
ssh spectre 'sudo install -m 0600 -o neverskip -g neverskip /tmp/neverskip-sync.env.example /etc/neverskip-sync.env'
ssh spectre 'sudo $EDITOR /etc/neverskip-sync.env'   # fill in real values
ssh spectre 'sudo install -m 0644 /tmp/neverskip-sync.conf /etc/nginx/snippets/neverskip-sync.conf'
```

Include the snippet inside your existing `server {}` block for
`spectretrade.in`:

```nginx
include /etc/nginx/snippets/neverskip-sync.conf;
```

Then:

```bash
ssh spectre 'sudo nginx -t && sudo systemctl reload nginx'
ssh spectre 'sudo systemctl daemon-reload && sudo systemctl enable --now neverskip-sync'
ssh spectre 'systemctl status neverskip-sync; journalctl -u neverskip-sync --since=-2m -n 50'
```

Finally, on the iPhone: **Settings → Calendar → Accounts → Add Account →
Other → Add Subscribed Calendar**, paste
`https://spectretrade.in/school/calendar.ics?token=<ICS_TOKEN>`, refresh
frequency every 15 minutes. Done.

## What's intentionally not here

- PDF mirroring to MinIO (Phase 3)
- Deadline extraction from prose ("submit by Tuesday 12th May 2026") (Phase 3).
  Until then, calendar events use the post date as `DTSTART`, so the calendar
  reads as a chronological diary of school communication rather than a list
  of "things due on a date".
- Web dashboard at `/school/dashboard` (Phase 3)
- Best-effort retry/backoff on Neverskip 5xx — currently single attempt per
  tick. The next tick will retry. Good enough at 15m cadence.
