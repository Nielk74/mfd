#!/usr/bin/env python3
"""Read-only demo connectivity probe. Secrets come only from the environment.

Prints HTTP status and response shape, never response values or credentials.
Does not persist account data or submit orders.
"""
import json
import os
import sys
import urllib.error
import urllib.request
import uuid


def main():
    api_key = os.environ.get("ETORO_API_KEY")
    user_key = os.environ.get("ETORO_DEMO_USER_KEY")
    if not api_key or not user_key:
        print("Set ETORO_API_KEY and ETORO_DEMO_USER_KEY in your local environment.", file=sys.stderr)
        return 2
    request = urllib.request.Request(
        "https://public-api.etoro.com/api/v1/trading/info/demo/pnl",
        headers={"x-api-key": api_key, "x-user-key": user_key,
                 "x-request-id": str(uuid.uuid4()), "Accept": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            data = json.loads(response.read(2_000_000))
            print(json.dumps({"endpoint": "demo/pnl", "http_status": response.status,
                              "shape": type(data).__name__, "account_values_omitted": True}))
            return 0
    except urllib.error.HTTPError as error:
        body = error.read(16_384).decode("utf-8", errors="replace").lower()
        # Classify known error phrases; never echo arbitrary provider text.
        category = "request_rejected"
        for phrase, label in [("invalid api key", "invalid_application_key"),
                              ("invalid user key", "invalid_user_key"),
                              ("subscription key", "application_subscription_key"),
                              ("insufficient", "insufficient_permissions"),
                              ("forbidden", "forbidden"),
                              ("cloudflare", "edge_rejection")]:
            if phrase in body:
                category = label
                break
        print(json.dumps({"endpoint": "demo/pnl", "http_status": error.code,
                          "category": category, "body_omitted": True}))
        return 1
    except Exception as error:
        print(json.dumps({"endpoint": "demo/pnl", "error_type": type(error).__name__}))
        return 1


if __name__ == "__main__":
    sys.exit(main())
