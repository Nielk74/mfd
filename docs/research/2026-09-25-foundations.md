# Ground research — 25 September 2026

Primary documentation and selected source files reviewed before choosing the architecture. Documented capability is not proof that a particular eToro account has access. No broker credentials, trading requests, or paid model calls were used.

## eToro: a viable first adapter, with details to verify

The official public API covers portfolio information, trading and streaming data. Authentication uses an application key, a user key and a UUID request ID. User keys distinguish demo/real and read/write permissions. Start with a verified account and read access. [Introduction](https://api-portal.etoro.com/), [authentication](https://api-portal.etoro.com/core/getting-started/authentication).

The order guide uses `POST /api/v2/trading/execution/orders` and `/api/v2/trading/execution/demo/orders`, with exactly one of amount, units or contracts. Closing refers to a particular broker position; selling a symbol is not necessarily closing its long position. The adapter must retain broker lot IDs. The request UUID is documented for identification; **we found no guarantee that it makes order submission idempotent**. A timeout must enter an unknown state and trigger reconciliation before resubmission. [Order guide](https://api-portal.etoro.com/core/guides/market-orders).

WebSocket subscriptions cover `instrument:<id>` and `private`. Instrument messages include bid/ask, source time and rate ID; the `content` field in the example is itself encoded JSON. Preserve the envelope and decode in two stages. Reconnect must refresh state and reconcile missed activity; do not assume lossless replay. [Overview](https://api-portal.etoro.com/core/websocket/overview), [topics](https://api-portal.etoro.com/core/websocket/topics).

There is a documentation discrepancy: the general rate-limit guide says most reads are 60/minute and executions 20/minute, while the endpoint index lists shared 120/minute market-data quotas and other exceptions. Use an endpoint-group limiter, initially conservative, and record actual 429 behavior in the demo contract tests. Do not multiply a shared quota by the number of workers. [Rate limits](https://api-portal.etoro.com/core/getting-started/rate-limits), [endpoint index](https://api-portal.etoro.com/llms.txt).

The equity guide is USD-specific and includes cash, invested capital, pending orders, copied positions and P&L. Its examples also vary in the spelling of credit/credits. Preserve original responses and verify actual schemas. Do not derive broker equity by blindly summing spot market values: leverage, mirrored holdings and fees change the accounting. [Equity guide](https://api-portal.etoro.com/core/guides/calculate-equity).

eToro also documents Agent Portfolios with scoped tokens. These are broker-managed constructs and may have virtual balances distinct from invested cash. mfd sleeves must not assume a 1:1 mapping to them. Treat integration as a later capability with explicit scaling and reconciliation tests. [Agent Portfolio v2](https://api-portal.etoro.com/api-reference/agent-portfolios/create-agent-portfolio-v2).

Unverified until an account-backed demo spike: region and instrument availability, short/CFD constraints, full/partial fill behavior, cancel races, transaction history completeness, rate-limit headers, session limits, corporate actions, data storage/redistribution rights and Agent Portfolio balance semantics. Store private data locally; review provider terms before sharing datasets.

## Jev / System One

TypeSafe describes Jev as a model for typed decisions: Choice, Score and Noul questions evaluated against supplied state. This can avoid turning free-form prose into an executable command. It does not establish trading profitability. mfd will support it behind a provider interface alongside deterministic strategies. [Introduction](https://docs.typesafe.ai/introduction).

The HTTP API accepts a state, model and named questions at `POST https://api.typesafe.ai/v1/systemone`. Results include the resolved model, answers and usage. A Go adapter can use ordinary HTTP; a Python service is unnecessary. Pin a model when available and always record the returned version, full distribution, question version, input hash, latency, usage and errors. Validate output types, option membership and probability totals. [API](https://docs.typesafe.ai/api).

Confidence summarizes the answer distribution. It is **not the probability a trade makes money**. Measure calibration against a defined outcome and horizon, separately from returns. Start in shadow mode, compare against the same strategy without the model, and include inference cost and delay. [Confidence](https://docs.typesafe.ai/confidence).

The vendor explicitly reports numeric, temporal, indirection and adversarial-input weaknesses. All arithmetic, exposure limits, timestamp comparisons and execution authorization stay in deterministic code. External news is untrusted evidence, never a source of tool permissions. [Jev 1.13 limitations](https://docs.typesafe.ai/model-jaggedness/jev-1.13).

## Projects worth learning from

| Project | Evidence reviewed | Useful lesson for mfd | Boundary |
| --- | --- | --- | --- |
| [QuantDinger](https://github.com/OpenByteInc/QuantDinger) | README; AI filter; execution event processor, commit `a9c722e024760c490e9f348fd0cca333b170b8c8` | Decision timelines; separate execution events and projections; structured optional Jev checks | Broad Python trading/SaaS scope. Do not copy its business surface into a personal lab. |
| [NautilusTrader](https://github.com/nautechsystems/nautilus_trader) | Project documentation | Event-driven research/live architecture and explicit execution simulation | Rust/Python engine, not a Go or eToro integration shortcut. |
| [LEAN](https://github.com/QuantConnect/Lean) | Project documentation | Separate algorithm, data, brokerage and portfolio responsibilities | C#/Python; integration and data access still need work. |
| [Jev Realtime](https://github.com/rthomas24/jev-realtime-trading) | README | Display each check, result, cost and action together | Paper-only demonstration; simulated screenshots are not performance evidence. |

Specific QuantDinger finding: [`ai_decision_filter.py`](https://github.com/OpenByteInc/QuantDinger/blob/a9c722e024760c490e9f348fd0cca333b170b8c8/backend_api_python/app/services/ai_decision_filter.py#L134) tries Jev and then an LLM; errors can ultimately produce an allowed entry. Low confidence also enters the exception path. This is a behavior of that optional filter, not a claim that all project risk controls fail open. mfd's chosen policy: a required model gate failure blocks new exposure; protective exits have a separate deterministic path. The reviewed [`processor.py`](https://github.com/OpenByteInc/QuantDinger/blob/a9c722e024760c490e9f348fd0cca333b170b8c8/backend_api_python/app/services/execution_streams/processor.py) also illustrates durable execution projection and transaction boundaries. No third-party implementation was copied.

## Storage and queue choices

[Kafka](https://kafka.apache.org/41/getting-started/introduction/) provides partitioned durable event streams. It becomes attractive with long retention, large throughput or a Kafka-dependent integration estate. None is established here yet.

[NATS JetStream pull consumers](https://docs.nats.io/learn/jetstream/pull-consumers) support explicit acknowledgments and bounded worker consumption. Choose it for the initial queue. Delivery may repeat; database uniqueness and transactional effects remain mandatory. Queue acknowledgment does not make an external brokerage order exactly-once.

[Redis Streams](https://redis.io/docs/latest/develop/data-types/streams/) could combine cache and queue, but pending recovery and durable configuration need explicit handling. Choose Redis only for disposable quote/cache state initially. Neither Redis nor the message broker is the financial book of record. PostgreSQL holds runs, decisions, accounting and reconciliation history.

These are architecture choices based on the anticipated workload, not a benchmark result. Revisit after measuring backlog, ingest rates, retention and operating cost.
