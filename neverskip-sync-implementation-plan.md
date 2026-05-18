# Neverskip Sync — Implementation Plan

## 0. Status (2026-05-14)

- **Phase 1 — done.** Lounge + dailynotice polled, items deduped via SQLite,
  pushes fired to ntfy. Live smoke test: 15 items bootstrapped, no spurious
  notifications, fixture-based parser tests green.
- **Phase 2 — done.** ICS feed served at `/school/calendar.ics?token=...`,
  60-second in-memory cache with explicit invalidation from the poll loop,
  ETag + 304 conditional GET, token check via `subtle.ConstantTimeCompare`.
  systemd unit and nginx fragment committed under `systemd/` and `nginx/`.
- **Phase 3 — not started.** Deadline extraction from prose, PDF mirroring,
  web dashboard.

### What turned out different from the plan

Auth is **dramatically simpler** than §6 originally assumed. There is no
mobile + password login flow to replicate. The Neverskip Angular SPA at
`parent.neverskip.com` is a thin shell; the real API lives at
`https://nskapi.neverskip.com`, and every call carries a single HTTP header
`token: <value>`. That value is the same as the `token` cookie set on
`parent.neverskip.com` after OTP login, which lives for ~11 months. The SPA
reads its own cookie via JS and re-sends it as a header because cross-domain
cookies don't auto-attach. The Go service does the same — minus the browser.

Operational implication: re-pairing is roughly an annual chore (or whenever
you log out of Chrome), not a per-session concern. ntfy alerts when the token
is rejected.

The original §16 blockers (capture the login request, capture dailynotice
JSON) were resolved by **driving headless Chrome via Playwright** through the
already-logged-in Chrome profile, recording every API call, and reverse-
engineering the actual endpoints from the captured traffic. See
`scripts/capture_api.py`.

## 1. Problem statement

The Neverskip parent app surfaces newsletters, events, daily notices, and homework only inside the app, and only via push notifications that are easy to miss. There is no email digest, no calendar export, no public API. Tracking what's coming up means opening the app and scrolling.

The goal is to replace that workflow with a system that pushes new items to my phone the moment they appear and adds dated events to the calendars I already use across iPad, iPhone, and laptop. I should never have to open the Neverskip app to know what's happening, unless I want to read details.

## 2. Solution shape

One Go web service. No mobile app to build. No UI of my own.

The service runs on a small Linux server reachable at a public HTTPS domain (TBD; referred to below as `<your-domain>`). On a schedule, it logs into `parent.neverskip.com`, fetches the lounge and dailynotice JSON endpoints, detects items it has not seen before, and fans them out two ways:

1. A push notification via [ntfy.sh](https://ntfy.sh) — installed once on my iPhone, free, no account, pushes arrive in seconds.
2. An entry in an ICS calendar feed served at a token-protected URL on `<your-domain>`. iOS Calendar subscribes to that feed once and pulls updates automatically. The same calendar shows up on iPad, iPhone, and via `icloud.com` on the laptop.

That is the entire system. One Go binary, one SQLite file, two HTTP endpoints (one outbound to ntfy, one inbound serving the ICS feed). The rendering on every device is handled by apps that already exist on the OS — no client code is written by me.

## 3. Hosting

A small dedicated Linux VM with a public IP and a domain pointed at it (see `<your-domain>` placeholder used throughout). The service needs almost nothing in resources — roughly 30 MB of RAM and near-zero CPU, since it polls once every fifteen minutes and serves a calendar feed a few times an hour.

The Go process listens only on `127.0.0.1:8080`. nginx terminates SSL on the public domain and proxies the public path `/school/*` to the local port. The only inbound traffic from the internet is the ICS feed and the dashboard URL; everything else is internal.

Viable cheap hosts: Oracle Cloud Always-Free (overkill but truly free), Hetzner CX11 (about €4/month), or Fly.io's free tier. A home server or Raspberry Pi is not suitable, because iOS Calendar refreshes the ICS feed from public IP space and a NAT setup would require tunneling.

## 4. Project layout

A single Go module, packages split by responsibility so each piece is independently testable:

```
neverskip-sync/
  cmd/server/main.go        # wiring, lifecycle, http.Server
  internal/
    config/                 # env loading, validation
    neverskip/              # login client + scrapers
    parser/                 # title cleanup, timestamp extraction
    state/                  # sqlite wrapper
    notifier/               # ntfy poster
    calendar/               # ICS feed handler
  migrations/               # versioned sql files
  systemd/                  # unit file template
  nginx/                    # location block template
  Makefile
  README.md
```

SQLite via `modernc.org/sqlite` (pure Go, no CGO) so the binary cross-compiles statically from a laptop to the deploy target without dragging in libc dependencies. Calendar generation via `github.com/arran4/golang-ical` to avoid hand-writing RFC 5545.

## 5. Configuration and secrets

Everything via environment variables, loaded and validated once at startup, then passed through the call tree as a typed `Config` struct. No global state.

Variables:

- `NEVERSKIP_TOKEN` — value of the `token` cookie captured from a fresh Chrome
  login at `parent.neverskip.com`. Required. Lasts ~11 months.
- `NTFY_URL` — default `https://ntfy.sh`
- `NTFY_TOPIC` — long random string (anyone who guesses it can spam my phone)
- `ICS_TOKEN` — long random string used as a query-param secret on the ICS feed
- `CALENDAR_HOST` — default `neverskip-sync.local`; set to your real public domain so VEVENT UIDs remain globally unique
- `SQLITE_PATH` — default `/var/lib/neverskip-sync/state.db`
- `POLL_INTERVAL` — default `15m`
- `LISTEN_ADDR` — default `127.0.0.1:8080`
- `LOG_LEVEL` — default `info`
- `QUIET_HOURS` — default `false`; if `true`, skip polls 23:00–06:00 IST

Loaded by systemd from `/etc/neverskip-sync.env` with mode 0600, owned by the service user. Never committed to git. The credentials never appear in the binary, in logs, or in any error message — log lines mention "credential failure" rather than echoing the value back.

## 6. Neverskip client

> **As-built.** Original assumption: `mobile + password → session cookie`.
> Reality: a single header `token: <value>`, where the value is the long-lived
> cookie issued at OTP login. Re-auth is a once-a-year operator action, not a
> per-session client concern. The "self-healing re-login" idea from the
> original draft is gone; instead, a 401 / `S=false` response surfaces as
> `neverskip.ErrUnauthenticated`, which the poll loop translates into a "Re-
> pair the token" ntfy alert.

Endpoints (`POST`, JSON in/out):

| Purpose | Path | Body |
|---|---|---|
| Lounge | `/parentweb/connect/fetchloungeinfo` | `{"values":"","page":"","filter_date":"0"}` |
| Daily notice | `/parentweb/connect/fetchdailynoticeinfo` | `{"values":"","page":"","filter_date":"0"}` |
| Auth probe | `/parentweb/auth/hasauth` | `{}` |

Host: `https://nskapi.neverskip.com`. Required headers: `token`,
`content-type: application/json`, plus a real-browser `origin` and `referer`
to keep Imperva/Incapsula happy.

Strongly-typed response structs use Go's standard `encoding/json`:

```go
type LoungeResp struct {
    S bool       `json:"S"`
    D LoungeData `json:"D"`
    F string     `json:"F"`
}

type LoungeData struct {
    ItemList    []LoungeItem `json:"item_list"`
    PrMonthYr   string       `json:"prmontyr"`
    PrevMonthYr string       `json:"pervious_prmontr"`
}

type LoungeItem struct {
    Title   string       `json:"title"`
    Items   []Attachment `json:"item_list"`
    MoreCnt string       `json:"cnt"`
}

type Attachment struct {
    DownloadURL string `json:"download_url"`
    Src         string `json:"src"`
    MsgID       string `json:"msg_id"`
    Type        string `json:"type"`
    Ext         string `json:"ext"`
    YM          string `json:"ym"`
}
```

## 7. Title parsing

The `title` field in the lounge response is messy. It mashes together the section tag, the title, the body, HTML markup, and the timestamp into one string, with content like:

```
[ I - E ] NEWSLETTER FOR THE MONTH OF MAY:|Jai Sri Gurudev!\r\nNamaste Dear Parents,...Thank you,\r\nCoordinator - 09:05 AM | 05 May 2026
```

A small `parser` package owns the cleanup and produces a structured `Item`:

```go
type Item struct {
    Source      string       // "lounge" or "dailynotice"
    MsgID       string
    Section     string       // e.g. "I - E"
    CleanTitle  string
    Body        string
    PostedAt    time.Time
    EventTime   *time.Time   // best-effort; nil if not parseable
    Attachments []Attachment
}
```

The parsing pipeline:

The `[ I - E ]` prefix matches `^\[\s*([^\]]+)\s*\]\s*` and captures the section. The trailing timestamp matches `(?i) - (\d{1,2}:\d{2}\s*(?:AM|PM)) \| (\d{1,2}\s+\w{3}\s+\d{4})$` and captures `PostedAt`. `<br>` tags and `\r\n` sequences collapse into newlines. HTML entities decode via `html.UnescapeString`. The remaining string splits on the first `:|` to separate title from body.

Anything that fails to parse falls back to using the raw string as `CleanTitle` rather than crashing. A slightly ugly notification is much better than a missed one.

Deadline extraction from prose ("submit by Tuesday 12th May 2026") is explicitly deferred. The natural-language variation is too high for v1, and the full body is in the notification anyway — the human reading it can decide what to do.

## 8. State store

SQLite, single table, single source of truth for "have I seen this?":

```sql
CREATE TABLE seen (
  source       TEXT NOT NULL,
  msg_id       TEXT NOT NULL,
  first_seen   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  section      TEXT,
  clean_title  TEXT,
  body         TEXT,
  posted_at    TIMESTAMP,
  event_time   TIMESTAMP,
  attachments  TEXT,        -- JSON array of URLs
  PRIMARY KEY (source, msg_id)
);

CREATE INDEX seen_event_time ON seen(event_time) WHERE event_time IS NOT NULL;
CREATE INDEX seen_first_seen ON seen(first_seen);
```

The compound primary key on `(source, msg_id)` defends against the unlikely case of lounge and dailynotice sharing an ID space. `INSERT OR IGNORE` is the dedup primitive: a successful insert (rows-affected = 1) means a genuinely new item; a no-op insert (rows-affected = 0) means already-seen. No conflict resolution gymnastics.

Bootstrap concern: on the very first run, the database is empty and every existing lounge/dailynotice item is "new", which would push-notify a year of history all at once. The first-run bootstrap step fetches everything once with notification suppressed and writes the rows into `seen`, then transitions to the normal loop. A small marker file or a `bootstrapped_at` row in a `meta` table records this.

## 9. Poll loop

One goroutine, ticker with `POLL_INTERVAL` cadence and ±30 second jitter to avoid synchronised behaviour. Each tick:

The loop fetches both sources concurrently (two goroutines, waitgroup), parses each response, runs every item through `INSERT OR IGNORE`, collects the genuinely new ones, and for each new item: posts to ntfy and (if it has a parseable `event_time`) invalidates the ICS cache so the next calendar fetch reflects it.

Errors per source do not kill the loop. If the lounge fetch fails but dailynotice succeeds, the new dailynotice items still get processed.

Optionally, the loop skips polling between 23:00 and 06:00 local time. School systems do not post notices overnight; this saves log noise and is a small courtesy to Neverskip's infrastructure.

## 10. Notifier (ntfy)

ntfy.sh is intentionally simple: one HTTP POST to `https://ntfy.sh/<topic>` with the message as the body. Optional headers add structure:

- `Title: [I-E] Newsletter for May` — bold header in the notification
- `Click: <attachment_url>` — tapping the push opens the PDF or Google Form directly
- `Priority: 3` — default; can be bumped for urgent items if I later add a notion of urgency
- `Tags: school` — categorisation if I subscribe to multiple ntfy topics

Five-second timeout per POST. Errors get logged but not retried indefinitely. If the push fails, the item is still in the calendar feed and will appear when the calendar refreshes — push is a "nice to have first alert", the calendar is the source of truth.

The topic name is the only auth, so it must be a long random string. Anyone who guesses or finds it can spam my phone but cannot read past notifications.

## 11. ICS feed

The handler reads all rows from `seen` where the item belongs on the calendar — initially that means dailynotice items with a non-null `event_time`, but the rule is configurable. It builds a `VCALENDAR` with one `VEVENT` per row:

- `UID = msg_id@<your-domain>` — stable across regenerations, so iOS Calendar updates events rather than duplicating them
- `SUMMARY = [section] clean_title`
- `DTSTART = event_time`
- `DTEND = event_time + 1h` (default duration when not specified)
- `DESCRIPTION = body + attachment URLs`
- `URL = first attachment` (so tapping the event in iOS opens the PDF)

The rendered ICS bytes are cached in memory for ~60 seconds. iOS Calendar hits the endpoint repeatedly, often back-to-back across devices, and re-rendering on every request wastes CPU. The cache invalidates when new items are inserted.

The handler returns `Content-Type: text/calendar; charset=utf-8`, sets an `ETag` based on the content hash, and responds with `304 Not Modified` on conditional requests. iOS Calendar honours both, which keeps bandwidth and load minimal.

Endpoint shape: `GET /school/calendar.ics?token=<ICS_TOKEN>`. The token check uses `subtle.ConstantTimeCompare`. Anyone with the URL can subscribe and read the calendar, so I treat the URL as a secret and rotate `ICS_TOKEN` if I ever share it accidentally.

## 12. HTTP server and nginx

The Go server listens only on `127.0.0.1:8080`. Three handlers:

- `GET /school/calendar.ics` — the feed described above
- `GET /school/healthz` — returns `200 OK` with the literal body `ok`, for monitoring
- `GET /school/debug/recent` — returns the last 20 items as JSON, protected by a separate debug token, useful when something goes wrong

nginx has a `location /school/` block that proxies to `127.0.0.1:8080` with the standard `X-Forwarded-For`, `X-Forwarded-Proto`, and `Host` headers. SSL termination is handled by the existing nginx config on `<your-domain>`, so the new block inherits HTTPS for free.

## 13. Resilience

Each outbound HTTP call to Neverskip uses a 30-second timeout and up to 3 retries with exponential backoff (1 s, 4 s, 9 s). After 3 failures the call returns an error, the current tick logs it, and the next tick tries again — there's no point hammering Neverskip if they're down.

Database errors are treated as fatal: the service panics, systemd restarts it. SQLite failures usually mean disk-full or corruption, both of which warrant fresh attention rather than a limping process.

A health-tracking counter records consecutive failed cycles. After three in a row, the service pushes "Neverskip sync is unhealthy: <last error>" to ntfy. After ten, it pushes again with a louder priority. This makes silent failure impossible — if the service stops working, I find out within an hour.

## 14. Deployment

`make build` cross-compiles a static binary for the deploy target's architecture (likely linux/amd64; arm64 if it's an ARM instance). `scp` the binary, the systemd unit, and the nginx fragment over. `systemctl daemon-reload && systemctl restart neverskip-sync`. `nginx -t && nginx -s reload`.

The systemd unit pins:

```
[Service]
Type=simple
User=neverskip
Group=neverskip
EnvironmentFile=/etc/neverskip-sync.env
ExecStart=/opt/neverskip-sync/bin/server
Restart=always
RestartSec=10s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/neverskip-sync
```

A dedicated `neverskip` user owns the SQLite file and has no shell. The binary lives in `/opt/neverskip-sync/bin/`. State lives in `/var/lib/neverskip-sync/`. Logs go to the journal (`journalctl -u neverskip-sync`).

The iOS Calendar subscription is set up once: Settings → Calendar → Accounts → Add Account → Other → Add Subscribed Calendar → paste `https://<your-domain>/school/calendar.ics?token=<ICS_TOKEN>`. Refresh frequency: every 15 minutes. Done.

The ntfy iOS app is installed once from the App Store, then `Add subscription` with the topic name. Done.

## 15. Phased rollout

The temptation with a green-field project is to build everything at once. Resist. Three phases, each independently useful.

**Phase 1 — done.** Lounge + dailynotice scrapers (the auth model collapsed
the artificial Phase 1/2 split — once the API was understood, dailynotice was
a free extra rather than separate work), parser, SQLite state, ntfy notifier,
poll loop with bootstrap mode and consecutive-failure alerts.

**Phase 2 — done.** ICS feed handler with token check, in-memory cache,
ETag/304. systemd unit and nginx fragment. iOS Calendar subscription is the
operator action — paste the URL into Settings → Calendar → Add Subscribed
Calendar on the phone.

**Phase 3 — optional, when motivated.** Mirror PDFs to MinIO so links survive Neverskip URL rotation. Best-effort deadline parsing from body text for known patterns ("submit by", "deadline:", "due on") — this would populate `event_time` so calendar entries land on the date *something is due* rather than the date the school posted about it. A small web dashboard at `/school/dashboard` showing the last 30 days in a sortable table. None of this is necessary to call the project done — Phases 1 and 2 already replace the workflow.

## 16. Open questions / blocking items

> **Resolved.** Both blockers (login request shape, dailynotice JSON shape)
> were sidestepped by capturing the iOS-Angular app's network traffic via
> Playwright driving headless Chromium with the already-logged-in profile.
> See `scripts/capture_api.py` for the discovery script and
> `internal/neverskip/testdata/` for the sanitised fixtures. Phase 1 ended up
> being roughly the line count predicted, but the architecture got *smaller*
> because no login machinery was needed.

## Appendix A: Lounge JSON shape (known)

The lounge endpoint returns:

```json
{
  "S": true,
  "D": {
    "allow_download_nb_cg_videos": "N",
    "item_list": [
      {
        "title": "[ I - E ] <title>:<body> - HH:MM AM/PM | DD MMM YYYY",
        "item_list": [
          {
            "src": "https://...",
            "file_loc": "D" | "S",
            "thump_url": "https://...",
            "download_url": "https://...",
            "type": "L",
            "msg_id": "34489",
            "delivery_stat": "P" | "N",
            "pin_stat": "N",
            "ym": "202605",
            "ext": "pdf" | ""
          }
        ],
        "cnt": "" | " +6"
      }
    ],
    "prmontyr": "20260405_13092",
    "pervious_prmontr": ""
  },
  "F": "S"
}
```

Key fields for the implementation:

- `msg_id` is the stable dedup key (string, not int).
- `title` is composite and needs the parser pipeline described in §7.
- `download_url` works without auth once the URL is known — PDFs are public S3/CloudFront objects.
- `cnt: " +6"` indicates 6 additional attachments not shown in this view, suggesting a per-message detail endpoint may exist. To be confirmed in DevTools when clicking into a message.
- `prmontyr` is a pagination cursor (looks like `YYYYMMDD_id`); useful for backfill on first run.

## Appendix B: Risks and caveats

Neverskip is a closed system with no API stability promise. The endpoint structure can change without warning. The mitigation is small: types are concentrated in one file, the parser is concentrated in another, and the service alerts on parse failures. If they change the JSON shape, I find out within an hour and have a localised fix.

The login flow is the most fragile part. If they add CAPTCHA, MFA, or rate limiting, the service breaks. There is no clean workaround other than reverting to manual checks until I add support for the new flow.

The service is single-user, scoped to my account. Sharing it with another parent would require multi-tenant credential storage, which is a significantly larger project and explicitly out of scope.

Storing the Neverskip password in an env file on a server is a real risk — the server's compromise compromises the account. Mitigation: server is hardened (no shell access except via key, firewall, fail2ban), the account's blast radius is limited (it's a parent portal, not a financial account), and the password is unique to this service. If Neverskip ever supports app-specific tokens, I switch to that immediately.
