# platformkit

The Go module every platform app imports for the front door, telemetry, the
browser relay, and the catalog, ledger, and incident desk clients. Read `README.md` first.

## Rules

- Each package is a contract other repositories build on. A breaking change
  to an exported name is a `feat!` and every app moves with it.
- No package reads the environment except `telemetry.Setup`, which follows
  the OpenTelemetry variables. An app reads its own settings and passes
  them in.
- Metric names are `<service>.<what>` with the app's service name, and
  attributes stay low-cardinality: never a person, an id, or a path.
- Never log a person's email or a token. Log both branches of a decision
  about who is let in.
- `ledger/event.go` is the ledger's contract. Its limits and reasons change
  only together with the ledger's published schema.
- Tests are focused and need no network. No real names or emails in them.
- No em dashes or en dashes in prose.
- `VERSION` is the version of record; never edit it, the manifest, or
  `CHANGELOG.md` by hand.

## Consumers

Every app vendors this module (README, Use). A release here reaches an app
only when the app bumps it with `go get` and `go mod vendor` and commits
both.

## Delivery

Agents push to main.

Commit on `main` and land with `make land`. CI versions every `feat` and
`fix` and tags it `vX.Y.Z`, which is the version `go get` fetches.

## Checks

```bash
make check
```
