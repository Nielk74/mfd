# ADR 0001 — Small durable core

Date: 2026-09-25. Status: accepted for the foundation; revisit with measured workload.

**Decision:** Go modular application, PostgreSQL, Redis, NATS JetStream and Docker Compose. Embedded web UI and versioned HTTP API. Optional Prometheus/Grafana profile.

PostgreSQL supplies transactions and durable queryable history. Redis is disposable cache. NATS supplies durable work delivery with explicit acknowledgments. An outbox closes the database/publish gap; unique effect IDs close the redelivery gap. Neither closes the external broker submission gap, which needs reconciliation.

Alternatives: SQLite is attractive for a single process, but concurrent workers and relational portfolio queries justify PostgreSQL here. Redis Streams could remove NATS but couples cache and work retention/failure behavior. Kafka or Redpanda adds a Kafka ecosystem before we have a requirement for it. In-process queues alone lose pending work across restarts. A separate service for every module would multiply deployment and debugging work.

Costs: four core containers; backups and migrations are real work. Single-node Compose has no failover guarantee. We must test at-least-once behavior rather than claiming exactly-once execution.

Change this decision if measured throughput/retention needs partitioned long-lived streams, if third-party tooling requires Kafka, or if the project becomes a desktop-only application. See [research](../research/2026-09-25-foundations.md).
