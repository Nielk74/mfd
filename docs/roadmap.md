# Delivery plan

## 0 — Foundation (this repository)

Research, architecture decisions, accounting/decision model, API contract and an executable fixture replay. Compose runs Go, PostgreSQL, Redis and NATS. The lab displays queue status, immutable replay results, sleeve marks and decision evidence. Optional Prometheus/Grafana provides initial infrastructure visibility. The separate eToro account and Demo order test slices are described below; there is no streaming market feed, model integration or Real order execution.

The research scenario is two fixed USD sleeves across three simulated price observations. Named experiments can shock the final AAPL/MSFT prices and adjust the review threshold. The lab compares runs, exports valuations and reads queue/database state. Two Grafana boards separate portfolio/decision aggregates from operations. It is still a pipeline and valuation demonstration, not a backtest or paper trading engine. Scenario runs have no fills, fees, rebalancing or real strategy performance claim. The separately guarded Demo probe tracks its own broker order state.

## 1 — eToro read-only vertical slice

Resolve official REST/WebSocket schemas and capabilities with demo/read credentials. Add metadata, quote streaming, snapshot import, raw payload retention, gap detection and broker/local reconciliation. In the UI, show actual positions and provenance; keep copied/leveraged/unsupported products visible but explicitly unpriced internally.

Implemented slice: official Demo and Real aggregate clients, environment detection from schema-valid reads, separate encrypted snapshot histories, scheduled/manual sync, public access status, and operator-token protected account views. A Demo + Read key has now produced an encrypted Demo snapshot. The provider omitted a timezone offset, so the importer explicitly flags its UTC assumption. The previously active Real key was revoked and currently returns 401; earlier Real snapshots remain separate. The adapter validates a small set of account totals and asset aggregates; it does not infer missing values. The Demo probe adds on-demand instrument lookup and bid/ask reads, but no continuous feed. Remaining: streaming quotes, full broker lot import, general reconciliation, disconnect/quota tests and independently verified totals.

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

Implemented narrow slice: a fixed $100 unlevered BTC strategy with provider account/quote/eligibility/cost gates, an operator-token protected preview and explicit submit action, a private execution switch, durable intent and encrypted response evidence, and lookup by request reference with returned-order-ID fallback. A 200 submission remains `accepted` until a broker lookup confirms a fill or rejection. The first limit-IOC attempt was rejected by the broker; the separately versioned market-order probe retained the $100 notional and fresh-quote gates, had no broker price limit, and produced one confirmed Demo fill. HOLD and unknown outcomes remain visible; unknown submissions are never automatically retried. The path is hardcoded to eToro Demo. This does not yet include a general order builder, cancel/close, a balanced fill ledger, virtual sleeve ownership or operator identity beyond the shared token.

Done when: timeout-after-accept, duplicate events, partial fills, cancel races, manual trades and reconnect drills pass; complete decision → intent → fill → ledger trail is queryable.

## 6 — Limited live operation

Separate executor credentials/process, authenticated UI/API, bounded capital mandates, explicit live activation, alert routing, backup/restore, recovery rehearsal and security review. Start with supported unlevered products and narrow scope.

Done when: operational owner accepts evidence from prior gates, live limits are configured, and a small controlled end-to-end reconciliation succeeds. Building the repository does not enable this mode.

## Later, when justified

Multi-user ownership, broker Agent Portfolios, additional licensed data sources, complex products, object-store history, heavier charting and distributed research workers. Other portfolio/execution brokers remain out of scope until requested.
