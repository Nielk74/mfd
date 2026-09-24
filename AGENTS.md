# Working on mfd

Read README.md, docs/architecture.md and docs/roadmap.md first. Keep implemented behavior distinct from planned behavior.

- Go owns pricing, accounting, risk and execution. Model output is an input, never authorization.
- Preserve input/version provenance and HOLD/blocked decisions. No invented results or unexplained mock data.
- Keep cash/quantity arithmetic exact. Decimal values cross APIs as strings.
- eToro is the only planned portfolio/execution provider. Do not use private browser APIs.
- No credentials, account payloads or private datasets in Git, prompts, fixtures or browser storage.
- Queue delivery can repeat. Persist effects before ack; reconcile ambiguous broker submissions before retrying.
- Human and agent actions use the same permission checks and visible activity history.
- Keep documentation concise and product language concrete. No performance claims without reproducible evidence.
- Run `make check` and, for pipeline changes, `make up && make smoke`. Report limitations honestly.
- Do not introduce live execution merely to make a demo test pass.
