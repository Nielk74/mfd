#!/usr/bin/env python3
"""Identify Demo/Real read scope using official eToro aggregate endpoints.

Supply ETORO_API_KEY and one or both eToro user keys privately.
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
    ("demo/aggregate-portfolio", "/api/v1/trading/info/demo/aggregate-portfolio"),
    ("real/aggregate-portfolio", "/api/v1/trading/info/aggregate-portfolio"),
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
            if "aggregate-portfolio" in label:
                result["schema_valid"] = (isinstance(payload, dict)
                    and isinstance(payload.get("timestamp"), str)
                    and payload.get("accountCurrency") == "USD"
                    and isinstance(payload.get("accountTotals"), dict)
                    and isinstance(payload.get("instrumentAggregates"), list)
                    and isinstance(payload.get("mirrors"), list))
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
    keys = {slot: os.environ.get(f"ETORO_{slot.upper()}_USER_KEY") for slot in ("demo", "real")}
    if not api_key or not any(keys.values()):
        print("Set ETORO_API_KEY and at least one eToro user key in a private local environment.", file=sys.stderr)
        return 2
    output = []
    for slot, user_key in keys.items():
        if not user_key:
            continue
        results = [probe(*endpoint, api_key, user_key) for endpoint in ENDPOINTS]
        successful = [name.split("/")[0] for name in ("demo/aggregate-portfolio", "real/aggregate-portfolio")
                      if any(result.get("endpoint") == name and result.get("http_status") == 200 and result.get("schema_valid") for result in results)]
        confirmed = successful[0] if len(successful) == 1 else "unknown"
        output.append({"configured_slot": slot, "confirmed_environment": confirmed, "key_matches_slot": confirmed == slot,
                       "results": results})
    print(json.dumps({"read_only": True, "keys": output}))
    return 0 if all(item["key_matches_slot"] for item in output) else 1


if __name__ == "__main__":
    sys.exit(main())
