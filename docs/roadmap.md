# Delivery plan

## 0 — Foundation (this repository)

Research, architecture decisions, accounting/decision model, API contract and an executable fixture replay. Compose runs Go, PostgreSQL, Redis and NATS. The lab displays queue status, immutable replay results, sleeve marks and decision evidence. Optional Prometheus/Grafana provides initial infrastructure visibility. No real market feed, broker integration, model integration or order execution is enabled.

The fixture is two fixed USD sleeves across three artificial quote observations. It is a pipeline demonstration, not a backtest or paper trading engine. It has no fills, fees, rebalancing or real strategy performance claim.

## 1 — eToro read-only vertical slice

Resolve official REST/WebSocket schemas and capabilities with demo/read credentials. Add metadata, quote streaming, snapshot import, raw payload retention, gap detection and broker/local reconciliation. In the UI, show actual positions and provenance; keep copied/leveraged/unsupported products visible but explicitly unpriced internally.

Done when: sanitized contract fixtures cover current schemas; demo disconnect/reconnect and quota tests pass; portfolio totals reconcile; unsupported products cannot enter execution. Needs user-owned eToro access, supplied outside the repository.

## 2 — Accounting and virtual sleeves

Implement balanced journal, lot ownership, unassigned holdings, cash/quantity reservations, paired transfers, fee/corporate-action treatment and exact-decimal valuation. Add sleeve editor, funding history, attribution and reconciliation inbox.

Done when: property tests prove conservation of cash/units/fees, no negative available capacity, and identical reconstructed projections; manual broker trades reconcile; migrations and backup restore are tested.

## 3 — Strategy lab and paper execution

Version registry, run scheduler, datasets, realistic fill/cost model, run cancellation, progress, replay artifacts, parameter sweeps, baselines and comparison charts. Add general retry/dead-letter management and quotas.

Done when: deterministic replays match; look-ahead checks pass; costs and partial fills are tested; independent simulation capital never enters account totals.

## 4 — Agent and Jev evaluation

Scoped agent tokens and typed operation API; optional MCP adapter; Jev HTTP adapter, output validation, pinned question/model versions, cost controls, shadow runs and outcome calibration. Deterministic baseline remains available.

Done when: malformed/late/provider-error results create visible abstentions, untrusted content cannot grant permissions, and the model's incremental value is measured out of sample after costs.

## 5 — eToro demo execution

Capability-aware submit/cancel/close adapter, idempotent local intents, reconciliation of ambiguous requests, fill ingestion, account serialization, kill controls and proposal review.

Done when: timeout-after-accept, duplicate events, partial fills, cancel races, manual trades and reconnect drills pass; complete decision → intent → fill → ledger trail is queryable.

## 6 — Limited live operation

Separate executor credentials/process, authenticated UI/API, bounded capital mandates, explicit live activation, alert routing, backup/restore, recovery rehearsal and security review. Start with supported unlevered products and narrow scope.

Done when: operational owner accepts evidence from prior gates, live limits are configured, and a small controlled end-to-end reconciliation succeeds. Building the repository does not enable this mode.

## Later, when justified

Multi-user ownership, broker Agent Portfolios, additional licensed data sources, complex products, object-store history, heavier charting and distributed research workers. Other portfolio/execution brokers remain out of scope until requested.
