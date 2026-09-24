# Verification — 25 September 2026

## Local results

- Go tests with the race detector and `go vet` passed.
- Exact fractional valuation, liquidation marks, fixture reproducibility and combined sleeve totals checked.
- Missing, crossed, stale, future, duplicate and foreign-currency quotes are rejected without returning a partial NAV.
- HTTP tests cover idempotency requirements, malformed bodies, unsupported parameters and browser cross-origin writes.
- Compose built and ran Go, PostgreSQL, Redis and NATS successfully.
- Integration smoke: eight concurrent identical requests create one run; the worker records six snapshots/decisions with final combined equity of $10,005 from artificial holdings.
- Recovery drills: accepted work survives a NATS outage; reconnect drains the outbox; duplicate delivery does not change committed results; Redis loss preserves durable reads; app/database/queue restarts preserve results.
- Browser checks at 1440×1100 and 390×844: replay creation, completed results, sleeve filtering and decision expansion passed; no page overflow or browser exceptions.
- Optional Prometheus/Grafana profile started. Prometheus scraped `up=1`; Grafana served the provisioned `mfd-lab` dashboard.

GitHub CI repeats Go checks, builds the Compose stack, and runs the smoke/recovery scripts. Browser checks were performed locally; they are not part of CI yet.

## eToro demo check

Two read-only requests to the official demo P&L endpoint returned HTTP 403. The second probe classified the response as an edge rejection from known response text. No authenticated portfolio response was obtained, so key validity and account capabilities remain unverified. No orders were submitted. Credentials and response bodies are absent from the repository; only this sanitized outcome is retained.

The lab therefore remains in fixture mode. The next broker milestone needs successful authenticated demo access and sanitized contract fixtures. See [research](research/2026-09-25-foundations.md) for API assumptions still requiring verification.

## Limits

This does not validate real strategy performance, broker accounting, execution, Jev integration, backups, public deployment or high availability. Fixture observations are artificial and do not represent live or historical market results. Authentication, a complete financial ledger and general job administration are planned in the [delivery plan](roadmap.md).
