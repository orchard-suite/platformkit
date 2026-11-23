# platformkit

The shared plumbing every platform app is built from, so a rule is written
once and holds everywhere. A Go module of five packages:

| Package | What it does |
| --- | --- |
| `door` | Reads the person the front door signed in (`X-Forwarded-Email`, `X-Forwarded-Groups`) and gates each route on the app's roles. A role is the bare role: the door sends `Admin` for the app role `ledger.Admin`, and `NewGate` refuses a role with the app's part still on it. Also `SameOrigin` for changes. |
| `telemetry` | The telemetry standard in one call: `Setup` installs OTLP traces and metrics when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, `<service>.build_info`, and `process.cpu.time` and `process.memory.usage`. `NewLogger` writes JSON lines with `trace_id`. `Handler` serves a port with a span and `http.server.request.duration` per route; `Client` makes outbound calls client spans. |
| `faro` | The browser relay, `POST /api/telemetry`: caps a batch at 256 KB, names the app `<service>-web`, stamps the person a gate admitted or removes any user, relays with the key, and counts `<service>.browser.batches` by outcome. |
| `orrery` | The catalog hub's client for apps, pushers, runners, and shell jobs: paged reads with filters and typed field readers (`Record`), writes with retries (`Put`, `Patch`, `Delete`), snapshot and incremental runs, blueprint offers, catalog files, heartbeats, and action run reports. `Describe` writes the app's own `orrery.yaml` (its `sw_service`, the `sw_api`s it provides, the APIs it calls, and optionally the fields and relations that place it in the estate's own scheme) to the Orrery it runs beside. |
| `ledger` | The agent ledger's event contract in Go (types, limits, `Validate`) and a client that validates before it posts. A nil client posts nothing. |

## Use

The module is private. An app vendors it, so its CI and image build need no
access to this repository:

```bash
GOPRIVATE=github.com/dsswift/* go get github.com/orchard-suite/platformkit@vX.Y.Z
go mod vendor
```

Commit `vendor/`. The image build copies it and builds with `-mod=vendor`;
it never runs `go mod download`.


```go
shutdown, err := telemetry.Setup(ctx, "myapp", version)
log := telemetry.NewLogger(os.Stdout, "myapp", version, telemetry.ParseLevel(os.Getenv("LOG_LEVEL")))
gate, err := door.NewGate(log, "Reader", "Admin")
relay, err := faro.NewRelay(faro.Config{Service: "myapp", Version: version, URL: os.Getenv("FARO_URL"), Key: os.Getenv("FARO_API_KEY")}, log)

mux := http.NewServeMux()
mux.Handle("GET /{$}", gate.Func(page))
mux.Handle("POST /api/telemetry", gate.Wrap(relay))
http.ListenAndServe(":8080", telemetry.Handler(mux, "session"))
```

Inside a gated handler, `door.From(r.Context())` is the person, and
`Has("Admin")` asks for a role.

An app describes itself from the `orrery.yaml` at its repository root
when its estate runs Orrery. A file at the root of the module embeds it,
since `go:embed` reaches no parent directory:

```go
// orrery.go, at the repository root
package myapp

import _ "embed"

//go:embed orrery.yaml
var Descriptor []byte
```

```go
// in main

if u, t := os.Getenv("ORRERY_URL"), os.Getenv("ORRERY_PUSH_TOKEN"); u != "" && t != "" {
	d, err := orrery.ParseDescriptor(myapp.Descriptor)
	describer, err := orrery.NewDescriber(orrery.New(u, t), d, "myapp", log)
	go describer.Run(ctx)
}
```

It patches the service, so the fields another source writes stay that
source's, puts each API it provides, and never removes anything. It writes
again every six hours, and after a failure with a growing wait, counting
`<service>.orrery.describe` by outcome. Every app reads the same two
settings, `ORRERY_URL` and `ORRERY_PUSH_TOKEN`, a source token whose source
is registered for `service` and `api`.
