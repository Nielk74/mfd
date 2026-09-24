#!/usr/bin/env python3
"""Diagnose official eToro demo read access without recording account payloads.

Supply ETORO_API_KEY and ETORO_DEMO_USER_KEY privately in the environment.
Only GET requests are made. Neither credentials nor response values are logged.
"""
import json
import os
import sys
import urllib.error
import urllib.request
import uuid

BASE = "https://public-api.etoro.com"
ENDPOINTS = [
    ("watchlists", "/api/v1/watchlists"),
    ("demo/portfolio", "/api/v1/trading/info/demo/portfolio"),
    ("demo/aggregate-portfolio", "/api/v1/trading/info/demo/aggregate-portfolio"),
    ("demo/pnl", "/api/v1/trading/info/demo/pnl"),
]


def probe(label, path, api_key, user_key):
    request = urllib.request.Request(
        BASE + path,
        headers={
            "x-api-key": api_key,
            "x-user-key": user_key,
            "x-request-id": str(uuid.uuid4()),
            "Accept": "application/json",
            "User-Agent": "mfd-read-probe/0.2",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            payload = json.load(response)
            result = {"endpoint": label, "http_status": response.status,
                      "shape": type(payload).__name__, "account_values_omitted": True}
            if isinstance(payload, dict) and isinstance(payload.get("isSucceeded"), bool):
                result["provider_success"] = payload["isSucceeded"]
            return result
    except urllib.error.HTTPError as error:
        body = error.read(16384).decode("utf-8", errors="replace").lower()
        category = "request_rejected"
        if "permission" in body and "access" in body:
            category = "permission_denied"
        elif "cloudflare" in body:
            category = "edge_rejection"
        elif "invalid api key" in body:
            category = "invalid_application_key"
        elif "invalid user key" in body:
            category = "invalid_user_key"
        elif "unauthorized" in body:
            category = "unauthorized"
        return {"endpoint": label, "http_status": error.code,
                "category": category, "body_omitted": True}
    except Exception as error:
        return {"endpoint": label, "error_type": type(error).__name__}


def main():
    api_key = os.environ.get("ETORO_API_KEY")
    user_key = os.environ.get("ETORO_DEMO_USER_KEY")
    if not api_key or not user_key:
        print("Set ETORO_API_KEY and ETORO_DEMO_USER_KEY in a private local environment.", file=sys.stderr)
        return 2
    results = [probe(*endpoint, api_key, user_key) for endpoint in ENDPOINTS]
    print(json.dumps({"read_only": True, "results": results}))
    return 0 if all(result.get("http_status") == 200 for result in results) else 1


if __name__ == "__main__":
    sys.exit(main())
