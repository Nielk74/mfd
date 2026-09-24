#!/usr/bin/env python3
"""Failure drills for this repository's local Compose stack (no broker access)."""
import json
import subprocess
import time
import urllib.request
import uuid
import os

BASE = os.environ.get("MFD_URL", "http://127.0.0.1:8088")


def compose(*args, data=None):
    return subprocess.run(["docker", "compose", *args], input=data,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          check=True, timeout=45).stdout


def get(path):
    with urllib.request.urlopen(BASE + path, timeout=5) as response:
        return json.load(response)


def enqueue():
    request = urllib.request.Request(BASE + "/api/v1/replays", data=b"{}", headers={
        "Content-Type": "application/json", "Idempotency-Key": str(uuid.uuid4())})
    with urllib.request.urlopen(request, timeout=5) as response:
        return json.load(response)["run_id"]


def acknowledged_sequence():
    state = json.loads(compose("exec", "-T", "nats", "wget", "-q", "-O", "-",
                              "http://127.0.0.1:8222/jsz?accounts=true&streams=true&consumers=true"))
    for account in state["account_details"]:
        for stream in account["stream_detail"]:
            if stream["name"] == "MFD_JOBS":
                for consumer in stream["consumer_detail"]:
                    if consumer["name"] == "replay-workers":
                        return consumer["ack_floor"]["consumer_seq"]
    raise AssertionError("durable consumer missing")


def complete(run_id):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        run = get("/api/v1/runs/" + run_id)
        if run["status"] == "completed":
            return run
        assert run["status"] == "queued", "run failed"
        time.sleep(0.3)
    raise AssertionError("run did not complete")


compose("stop", "nats")
try:
    run_id = enqueue()
    time.sleep(2)
    assert get("/api/v1/runs/" + run_id)["status"] == "queued"
finally:
    compose("start", "nats")
run = complete(run_id)

# Simulate duplicate delivery after a prior worker committed its result.
time.sleep(0.3)
before = acknowledged_sequence()
payload = ("CONNECT {}\r\nPUB mfd.jobs.replay.v1 36\r\n" + run_id + "\r\nPING\r\n").encode()
reply = compose("exec", "-T", "nats", "nc", "-w", "2", "127.0.0.1", "4222", data=payload)
assert b"PONG" in reply and b"-ERR" not in reply, "NATS rejected test publish"
deadline = time.monotonic() + 10
while acknowledged_sequence() <= before and time.monotonic() < deadline:
    time.sleep(0.3)
assert acknowledged_sequence() > before, "duplicate message was not acknowledged"
assert get("/api/v1/runs/" + run_id) == run, "redelivery changed committed result"

compose("stop", "redis")
try:
    assert get("/api/v1/runs/" + run_id) == run, "durable reads depend on cache"
finally:
    compose("start", "redis")

compose("restart", "app", "postgres", "nats")
compose("up", "-d", "--wait", "--wait-timeout", "30")
assert get("/api/v1/runs/" + run_id) == run, "restart changed persisted result"
print(json.dumps({"status": "passed", "run_id": run_id, "checks": [
    "queue outage preserves accepted job", "outbox drains after reconnect",
    "redelivery has no duplicate effect", "cache loss preserves reads", "restart persistence"]}))
