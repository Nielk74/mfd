# Verification — through 27 September 2026

## Local results

- Go tests with the race detector and `go vet` passed.
- Exact fractional valuation, liquidation marks, fixture reproducibility and combined sleeve totals checked.
- Missing, crossed, stale, future, duplicate and foreign-currency quotes are rejected without returning a partial NAV.
- HTTP tests cover idempotency requirements, malformed bodies, unsupported parameters and browser cross-origin writes.
- Compose built and ran Go, PostgreSQL, Redis and NATS successfully.
- Integration smoke: eight concurrent identical requests create one run; the worker records six snapshots/decisions with final combined equity of $10,005 from simulated holdings.
- Recovery drills: accepted work survives a NATS outage; reconnect drains the outbox; duplicate delivery does not change committed results; Redis loss preserves durable reads; app/database/queue restarts preserve results.
- Browser checks at 1440×1100 and 390×844: replay creation, completed results, sleeve filtering and decision expansion passed; no page overflow or browser exceptions.
- Optional Prometheus/Grafana profile started. Prometheus scraped `up=1`; Grafana served the provisioned `mfd-lab` dashboard.

GitHub CI repeats Go checks, builds the Compose stack, and runs the smoke/recovery scripts. Browser checks were performed locally; they are not part of CI yet.

## eToro account checks

The initial key produced HTTP 403 on the official demo P&L endpoint; its response looked like an edge rejection. With the next supplied key, the watchlists endpoint returned HTTP 200, while demo portfolio and P&L returned HTTP 403 with an access/permission error. The latest supplied key also returned HTTP 403 with the same error category on the three documented demo reads: [portfolio breakdown](https://api-portal.etoro.com/api-reference/trading--demo/get-demo-portfolio-breakdown), [aggregated portfolio](https://api-portal.etoro.com/api-reference/trading--demo/get-aggregated-portfolio-snapshot) and [P&L](https://api-portal.etoro.com/api-reference/trading--demo/get-account-pnl-and-portfolio-details). Only GET requests were made in those probes; no account values or orders were saved then, and credentials and response bodies are absent from the repository.

The Demo error alone did not establish which permission was missing. On 27 September, the same supplied key returned HTTP 200 on the [official Real aggregate endpoint](https://api-portal.etoro.com/api-reference/trading--real/get-aggregated-portfolio-snapshot) and HTTP 403 on the Demo aggregate endpoint. Its Real response had the required aggregate schema. [eToro's authentication guide](https://api-portal.etoro.com/core/getting-started/authentication) says user keys are generated separately for Demo or Real with Read/Write permissions. This confirms Real read scope for the supplied key; a separate Demo + Read key is needed for a Demo account snapshot. Research scenarios remain simulated.

The additional key supplied as Demo was tested against three official Demo portfolio reads on 27 September: breakdown, aggregate and P&L each returned HTTP 403 permission denied. The [Real aggregate read](https://api-portal.etoro.com/api-reference/trading--real/get-aggregated-portfolio-snapshot) returned HTTP 200 with a valid schema. Its provider account identifier differed from the active Real key's identifier; neither identifier nor account amounts were logged. The key is retained as an inactive candidate in private settings, outside the application environment, to avoid mixing two Real accounts in one history. Demo account access remained unverified at that stage.

An isolated Compose deployment with that key still in the Demo settings slot detected and relabeled it as Real. Its database recorded the Demo denial and saved an encrypted Real response; the Real history was available only with the operator token, the Demo history stayed empty, and both environments rejected unauthenticated history reads. Decimal account totals crossed the API as strings. Desktop and 390-pixel mobile browser checks showed the Real read-only banner, no horizontal overflow or page errors, and no token in browser storage. No account values were written to test output or Prometheus. A real Demo key and simultaneous two-key provider reads remained unverified at that stage.

Later on 27 September, a new key returned HTTP 200 on the [official Demo aggregate endpoint](https://api-portal.etoro.com/api-reference/trading--demo/get-aggregated-portfolio-snapshot) and HTTP 403 on Real. The Demo response had complete account totals and empty position arrays, but its timestamp lacked a timezone offset, causing the first public importer attempt to record `schema_error` without saving a snapshot. The parser now accepts that observed timestamp shape, flags its UTC interpretation as an assumption, and retains the exact source response encrypted. A direct live parser check and an isolated Compose run imported one Demo snapshot; the operator-token protected API returned it with `provider_time_assumed_utc=true`. `make check`, `make up` and `make smoke` passed, and the isolated volumes were removed. The configured Real key returned HTTP 401 after the operator revoked it; earlier encrypted Real history was left intact. Simultaneous successful Demo and Real imports still require a valid replacement Real key for the same provider account.

The guarded [Demo BTC probe](demo-btc-probe.md) passed all 21 live provider checks in an isolated Compose project and persisted a `ready` preview. That preview used the current Demo account, its bound provider identity, an exact BTC instrument match, realtime bid/ask rates, account-specific eligibility and a what-if cost estimate. Six source responses were encrypted as six evidence artifacts. The preview test **did not submit** a broker order; its isolated database volume was removed afterward. Unit tests cover hold decisions for low cash, pending orders and wide spread, exact numeric order serialization to the Demo-only path, and the distinction between acceptance and a verified fill. `make check`, isolated `make up` and `make smoke` passed after the execution path was added.

## Interactive lab and monitoring extension

- Isolated Compose project `mfd-next` built and started with its own PostgreSQL/NATS volumes, ports and Grafana, leaving the automatic `mfd` deployment untouched.
- A named MSFT +10% final quote shock produced exact experiment-sleeve equity of $4,227.50 and combined equity of $10,207.50; earlier observations and the unshocked core sleeve were unchanged.
- Concurrent baseline requests still created one run. Reusing an idempotency key with different experiment inputs returned HTTP 409. Queue state, recent jobs and synthetic product metrics matched durable results.
- Browser checks at 1440×1050 and 390×844 created a Tech dip experiment, displayed $9,703.50, compared runs, filtered the decision journal, opened the captured snapshot, read queue/database state and downloaded valuation CSV. No page exceptions or horizontal overflow occurred.
- Both provisioned Grafana dashboards loaded, and Prometheus returned the new synthetic equity, readiness and queue series. Stat panels were corrected to use instant queries after the first browser check showed missing data.
- The first public browser check showed empty charts under Grafana's 24-hour default because the new series had only a few recent scrapes. The one-hour range rendered every panel; that range is now the dashboard default, while Grafana's time selector still permits longer history.
- The isolated broker adapter used dummy credentials. Its background read recorded HTTP 401 without storing an account snapshot; unauthenticated snapshot access returned HTTP 401, while the operator-token protected view returned an empty history. The adapter tests also checked official request headers, exact decimal parsing, missing-field rejection, provider-error sanitization and authenticated encryption/tamper detection. A manual sync route has a response deadline longer than the provider request timeout.
- `promtool` accepted all five alert rules; both dashboards rendered in a browser without page errors. The eToro tab passed desktop/mobile checks with no horizontal overflow.

## Network deployment and updates

The Mac publishes the lab, Grafana, Prometheus, PostgreSQL, Redis and NATS on `0.0.0.0`. All seven published ports were reached through its LAN address. Browser replay checks passed at desktop/mobile widths through both LAN HTTP and `https://mfd.aklein.fr`. Caddy serves the lab, Grafana and Prometheus with valid HTTPS; its pre-existing routes were preserved and a configuration snapshot saved.

The installed cron updater was observed waiting for CI on commit `8dd183d`, then building and deploying it without a manual deployment command. The public health endpoint reported that exact compiled commit. A replay created before the deployment remained queryable afterward. The updater saved a PostgreSQL backup, retained named volumes and left unrelated cron entries unchanged.

Updater tests cover exact-SHA push CI gating, latest failed-run rejection, cron installation idempotency, exclusive locking and rollback after failed candidate health. Reboot recovery is configured but an actual Mac reboot was not performed. Container rollback does not automatically reverse database migrations. See [deployment operations](deployment.md).

## Limits

This does not validate strategy performance, a balanced broker fill ledger, general execution, Jev integration, full backup restoration or high availability. Scenario prices are simulated and do not represent live or historical market results. The Demo BTC probe uses provider-backed observations but has no automatic exit or performance attribution. Individual broker order outcomes are recorded in the protected decision history. Individual identities, a complete financial ledger and general job administration are planned in the [delivery plan](roadmap.md).
