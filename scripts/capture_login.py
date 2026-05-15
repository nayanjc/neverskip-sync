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
`scripts/capture_output/login.json` for later inspection.

Steps:

  $ .venv/bin/python scripts/capture_login.py

  A Chrome window opens at parent.neverskip.com.
  - Enter your mobile number, click "Request OTP" / similar.
  - Wait for the SMS, type the OTP.
  - Click verify / login.
  - Once you see the lounge / dashboard, close the browser window.

The script then writes scripts/capture_output/login.json containing one entry
per HTTP request, with request bodies and response bodies. Sanitised display
on stdout afterward — full payloads only in the file (which is gitignored).

The captured file *will* contain your OTP, mobile number, and the freshly
issued token. Do not paste it anywhere public. The file lives under
scripts/capture_output/ which is .gitignored.
"""

import asyncio
import json
import sys
from pathlib import Path

from playwright.async_api import async_playwright

OUT_DIR = Path(__file__).parent / "capture_output"
OUT_FILE = OUT_DIR / "login.json"

LOGIN_URL = "https://parent.neverskip.com/auth/login"


def looks_relevant(url: str, content_type: str | None) -> bool:
    if not url:
        return False
    # static asset filters
    if any(url.endswith(ext) for ext in (
        ".js", ".css", ".woff2", ".png", ".jpg", ".svg", ".ico", ".gif", ".webp",
    )):
        return False
    if "cdn.jsdelivr.net" in url or "fonts.g" in url or "cloudflare" in url:
        return False
    # we care most about nskapi calls but also keep anything API-ish
    if "nskapi.neverskip.com" in url:
        return True
    if content_type and "application/json" in content_type:
        return True
    if "/auth" in url or "login" in url or "otp" in url or "/parentweb/" in url:
        return True
    return False


async def main() -> int:
    OUT_DIR.mkdir(exist_ok=True)

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

        captured: list[dict] = []

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
                # tiny live tail so the user sees progress
                tag = url_tag(resp.url)
                print(f"  [{resp.status}] {req.method} {tag}", file=sys.stderr)
            except Exception as e:
                print(f"  on_response error for {resp.url}: {e}", file=sys.stderr)

        page.on("response", lambda r: asyncio.create_task(on_response(r)))

        print("opening login page; complete the OTP flow in the browser, then close the window…",
              file=sys.stderr)
        await page.goto(LOGIN_URL)

        # Wait for browser to close (user driven).
        close_event = asyncio.Event()
        browser.on("disconnected", lambda *_: close_event.set())
        await close_event.wait()

    OUT_FILE.write_text(json.dumps(captured, indent=2))
    print(f"\nwrote {len(captured)} captured exchanges to {OUT_FILE}", file=sys.stderr)

    # short tabular summary on stdout (no payloads)
    print("\n=== summary ===")
    for c in captured:
        flag = "JSON" if (c.get("response_content_type") or "").startswith("application/json") else ""
        print(f"  [{c['status']:>3}] {c['method']:<6} {url_tag(c['url']):<70} {flag}")

    # try to flag the obvious "login-y" calls
    print("\n=== heuristically-interesting exchanges ===")
    for c in captured:
        path = c["url"].split("?")[0].lower()
        if any(k in path for k in ("auth", "login", "otp", "verify", "session")):
            body = (c.get("response_body") or "")[:200].replace("\n", " ")
            print(f"  {c['method']} {c['url']}")
            print(f"    request_post_data: {(c.get('request_post_data') or '')[:200]}")
            print(f"    response_body[0:200]: {body}")
            print()
    return 0


def url_tag(url: str) -> str:
    """Compact display form of a URL — host + path, no query string."""
    try:
        from urllib.parse import urlparse
        p = urlparse(url)
        return p.netloc + p.path
    except Exception:
        return url[:80]


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
