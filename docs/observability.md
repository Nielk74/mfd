# Observability

Portfolio correctness and decision visibility come before CPU dashboards. Metrics are aggregates; individual decisions remain queryable records in PostgreSQL and the lab.

## Product monitoring plan

| Signal | User view / response |
| --- | --- |
| Quote/FX age, missing marks, feed gap | Badge every affected valuation; block new exposure |
| Broker/local cash, lot and fee differences | Reconciliation inbox with evidence and resolution owner |
| Unknown submissions and aged reservations | Execution incident; freeze affected intent, reconcile |
| Strategy returns, drawdown, exposure and costs | Same-period baseline comparison; distinguish modes |
| Model failures, abstentions, calibration and spend | Decision journal; gate failures block new exposure |
| Queue delay, retries, dead letters, stale jobs | Job progress and intervention controls |
| Policy blocks and human overrides | Searchable timeline with actor and before/after state |

Initial target objectives, to validate with actual provider limits: internal accepted work visible immediately; fixture replay completes within 30 seconds; all decisions have input and version lineage; no unexplained accounting difference may pass live promotion. Market freshness thresholds must be instrument/calendar-specific, not a universal five-second rule.

## Infrastructure

Implemented: structured JSON process logs, `/healthz`, dependency `/readyz`, Prometheus `/metrics`, [portfolio and decisions](https://mfd-grafana.aklein.fr/d/mfd-lab) and [queue/database operations](https://mfd-grafana.aklein.fr/d/mfd-operations) dashboards, plus local alert rules. The lab's **Queues & database** view reads dependency probes, the NATS consumer, PostgreSQL run counts, outbox and recent job timestamps through `GET /api/v1/operations`.

Metrics include run state, queue depth and redelivery, outbox count/age, oldest queued age, dependency readiness, storage size, latest run duration and the latest completed fixture's sleeve equity, unrealized mark-to-cost and decision counts. The eToro metrics contain only connector configuration, read success, HTTP status and encrypted snapshot count; account values are never exported to Prometheus. Product series are explicitly named `mfd_fixture_*`; they are absent until a fixture run completes. Their time axes show when experiments completed, not simulated investment performance. Prometheus may round numeric samples for display; PostgreSQL retains exact decimal strings.

Rules detect an unavailable scrape/dependency, a queued run or unpublished outbox job older than one minute, and a change in failed-run count. The rules are local only; no notification destination is configured.

Next: OpenTelemetry trace propagation through HTTP, outbox, NATS and execution; PostgreSQL/Redis/NATS exporters; disk capacity; backup age/restore outcomes; model/provider HTTP latency and rate limits. Keep run, order, user and instrument IDs out of metric labels; retain them in logs/traces/database records. Redact credentials and private response bodies at the boundary.

Alert routing is intentionally unconfigured. Local rules can show failures; there is no claim that someone will be paged. Before live use, assign an owner and configure/test a notification destination.

## Failure drills and runbooks

1. Stop NATS: enqueue must persist in PostgreSQL; restart drains the outbox without duplicate results.
2. Kill a worker after commit before ack: redelivery sees completed run and acknowledges it.
3. Lose Redis: durable views remain readable; readiness degrades; caches can rebuild.
4. Lose PostgreSQL: refuse writes and stop acknowledging effects; never substitute cache for truth.
5. Disconnect broker feed: identify gap, pause new exposure, refresh snapshots and reconcile.
6. Timeout an order: retain unknown state and reservation; do not blindly submit again.
7. Restore backup into a separate environment and verify journal totals and projections before reconnecting a broker.

The fixture smoke test covers the normal pipeline and idempotency. Broker drills, restore tests and general dead-letter operations remain promotion requirements.
