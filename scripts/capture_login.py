"""One-off probe: capture the Neverskip OTP login flow's API endpoints.

We already know the runtime API (lounge, dailynotice). What we don't yet know
is the LOGIN API — the endpoints the Angular app hits when you:

1. submit your mobile number → server sends an OTP to your phone
2. submit the OTP → server returns the long-lived `token` value

Capturing that exchange is what lets us build a `make refresh-token` flow
that can re-auth without a browser at all.

This script launches a *headed* Chromium window with a fresh, empty profile
(so we don't reuse your already-logged-in cookies). You manually drive the
login. The script records every network request and saves it to
`scripts/capture_output/login.json`.

Robustness: each captured exchange is written to disk immediately, so even
if the browser teardown raises or you Ctrl-C, the file is current. We also
write a newline-delimited streaming copy (login.ndjson) — useful if the
final JSON file gets truncated somehow.

Steps:

  $ .venv/bin/python scripts/capture_login.py

  A Chrome window opens at parent.neverskip.com.
  - Enter your mobile number, click "Request OTP" / similar.
  - Wait for the SMS, type the OTP.
  - Click verify / login.
  - Once you see the lounge / dashboard, close the browser window.

The captured files *will* contain your OTP, mobile number, and the freshly
issued token. Do not paste them anywhere public. They live under
scripts/capture_output/ which is .gitignored.
"""

import asyncio
import json
import sys
import traceback
from pathlib import Path

from playwright.async_api import async_playwright

OUT_DIR = Path(__file__).parent / "capture_output"
OUT_FILE = OUT_DIR / "login.json"
NDJSON_FILE = OUT_DIR / "login.ndjson"

LOGIN_URL = "https://parent.neverskip.com/auth/login"


def looks_relevant(url: str, content_type: str | None) -> bool:
    if not url:
        return False
    if any(url.endswith(ext) for ext in (
        ".js", ".css", ".woff2", ".png", ".jpg", ".svg", ".ico", ".gif", ".webp",
    )):
        return False
    if "cdn.jsdelivr.net" in url or "fonts.g" in url or "cloudflare" in url:
        return False
    if "nskapi.neverskip.com" in url:
        return True
    if content_type and "application/json" in content_type:
        return True
    if "/auth" in url or "login" in url or "otp" in url or "/parentweb/" in url:
        return True
    return False


async def main() -> int:
    OUT_DIR.mkdir(exist_ok=True)
    captured: list[dict] = []

    # Reset both files at start so an old run can't shadow this one.
    OUT_FILE.write_text("[]")
    NDJSON_FILE.write_text("")

    def persist():
        """Write the running list to disk after every captured exchange. We
        rewrite the entire file each time — N is small (tens of entries)."""
        try:
            OUT_FILE.write_text(json.dumps(captured, indent=2))
        except Exception as e:
            print(f"  persist error: {e}", file=sys.stderr)

    def append_ndjson(entry: dict):
        try:
            with NDJSON_FILE.open("a") as f:
                f.write(json.dumps(entry) + "\n")
        except Exception as e:
            print(f"  ndjson append error: {e}", file=sys.stderr)

    async with async_playwright() as p:
        browser = await p.chromium.launch(
            headless=False,
            args=["--start-maximized"],
        )
        context = await browser.new_context(
            no_viewport=True,
            user_agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
                       "(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
        )
        page = await context.new_page()

        async def on_response(resp):
            try:
                req = resp.request
                ct = resp.headers.get("content-type")
                if not looks_relevant(resp.url, ct):
                    return
                entry: dict = {
                    "url": resp.url,
                    "method": req.method,
                    "status": resp.status,
                    "request_headers": dict(await req.all_headers()),
                    "request_post_data": req.post_data,
                    "response_headers": dict(resp.headers),
                    "response_content_type": ct,
                }
                try:
                    body = await resp.text()
                    entry["response_body"] = body
                    entry["response_body_len"] = len(body)
                except Exception:
                    entry["response_body"] = None
                    entry["response_body_len"] = None
                captured.append(entry)
                append_ndjson(entry)
                persist()
                # short live tail so user sees progress
                tag = url_tag(resp.url)
                print(f"  [{resp.status}] {req.method} {tag}", file=sys.stderr)
            except Exception:
                print("  on_response error:", file=sys.stderr)
                traceback.print_exc()

        page.on("response", lambda r: asyncio.create_task(on_response(r)))

        print("opening login page; complete the OTP flow in the browser, then close the window…",
              file=sys.stderr)
        try:
            await page.goto(LOGIN_URL, wait_until="domcontentloaded", timeout=45_000)
        except Exception as e:
            print(f"  initial nav warning (continuing): {e}", file=sys.stderr)

        # Wait for either the browser disconnect (user closed the window) OR
        # the page closing. Either way we exit gracefully.
        close_event = asyncio.Event()
        browser.on("disconnected", lambda *_: close_event.set())
        context.on("close", lambda *_: close_event.set())
        page.on("close", lambda *_: close_event.set())

        try:
            await close_event.wait()
        except KeyboardInterrupt:
            print("\nKeyboardInterrupt — saving what we have", file=sys.stderr)

    # Final persist outside the playwright context, just to be sure.
    persist()
    print(f"\nwrote {len(captured)} captured exchanges to {OUT_FILE}", file=sys.stderr)
    print(f"streamed copy at {NDJSON_FILE}", file=sys.stderr)

    print("\n=== summary (no payloads) ===")
    for c in captured:
        flag = "JSON" if (c.get("response_content_type") or "").startswith("application/json") else ""
        print(f"  [{c['status']:>3}] {c['method']:<6} {url_tag(c['url']):<70} {flag}")

    print("\n=== heuristically-interesting exchanges (paths only) ===")
    for c in captured:
        path = c["url"].split("?")[0].lower()
        if any(k in path for k in ("auth", "login", "otp", "verify", "session", "user", "token")):
            print(f"  {c['method']} {c['url']}")
    return 0


def url_tag(url: str) -> str:
    try:
        from urllib.parse import urlparse
        p = urlparse(url)
        return p.netloc + p.path
    except Exception:
        return url[:80]


if __name__ == "__main__":
    try:
        sys.exit(asyncio.run(main()))
    except KeyboardInterrupt:
        print("\nKeyboardInterrupt at top level", file=sys.stderr)
        sys.exit(1)
    except Exception:
        traceback.print_exc()
        sys.exit(2)
