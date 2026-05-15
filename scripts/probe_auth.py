"""One-off probe: do the cookies in Chrome let us reach Neverskip authenticated?

Reads cookies for parent.neverskip.com from the local Chrome profile, hits the
root URL with them (no redirects), and reports status + a fingerprint of the
response so we can tell "logged in" from "redirected to login".

Never prints cookie values. Cookie names are listed so we know what's in play.
"""

import sys
from urllib.parse import urlparse

import browser_cookie3
import requests

DOMAIN = "parent.neverskip.com"
URLS_TO_TRY = [
    f"https://{DOMAIN}/",
    f"https://{DOMAIN}/default/lounge",
    f"https://{DOMAIN}/default/dailynotice",
]


def main() -> int:
    try:
        jar = browser_cookie3.chrome(domain_name=DOMAIN)
    except Exception as e:
        print(f"FAIL: could not read Chrome cookies: {e}", file=sys.stderr)
        return 2

    names = sorted({c.name for c in jar if DOMAIN in (c.domain or "")})
    print(f"cookies found for {DOMAIN}: {len(names)}")
    for n in names:
        print(f"  - {n}")
    if not names:
        print("no cookies — are you logged in to parent.neverskip.com in Chrome?")
        return 3

    session = requests.Session()
    session.cookies = jar
    session.headers.update({
        "User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
                      "(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
        "Accept": "application/json, text/html, */*",
    })

    for url in URLS_TO_TRY:
        print(f"\n--- GET {url} ---")
        try:
            r = session.get(url, allow_redirects=False, timeout=15)
        except Exception as e:
            print(f"  request failed: {e}")
            continue
        print(f"  status: {r.status_code}")
        loc = r.headers.get("Location")
        if loc:
            print(f"  redirect -> {loc}")
        ctype = r.headers.get("Content-Type", "")
        print(f"  content-type: {ctype}")
        body = r.text or ""
        print(f"  body length: {len(body)}")
        snippet = body[:300].replace("\n", " ").replace("\r", " ")
        print(f"  body[0:300]: {snippet!r}")

        verdict = classify(r.status_code, loc, ctype, body)
        print(f"  verdict: {verdict}")

    return 0


def classify(status: int, loc: str | None, ctype: str, body: str) -> str:
    body_low = body.lower()
    if status in (301, 302, 303, 307, 308) and loc:
        host = urlparse(loc).path.lower() + " " + (urlparse(loc).netloc or "").lower()
        if "login" in host or "auth" in host:
            return "REDIRECT-TO-LOGIN (cookies not accepted)"
        return "REDIRECT (non-login)"
    if status == 401 or status == 403:
        return "UNAUTHORIZED (cookies rejected)"
    if "application/json" in ctype:
        if '"s":true' in body_low or '"s": true' in body_low:
            return "JSON OK (looks logged-in)"
        if '"s":false' in body_low or '"s": false' in body_low:
            return "JSON but S=false (likely auth failure)"
        return "JSON (inspect manually)"
    if "<html" in body_low:
        if "login" in body_low and ("password" in body_low or "otp" in body_low or "mobile" in body_low):
            return "HTML login page (cookies not accepted)"
        return "HTML (probably the app shell — possibly logged-in)"
    return "UNKNOWN"


if __name__ == "__main__":
    sys.exit(main())
