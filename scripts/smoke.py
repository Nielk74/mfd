#!/usr/bin/env python3
"""Exercise real HTTP -> PostgreSQL outbox -> NATS -> worker -> result."""
import concurrent.futures
import json
import os
import time
import urllib.error
import urllib.request
import uuid
from decimal import Decimal

BASE = os.environ.get("MFD_URL", "http://127.0.0.1:8088")


def request(path, key=None, body=None):
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(BASE + path, data=json.dumps(body or {}).encode() if key else None, headers=headers)
    with urllib.request.urlopen(req, timeout=10) as response:
        return json.load(response)


assert all(request("/readyz").values()), "dependencies unavailable"
key = str(uuid.uuid4())
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
    requests = list(executor.map(lambda _: request("/api/v1/replays", key), range(8)))
assert len({r["run_id"] for r in requests}) == 1, "idempotency failed"
path = requests[0]["status_url"]
deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    run = request(path)
    if run["status"] != "queued":
        break
    time.sleep(0.25)
assert run["status"] == "completed", run["status"]
result = run["result"]
assert len(result["decisions"]) == 6
assert len(result["snapshots"]) == 6
assert sum(Decimal(s["equity"]) for s in result["snapshots"][-2:]) == Decimal("10005")
assert result["decisions"][-1]["action"] == "review"
assert request("/api/v1/replays", key)["run_id"] == run["id"]
assert request(path)["result"] == result, "retry changed the committed result"
scenario = {"name": "MSFT +10%", "final_shock_bps": {"MSFT": 1000}, "review_threshold_bps": 200}
scenario_key = str(uuid.uuid4())
scenario_id = request("/api/v1/replays", scenario_key, scenario)["run_id"]
for _ in range(120):
    custom = request("/api/v1/runs/" + scenario_id)
    if custom["status"] != "queued":
        break
    time.sleep(0.25)
assert custom["status"] == "completed"
assert Decimal(custom["result"]["snapshots"][-1]["equity"]) == Decimal("4227.5")
assert sum(Decimal(s["equity"]) for s in custom["result"]["snapshots"][-2:]) == Decimal("10207.5")
assert all(custom["result"]["snapshots"][i]["equity"] == result["snapshots"][i]["equity"] for i in range(4))
assert request("/api/v1/replays", scenario_key, scenario)["run_id"] == scenario_id
try:
    request("/api/v1/replays", scenario_key, {"name": "Changed"})
except urllib.error.HTTPError as error:
    assert error.code == 409
else:
    raise AssertionError("different payload reused an idempotency key")
ops = request("/api/v1/operations")
assert ops["database"]["runs"]["completed"] >= 2
assert any(job["run_id"] == scenario_id for job in ops["database"]["recent_jobs"])
print(json.dumps({"status": "passed", "run_id": run["id"], "checks": [
    "readiness", "concurrent idempotency", "durable queue processing", "decimal valuation",
    "decision trail", "immutable result on request retry", "scenario valuation",
    "idempotency conflict", "operations view"]}))
