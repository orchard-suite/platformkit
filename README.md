# platformkit

The shared plumbing every platform app is built from, so a rule is written
once and holds everywhere. A Go module of five packages:

| Package | What it does |
| --- | --- |
| `door` | Reads the person the front door signed in (`X-Forwarded-Email`, `X-Forwarded-Groups`) and gates each route on the app's roles. A role is the bare role: the door sends `Admin` for the app role `ledger.Admin`, and `NewGate` refuses a role with the app's part still on it. Also `SameOrigin` for changes. |
| `telemetry` | The telemetry standard in one call: `Setup` installs OTLP traces and metrics when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, `<service>.build_info`, and `process.cpu.time` and `process.memory.usage`. `NewLogger` writes JSON lines with `trace_id`. `Handler` serves a port with a span and `http.server.request.duration` per route; `Client` makes outbound calls client spans. |
| `faro` | The browser relay, `POST /api/telemetry`: caps a batch at 256 KB, names the app `<service>-web`, stamps the person a gate admitted or removes any user, relays with the key, and counts `<service>.browser.batches` by outcome. |
| `orrery` | The catalog hub's client for apps, pushers, runners, and shell jobs: paged reads with filters and typed field readers (`Record`), writes with retries (`Put`, `Patch`, `Delete`), snapshot and incremental runs, blueprint offers, catalog files, heartbeats, action run reports, and custom icons (`PutIcon`, `Icon`). `Describe` writes the app's own `orrery.yaml` (its `sw_service`, the `sw_api`s it provides, the APIs it calls, and optionally the fields and relations that place it in the estate's own scheme) to the Orrery it runs beside. |
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

An app with its own logo names it in `orrery.yaml`, both keys or
neither. The name is a custom icon name (`^[a-z0-9][a-z0-9_]{0,62}$`, not
one of the hub's built-in names); the file is a path from the root of the
FS the descriptor is read from, an `.svg`, `.png`, or `.webp` of at most
256 KiB:

```yaml
service: github/acme/tally/cmd/tally
icon:
  name: tally
  file: logo.svg
```

The app then embeds both files and reads them with `ParseDescriptorFS`.
`ParseDescriptor` refuses a descriptor that names an icon, since it has no
file to read:

```go
//go:embed orrery.yaml logo.svg
var Files embed.FS

d, err := orrery.ParseDescriptorFS(myapp.Files, "orrery.yaml")
```

Describe uploads the icon first (`PUT /v1/icons/{name}`) and then sets
the service's icon to its name. A refused upload fails the describe.

An API it provides may name its definition with `spec`: a path from the
repository's root, or a full URL.
`SpecURL` turns a path into the file's link on the default branch
(`https://github.com/<owner>/<repo>/blob/HEAD/<path>`), so only a service
keyed by its GitHub path can name one; conform reads the definition from
that link. Kinds are `rest`, `graphql`, `grpc`, `event`, `mcp`, and `web`.

It patches the service, so the fields another source writes stay that
source's, puts each API it provides, and never removes anything. It writes
again every six hours, and after a failure with a growing wait, counting
`<service>.orrery.describe` by outcome. Every app reads the same two
settings, `ORRERY_URL` and `ORRERY_PUSH_TOKEN`, a source token whose source
is registered for `service` and `api`.

The client reads and writes icons directly too. `PutIcon(ctx, name,
contentType, data)` creates or replaces a custom icon with a token that
may write entities. `Icon(ctx, name, etag)` reads the custom icon, else the
built-in brand file of that name, as an `Image` (`Type`, `Data`, `ETag`,
`NotModified`). Pass the last `ETag` to revalidate: an unchanged icon comes
back with `NotModified` set and no data. A name the hub has no file for is
`ErrIconNotFound`. `IconFor(ctx, name, theme, etag)` reads the copy drawn for a
theme: `ThemeLight` keeps a black brand mark dark for a light background,
where `Icon` gives the hub's default, the same mark in light ink.
