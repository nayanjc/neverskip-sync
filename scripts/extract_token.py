"""Print the current parent.neverskip.com 'token' cookie value from Chrome.

The Neverskip parent portal OTP login sets a 'token' cookie that lives for
~11 months. The neverskip-sync service uses that value as the 'token' HTTP
header for every API call. When the token expires (or you log out), this
script extracts the freshly-issued value so it can be written into the
service's env file.

Usage:
    python scripts/extract_token.py              # prints token to stdout
    python scripts/extract_token.py --env        # prints as NEVERSKIP_TOKEN=...

Never paste the output into chat or commit it.
"""

import argparse
import sys

import browser_cookie3


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--env", action="store_true",
                    help="format as NEVERSKIP_TOKEN=<value> for piping into >> /etc/neverskip-sync.env")
    args = ap.parse_args()

    try:
        jar = browser_cookie3.chrome(domain_name="parent.neverskip.com")
    except Exception as e:
        print(f"error reading Chrome cookies: {e}", file=sys.stderr)
        return 2

    token = next((c.value for c in jar if c.name == "token"), None)
    if not token:
        print("no 'token' cookie found for parent.neverskip.com — are you logged in in Chrome?",
              file=sys.stderr)
        return 3

    if args.env:
        print(f"NEVERSKIP_TOKEN={token}")
    else:
        print(token)
    return 0


if __name__ == "__main__":
    sys.exit(main())
