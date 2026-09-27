# mfd

**metro finance dodo** — a Go lab for portfolios, strategies and traceable trading decisions.

See what you own, what a strategy decided, and what actually happened. Humans and agents will use the same recorded operations. eToro is the only planned portfolio/execution provider. Its official Demo and Real portfolio read adapters are wired; the active key has confirmed Real read access, while Demo still needs a Demo-scoped key.

## Start

Requires Docker with Compose. No API keys needed to run research scenarios.

```sh
docker compose up -d --build --wait
```

Open **http://localhost:8088**. Create a named experiment from a preset or set the final AAPL/MSFT quote shocks and review threshold. Each run goes through PostgreSQL → NATS → a bounded Go worker pool, calculates exact-decimal marks, and records six decisions across two virtual sleeves. Compare runs, inspect positions and decision inputs, export valuations, and open **Queues & database** to read the current pipeline. Results survive restarts. Redis holds a disposable run-result cache.

Research scenarios use simulated prices and fixed holdings; only the last price observation changes. This is **not yet a backtester, paper broker or live trading system**. The eToro adapter makes separate official, read-only Demo and Real portfolio requests; a 403 never becomes a fabricated account value. No broker order or model call is made. See [delivery plan](docs/roadmap.md) for explicit milestones.

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
| eToro read adapter | Official Demo and Real aggregate portfolio reads, separate encrypted histories and visible access status |
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

Follow the returned `status_url`. Reusing the same key and experiment returns the same run; reusing it with changed inputs returns 409. `GET /api/v1/runs` lists the latest 50; `GET /api/v1/runs/{id}` returns input snapshots, checks and decisions. `GET /api/v1/operations` reads queue, database and recent-job state. `GET /api/v1/etoro/status` shows Demo and Real access without account values; operator-token protected sync and snapshot endpoints select `environment=demo|real` in the [HTTP contract](api/openapi.yaml). `/healthz`, `/readyz` and `/metrics` expose runtime state. Scoped agent tokens and an MCP wrapper are planned.

## eToro connectivity check

Set `ETORO_API_KEY` with either or both `ETORO_DEMO_USER_KEY` and `ETORO_REAL_USER_KEY` in a private environment. `python3 scripts/etoro_probe.py` checks the official Demo and Real aggregate endpoints and prints only status and shape; it exits nonzero if a key resolves to the wrong slot. A successful, schema-valid read confirms the key's environment; if misplaced, the importer switches endpoints and labels the account accordingly. Both user keys supplied so far resolve to Real, for different provider accounts. One remains inactive in private settings to avoid mixing account histories; a Demo + Read key is still needed for Demo snapshots. Successful raw responses are encrypted before PostgreSQL stores them. The eToro tab unlocks each account history with a separate operator token held only in tab memory. No account amounts enter Prometheus. Do not commit keys or broker responses. See [verification](docs/verification.md) for results.
