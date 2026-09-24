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

The initial key produced HTTP 403 on the official demo P&L endpoint; its response looked like an edge rejection. With the next supplied key, the watchlists endpoint returned HTTP 200, while demo portfolio and P&L returned HTTP 403 with an access/permission error. The latest supplied key also returned HTTP 403 with the same error category on the three documented demo reads: [portfolio breakdown](https://api-portal.etoro.com/api-reference/trading--demo/get-demo-portfolio-breakdown), [aggregated portfolio](https://api-portal.etoro.com/api-reference/trading--demo/get-aggregated-portfolio-snapshot) and [P&L](https://api-portal.etoro.com/api-reference/trading--demo/get-account-pnl-and-portfolio-details). Only GET requests were made. No account values or orders were saved, and credentials and response bodies are absent from the repository.

The error does not establish which specific permission is missing. [eToro's authentication guide](https://api-portal.etoro.com/getting-started/authentication) says a user key must be generated for the Demo environment with Read permission to access demo account data. The lab therefore remains in fixture mode. The next broker milestone needs a successful authenticated demo read and sanitized contract fixtures.

## Interactive lab and monitoring extension

- Isolated Compose project `mfd-next` built and started with its own PostgreSQL/NATS volumes, ports and Grafana, leaving the automatic `mfd` deployment untouched.
- A named MSFT +10% final quote shock produced exact experiment-sleeve equity of $4,227.50 and combined equity of $10,207.50; earlier observations and the unshocked core sleeve were unchanged.
- Concurrent baseline requests still created one run. Reusing an idempotency key with different experiment inputs returned HTTP 409. Queue state, recent jobs and synthetic product metrics matched durable results.
- Browser checks at 1440×1050 and 390×844 created a Tech dip experiment, displayed $9,703.50, compared runs, filtered the decision journal, opened the captured snapshot, read queue/database state and downloaded valuation CSV. No page exceptions or horizontal overflow occurred.
- Both provisioned Grafana dashboards loaded, and Prometheus returned the new synthetic equity, readiness and queue series. Stat panels were corrected to use instant queries after the first browser check showed missing data.
- The isolated broker adapter used dummy credentials. Its background read recorded HTTP 401 without storing an account snapshot; unauthenticated snapshot access returned HTTP 401, while the operator-token protected view returned an empty history. The adapter tests also checked official request headers, exact decimal parsing, missing-field rejection, provider-error sanitization and authenticated encryption/tamper detection. A manual sync route has a response deadline longer than the provider request timeout.
- `promtool` accepted all five alert rules; both dashboards rendered in a browser without page errors. The eToro tab passed desktop/mobile checks with no horizontal overflow.

## Network deployment and updates

The Mac publishes the lab, Grafana, Prometheus, PostgreSQL, Redis and NATS on `0.0.0.0`. All seven published ports were reached through its LAN address. Browser replay checks passed at desktop/mobile widths through both LAN HTTP and `https://mfd.aklein.fr`. Caddy serves the lab, Grafana and Prometheus with valid HTTPS; its pre-existing routes were preserved and a configuration snapshot saved.

The installed cron updater was observed waiting for CI on commit `8dd183d`, then building and deploying it without a manual deployment command. The public health endpoint reported that exact compiled commit. A replay created before the deployment remained queryable afterward. The updater saved a PostgreSQL backup, retained named volumes and left unrelated cron entries unchanged.

Updater tests cover exact-SHA push CI gating, latest failed-run rejection, cron installation idempotency, exclusive locking and rollback after failed candidate health. Reboot recovery is configured but an actual Mac reboot was not performed. Container rollback does not automatically reverse database migrations. See [deployment operations](deployment.md).

## Limits

This does not validate real strategy performance, broker accounting, execution, Jev integration, full backup restoration or high availability. Fixture observations are artificial and do not represent live or historical market results. Authentication, a complete financial ledger and general job administration are planned in the [delivery plan](roadmap.md).
