# Setting up neverskip-sync

End-to-end walkthrough for getting the service running on your server and
hooking it up to your iPhone. Follow it top to bottom the first time.

If you just want to test the pipeline on your laptop without deploying
anything, jump to **[Appendix: local dry run](#appendix-local-dry-run)**.

## What you'll end up with

1. A Go service running on your server (Spectre box) that polls Neverskip
   every 15 minutes.
2. Push notifications on your iPhone the moment a new lounge or daily-notice
   item appears.
3. A subscribed calendar on the iPhone that shows every Neverskip
   communication as a dated event.

Total time: about 30 minutes if nothing goes wrong.

## Prerequisites

On your **laptop** (where Chrome is logged in to Neverskip):

- Go 1.25 or newer (`go version`)
- Python 3.10 or newer (`python3 --version`)
- `ssh` access to your server with a user that can `sudo`
- You're currently logged in to `parent.neverskip.com` in Chrome

On your **server** (the Spectre box):

- Linux (any modern distro with `systemd`)
- nginx already running, terminating SSL for `spectretrade.in`
- `sudo` privileges for the SSH user you'll use

On your **iPhone**:

- The free **ntfy** app from the App Store (don't install it yet — we'll
  configure it last)

## Part A — Capture the Neverskip auth token

The Neverskip parent portal sets a `token` cookie when you log in with OTP.
That cookie lasts about 11 months, and the service uses it as the only
credential. You re-capture it roughly once a year (or whenever you log out
of Chrome).

On your laptop, in the project directory:

```bash
cd ~/neverskip-sync
python3 -m venv .venv
.venv/bin/pip install browser_cookie3 requests
```

That sets up the helper script. Now verify it can read your Chrome cookie:

```bash
make token-env
```

You should see something like:

```
NEVERSKIP_TOKEN=eyJuc2thcHBfcHBfYWNjZXNzX3Rva2VuIjoiNz...
```

**If you see `no 'token' cookie found`**, your Chrome isn't logged in to
`parent.neverskip.com`. Open the site in Chrome, complete the OTP, then
re-run the command.

**Never paste this value into chat, screenshots, or git.** Treat it like a
password.

## Part B — Build the server binary

Still on the laptop:

```bash
make build-linux
```

This cross-compiles a statically linked binary at
`bin/neverskip-sync.linux-amd64` (about 10 MB). It needs no shared libraries
on the server side and will run on any modern x86_64 Linux.

**Different server architecture?** If your server is ARM (most Raspberry Pi
or Oracle Cloud Always-Free instances), edit the `build-linux` target in
`Makefile` and change `GOARCH=amd64` to `GOARCH=arm64`.

## Part C — Install on the server

Pick a working SSH alias. The examples below assume you can run
`ssh spectre` and reach the server. Adjust to your real hostname.

### C.1 — Create a dedicated user and directories

```bash
ssh spectre 'sudo useradd --system --home /var/lib/neverskip-sync --shell /usr/sbin/nologin neverskip'
ssh spectre 'sudo install -d -o neverskip -g neverskip /var/lib/neverskip-sync'
ssh spectre 'sudo install -d -o root      -g root      /opt/neverskip-sync/bin'
```

This creates an unprivileged `neverskip` user that owns the SQLite state
directory and runs the service. It has no shell, no home directory beyond
the state dir, and no sudo rights.

### C.2 — Ship the binary

```bash
scp bin/neverskip-sync.linux-amd64 spectre:/tmp/server
ssh spectre 'sudo install -m 0755 -o root -g root /tmp/server /opt/neverskip-sync/bin/server && rm /tmp/server'
```

### C.3 — Drop in the systemd unit

```bash
scp systemd/neverskip-sync.service spectre:/tmp/
ssh spectre 'sudo install -m 0644 /tmp/neverskip-sync.service /etc/systemd/system/'
ssh spectre 'sudo systemctl daemon-reload'
```

### C.4 — Create the environment file

This is the only place real secrets live. Mode `0600`, owned by the service
user, never committed.

```bash
scp systemd/neverskip-sync.env.example spectre:/tmp/
ssh spectre 'sudo install -m 0600 -o neverskip -g neverskip /tmp/neverskip-sync.env.example /etc/neverskip-sync.env'
```

Now edit it on the server and fill in the three required secrets:

```bash
ssh spectre 'sudo -e /etc/neverskip-sync.env'
```

You need to replace three lines:

| Variable | How to generate |
|---|---|
| `NEVERSKIP_TOKEN` | Run `make token-env` on the laptop, paste the value after `NEVERSKIP_TOKEN=` |
| `NTFY_TOPIC` | `openssl rand -hex 16` — a long random string. Remember it; goes into the iPhone ntfy app. |
| `ICS_TOKEN` | `openssl rand -hex 32` — another long random string. Goes into the calendar URL. |

Leave the other variables at their defaults unless you have reason to change
them.

**Save and close.** Verify the file is readable only by the service user:

```bash
ssh spectre 'sudo ls -l /etc/neverskip-sync.env'
# expect: -rw------- 1 neverskip neverskip ...
```

### C.5 — Add the nginx location block

```bash
scp nginx/neverskip-sync.conf spectre:/tmp/
ssh spectre 'sudo install -m 0644 /tmp/neverskip-sync.conf /etc/nginx/snippets/neverskip-sync.conf'
```

Now include it in the existing `server { }` block for `spectretrade.in`:

```bash
ssh spectre 'sudo $EDITOR /etc/nginx/sites-enabled/spectretrade.in.conf'
```

Inside the `server { ... }` block that listens on `443`, add:

```nginx
include /etc/nginx/snippets/neverskip-sync.conf;
```

Test and reload:

```bash
ssh spectre 'sudo nginx -t && sudo systemctl reload nginx'
```

`nginx -t` must say `syntax is ok` and `test is successful`. If it doesn't,
fix the config before continuing — a broken nginx config will take down
everything on `spectretrade.in`.

### C.6 — Start the service

```bash
ssh spectre 'sudo systemctl enable --now neverskip-sync'
```

Watch the first ~30 seconds of logs to confirm it's healthy:

```bash
ssh spectre 'sudo journalctl -u neverskip-sync -f --since=-1m'
```

You should see, in order:

```
level=INFO msg="startup auth probe ok"
level=INFO msg="starting poll loop" interval=15m0s ...
level=INFO msg="http listening" addr=127.0.0.1:8080
level=INFO msg="bootstrap: fresh database, marking existing items as seen without notifying"
level=INFO msg="bootstrap complete" items=15
```

The **bootstrap step is intentional** — on first run every existing notice
is "new", so the service records them in SQLite without push-notifying.
Otherwise you'd get a year of backlog pushed to your phone in one go.

Press `Ctrl-C` to stop tailing.

### C.7 — Verify the public endpoints work

From your **laptop** (or anywhere on the internet), test the health probe:

```bash
curl -s https://spectretrade.in/school/healthz
# expect: ok
```

And the calendar feed with your real `ICS_TOKEN`:

```bash
curl -s -o /tmp/cal.ics "https://spectretrade.in/school/calendar.ics?token=$(ssh spectre 'sudo grep ^ICS_TOKEN= /etc/neverskip-sync.env | cut -d= -f2-')"
head -20 /tmp/cal.ics
```

You should see a `BEGIN:VCALENDAR` block with one or more `BEGIN:VEVENT`
entries.

If you don't have access to the env file from your shell, just open
`https://spectretrade.in/school/calendar.ics?token=PASTE_TOKEN_HERE` in your
browser — it'll download or display the ICS feed.

The server side is done.

## Part D — Set up the iPhone

### D.1 — ntfy (push notifications)

1. Install **ntfy** from the App Store.
2. Open it, tap the **+** in the top-right corner → **Add subscription**.
3. **Topic**: paste the `NTFY_TOPIC` value from your env file. (You'll never
   see it in the UI again — write it down somewhere if you might need it.)
4. **Server**: leave the default (`https://ntfy.sh`) unless you set
   `NTFY_URL` to something else.
5. Tap **Subscribe**.
6. Pull down on the topic to refresh — you should see "Connected" at the top.

To verify pushes work, you can send a manual test from the server:

```bash
ssh spectre 'curl -s -d "test push from server" https://ntfy.sh/$(sudo grep ^NTFY_TOPIC= /etc/neverskip-sync.env | cut -d= -f2-)'
```

A notification titled "ntfy.sh/your-topic" should appear on the phone
within a couple of seconds.

### D.2 — Subscribed calendar

1. On the iPhone: **Settings → Apps → Calendar → Accounts → Add Account → Other → Add Subscribed Calendar**.
   (On older iOS: **Settings → Calendar → Accounts → ...**.)
2. **Server**: paste the full URL with the token:
   ```
   https://spectretrade.in/school/calendar.ics?token=YOUR_ICS_TOKEN_HERE
   ```
3. Tap **Next**. iOS will fetch the feed once to validate.
4. **Description**: rename to something like "School" if you want.
5. **Use SSL**: ON.
6. **Remove Alarms**: optional — set to ON if you don't want each event to
   alert you (notifications already cover that via ntfy).
7. Tap **Save**.

Open the **Calendar** app. Within a minute or two you'll see Neverskip
events appear on the dates the school posted them. Pulling down on a day's
view forces a refresh. iOS will then refresh in the background according to
the **Fetch New Data** setting (Settings → Calendar → Accounts → Fetch New
Data — set to **Every 15 Minutes** or **Push** for fastest updates).

The same calendar shows up on iPad (same Apple ID) and on the laptop via
`icloud.com → Calendar`.

## Part E — End-to-end verification

1. Watch the service: `ssh spectre 'sudo journalctl -u neverskip-sync -f'`
2. Wait until the school posts something new (or simulate by deleting a row
   from SQLite — see [Manual smoke test](#manual-smoke-test) below).
3. Within `POLL_INTERVAL` (default 15 minutes), you should see in the logs:
   ```
   level=INFO msg="tick: new items" count=1
   ```
4. Within seconds of that line, your iPhone should buzz with a notification
   from ntfy.
5. Within the next iOS calendar refresh window, the event appears on the
   calendar.

If all three happen, you're done. Close this guide and forget the service
exists — that's the goal.

## Part F — Routine maintenance

### When the token expires

Roughly once a year (or whenever you log out of Chrome at the parent
portal), ntfy will push you a message titled **"Neverskip token expired"**.
To recover:

1. On your laptop, log back in to `parent.neverskip.com` in Chrome with
   OTP.
2. Run `make token-env` — copy the value after `NEVERSKIP_TOKEN=`.
3. Edit the env file on the server and replace the old `NEVERSKIP_TOKEN`:
   ```bash
   ssh spectre 'sudo -e /etc/neverskip-sync.env'
   ```
4. Restart the service:
   ```bash
   ssh spectre 'sudo systemctl restart neverskip-sync'
   ```
5. Confirm it's healthy:
   ```bash
   ssh spectre 'sudo journalctl -u neverskip-sync --since=-1m'
   ```
   Look for `startup auth probe ok` again.

### Updating the service

When you pull new code on the laptop:

```bash
make build-linux
scp bin/neverskip-sync.linux-amd64 spectre:/tmp/server
ssh spectre 'sudo install -m 0755 /tmp/server /opt/neverskip-sync/bin/server && rm /tmp/server && sudo systemctl restart neverskip-sync'
```

The SQLite database persists across restarts — your dedup history doesn't
get lost.

## Troubleshooting

### `startup auth probe failed` on service start

The token in `/etc/neverskip-sync.env` is wrong, expired, or you logged out
of Chrome. Re-capture it (see **[When the token expires](#when-the-token-expires)**).

### `nginx: [emerg]` after editing nginx config

Don't reload — your nginx is still running on the old config. Fix the syntax
error first, re-run `sudo nginx -t` until it says `successful`, then reload.

### Calendar shows no events

- Run `curl -s -o /dev/null -w '%{http_code}\n' "https://spectretrade.in/school/calendar.ics?token=YOUR_TOKEN"` — should print `200`.
- If `401`: token mismatch. Compare the URL you pasted into iOS with the
  `ICS_TOKEN` value in `/etc/neverskip-sync.env`.
- If `501`: `ICS_TOKEN` is unset in the env file. Set it and restart.
- If `200` but the iPhone shows no events: iOS sometimes caches an empty
  calendar for a long time. Delete the subscription and re-add it.

### No push notifications

- Test ntfy manually with the curl one-liner under **Part D.1**. If that
  works but Neverskip pushes don't arrive, the issue is in the service —
  check journal logs for `ntfy push failed`.
- If even the manual ntfy push doesn't arrive on the iPhone, check that
  the ntfy app shows "Connected" and the topic name is identical (no extra
  spaces).

### Service crashes repeatedly

```bash
ssh spectre 'sudo journalctl -u neverskip-sync --since=-10m -n 100'
```

The most common cause: `SQLITE_PATH` points to a directory the
`neverskip` user can't write to. Default is `/var/lib/neverskip-sync/state.db`
— make sure that dir exists and is owned by `neverskip:neverskip`.

### Manual smoke test

To force the next tick to find a "new" item without waiting for the school:

```bash
ssh spectre 'sudo -u neverskip sqlite3 /var/lib/neverskip-sync/state.db \
  "DELETE FROM seen WHERE source = '\''dailynotice'\'' ORDER BY first_seen DESC LIMIT 1;"'
ssh spectre 'sudo systemctl restart neverskip-sync'
```

The just-deleted item will be re-discovered on the next poll and trigger a
real push notification.

## Appendix: local dry run

If you want to validate the whole pipeline on your laptop before touching
the server:

```bash
cat > .env <<EOF
NEVERSKIP_TOKEN=$(.venv/bin/python scripts/extract_token.py)
NTFY_TOPIC=$(openssl rand -hex 16)
ICS_TOKEN=$(openssl rand -hex 32)
SQLITE_PATH=$(pwd)/state.db
POLL_INTERVAL=15m
LISTEN_ADDR=127.0.0.1:18080
LOG_LEVEL=info
EOF

make run-once
```

This does **bootstrap only** (no pushes fired). To run the full service
locally with the HTTP server up:

```bash
make build
set -a; source .env; set +a
./bin/neverskip-sync
```

In another terminal:

```bash
curl -s http://127.0.0.1:18080/school/healthz
curl -s "http://127.0.0.1:18080/school/calendar.ics?token=$(grep ^ICS_TOKEN= .env | cut -d= -f2-)" | head -20
```

Press `Ctrl-C` in the first terminal to stop it. The local SQLite file at
`./state.db` is your scratch state — `rm state.db` resets the bootstrap.
