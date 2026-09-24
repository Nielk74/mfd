# mfd

**metro finance dodo** — a Go lab for portfolios, strategies and traceable trading decisions.

See what you own, what a strategy decided, and what actually happened. Humans and agents will use the same recorded operations. eToro is the only planned portfolio/execution provider. Its official demo read adapter is wired; account access is currently denied by eToro for the supplied key.

## Start

Requires Docker with Compose. No API keys needed for the fixture lab.

```sh
docker compose up -d --build --wait
```

Open **http://localhost:8088**. Create a named experiment from a preset or set the final AAPL/MSFT quote shocks and review threshold. Each run goes through PostgreSQL → NATS → a bounded Go worker pool, calculates exact-decimal marks, and records six decisions across two virtual sleeves. Compare runs, inspect positions and decision inputs, export valuations, and open **Queues & database** to read the current pipeline. Results survive restarts. Redis holds a disposable run-result cache.

The experiment data is artificial. Holdings stay fixed; only the last quote observation changes. This is an interactive research foundation, **not yet a backtester, paper broker or live trading system**. The configured eToro demo adapter makes a separate official, read-only portfolio request; a 403 never becomes a fabricated account value. No broker order or model call is made. See [delivery plan](docs/roadmap.md) for explicit milestones.

```sh
make check       # Go race tests, vet and Compose validation; requires Go 1.27.1+
make smoke       # HTTP/queue/database integration check against the running lab
make monitoring  # optional Prometheus :9098 and Grafana :3088
make down        # stop services; retain data volumes
```

Ports bind to `0.0.0.0`, so the lab is reachable at `http://<host-address>:8088`. Copy `.env.example` to `.env` to change ports or set `MFD_BIND_ADDRESS=127.0.0.1`. Infrastructure ports are published too. The UI/API has no authentication yet and the database uses development credentials. `docker compose down -v` deletes stored lab data.

The Mac deployment automatically checks `main` every minute and redeploys commits after CI passes. See [deployment and service addresses](docs/deployment.md) for setup, status, logs, backups and rollback behavior.

Hosted interfaces: [lab](https://mfd.aklein.fr), [Grafana](https://mfd-grafana.aklein.fr), [Prometheus](https://mfd-prometheus.aklein.fr).

## Design

| Component | Role |
| --- | --- |
| Go | API, embedded UI, workers and decimal valuation |
| PostgreSQL | Durable runs, decisions and transactional outbox; future accounting ledger |
| NATS JetStream | Persistent work queue and shared pull consumer |
| Redis | Disposable cache; never the book of record |
| eToro demo adapter | Official aggregate portfolio read, encrypted snapshots and visible access status |
| Prometheus / Grafana | Product and pipeline metrics, local alert rules, portfolio/decision and operations dashboards |

Use Kafka when measured throughput, retention or integrations justify it. Start with a smaller queue. Keep broker account capital, attributed sleeves and independent simulations separate. Keep model judgments separate from deterministic arithmetic and authorization.

- [Research: eToro, Jev, QuantDinger and related engines](docs/research/2026-09-25-foundations.md)
- [Architecture and data flow](docs/architecture.md)
- [Portfolio accounting, decisions and strategy evaluation](docs/portfolio-and-decisions.md)
- [Infrastructure decision](docs/adr/0001-small-durable-core.md)
- [Monitoring and recovery plan](docs/observability.md)
- [Milestones and acceptance gates](docs/roadmap.md)
- [HTTP contract](api/openapi.yaml)

## Agent/API entry point

```sh
curl -sS http://localhost:8088/api/v1/replays \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: my-first-replay' \
  -d '{"name":"Tech dip","final_shock_bps":{"AAPL":-500,"MSFT":-1000},"review_threshold_bps":100}'
```

Follow the returned `status_url`. Reusing the same key and experiment returns the same run; reusing it with changed inputs returns 409. `GET /api/v1/runs` lists the latest 50; `GET /api/v1/runs/{id}` returns input snapshots, checks and decisions. `GET /api/v1/operations` reads queue, database and recent-job state. `GET /api/v1/etoro/status` shows demo access without account values; the operator-token protected sync and snapshot endpoints are in the [HTTP contract](api/openapi.yaml). `/healthz`, `/readyz` and `/metrics` expose runtime state. Scoped agent tokens and an MCP wrapper are planned.

## eToro connectivity check

`scripts/etoro_probe.py` checks four official read-only endpoints (watchlists plus three demo portfolio views). Set `ETORO_API_KEY` and `ETORO_DEMO_USER_KEY` in a private local environment, then run `python3 scripts/etoro_probe.py`. It prints only status and shape, never account values. The latest supplied user key is stored in this Mac's private deployment settings and receives HTTP 403 with a permission/access error on demo portfolio views. [eToro's authentication guide](https://api-portal.etoro.com/getting-started/authentication) says demo account reads require a key generated for the **Demo** environment with **Read** permission. Replacing the key in private settings causes the updater to recreate the app; a successful response is encrypted before PostgreSQL stores it. The eToro tab unlocks account history using a separate operator token from the same private file. Do not commit keys or broker responses. See [verification](docs/verification.md) for results.
