"""Load the lounge page in a real headless browser using Chrome's cookies and
record every network request the app makes.

The aim is to discover the real API host and request shape without DevTools.
Outputs go to scripts/capture_output/ — JSON metadata + raw request/response
bodies for anything that looks API-shaped.

Cookie *values* are read from local Chrome and used in-process; they are never
printed. The captured request/response files may contain auth artefacts —
those files stay under the project dir and are gitignored.
"""

import asyncio
import hashlib
import json
import os
import re
import sys
from pathlib import Path

import browser_cookie3
from playwright.async_api import async_playwright

OUT_DIR = Path(__file__).parent / "capture_output"
ROUTES_TO_VISIT = [
    "https://parent.neverskip.com/default/lounge",
    "https://parent.neverskip.com/default/dailynotice",
]


def cookies_for_playwright() -> list[dict]:
    jar = browser_cookie3.chrome()
    out = []
    for c in jar:
        d = c.domain or ""
        if "neverskip" not in d:
            continue
        out.append({
            "name": c.name,
            "value": c.value,
            "domain": d,
            "path": c.path or "/",
            "secure": bool(c.secure),
            "httpOnly": False,
            "expires": int(c.expires) if c.expires else -1,
            "sameSite": "Lax",
        })
    return out


def looks_like_api(url: str, content_type: str | None) -> bool:
    if not url:
        return False
    if any(url.endswith(ext) for ext in (".js", ".css", ".woff2", ".png", ".jpg", ".svg", ".ico")):
        return False
    if "cdn.jsdelivr.net" in url or "fonts.g" in url or "cloudflare" in url:
        return False
    if content_type and ("application/json" in content_type or "text/plain" in content_type):
        return True
    if "/api/" in url or "lounge" in url or "dailynotice" in url or "/parent/" in url:
        return True
    return False


async def main() -> int:
    OUT_DIR.mkdir(exist_ok=True)
    cookies = cookies_for_playwright()
    print(f"loaded {len(cookies)} neverskip cookies from Chrome", file=sys.stderr)

    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        context = await browser.new_context(
            user_agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
                       "(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
        )
        await context.add_cookies(cookies)
        page = await context.new_page()

        captured: list[dict] = []

        async def on_response(resp):
            try:
                req = resp.request
                url = resp.url
                ct = resp.headers.get("content-type")
                if not looks_like_api(url, ct):
                    return
                entry = {
                    "url": url,
                    "method": req.method,
                    "status": resp.status,
                    "request_headers": {k: v for k, v in (await req.all_headers()).items()
                                          if k.lower() not in ("cookie", "authorization")},
                    "request_has_cookie": "cookie" in (await req.all_headers()),
                    "request_has_authorization": "authorization" in (await req.all_headers()),
                    "request_post_data": req.post_data,
                    "response_headers": {k: v for k, v in resp.headers.items()
                                            if k.lower() not in ("set-cookie",)},
                    "response_content_type": ct,
                }
                try:
                    body = await resp.text()
                    entry["response_body_len"] = len(body)
                    entry["response_body_sample"] = body[:600]
                except Exception:
                    entry["response_body_len"] = None
                    entry["response_body_sample"] = None
                captured.append(entry)
            except Exception as e:
                print(f"  on_response error for {resp.url}: {e}", file=sys.stderr)

        page.on("response", lambda r: asyncio.create_task(on_response(r)))

        for route in ROUTES_TO_VISIT:
            print(f"\n=== visiting {route}", file=sys.stderr)
            try:
                await page.goto(route, wait_until="networkidle", timeout=45_000)
            except Exception as e:
                print(f"  navigation error: {e}", file=sys.stderr)
            await page.wait_for_timeout(4000)

        await browser.close()

    # write summary
    summary_path = OUT_DIR / "summary.json"
    with summary_path.open("w") as f:
        json.dump(captured, f, indent=2)

    print(f"\ncaptured {len(captured)} API-shaped responses, saved to {summary_path}")

    # print short tabular summary to stdout
    print("\n--- captured API calls ---")
    for c in captured:
        flags = []
        if c["request_has_cookie"]:
            flags.append("cookie")
        if c["request_has_authorization"]:
            flags.append("authz")
        flag_str = ",".join(flags) or "-"
        print(f"  [{c['status']:>3}] {c['method']:<6} {c['url']:<80} hdr={flag_str:<14} ct={c['response_content_type']}")

    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
