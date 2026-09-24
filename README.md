# mfd

**metro finance dodo** — a Go lab for portfolios, strategies and traceable trading decisions.

See what you own, what a strategy decided, and what actually happened. Humans and agents will use the same recorded operations. eToro is the first and only planned portfolio/execution provider.

## Start

Requires Docker with Compose. No API keys needed for the fixture lab.

```sh
docker compose up -d --build --wait
```

Open **http://localhost:8088** and select **Run fixture replay**. This sends a durable job through PostgreSQL → NATS → a bounded Go worker pool, calculates exact-decimal marks, and records six decisions across two virtual sleeves. Results survive restarts. Redis holds a disposable run-result cache.

The data is artificial. Holdings stay fixed. This is an executable foundation, **not yet a backtester, paper broker or live trading system**. It makes no market, model or broker calls. See [delivery plan](docs/roadmap.md) for explicit milestones.

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
| Prometheus / Grafana | Optional metrics, local alert rules and provisioned dashboard |

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
  -H 'Idempotency-Key: my-first-replay' -d '{}'
```

Follow the returned `status_url`. Reusing the same key returns the same run. `GET /api/v1/runs` lists the latest 50; `GET /api/v1/runs/{id}` returns input snapshots, checks and decisions. `/healthz`, `/readyz` and `/metrics` expose runtime state. These endpoints are implemented; scoped agent tokens and an MCP wrapper are planned.

## eToro connectivity check

`scripts/etoro_probe.py` performs one read-only request to the official demo P&L endpoint. Set `ETORO_API_KEY` and `ETORO_DEMO_USER_KEY` in a private local environment, then run `python3 scripts/etoro_probe.py`. It prints only status/shape and never saves account values. Do not commit keys or broker responses. This probe is not an integrated portfolio adapter. See [verification](docs/verification.md) for current results.
