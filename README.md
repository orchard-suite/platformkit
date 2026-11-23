# platformkit

The shared plumbing every platform app is built from, so a rule is written
once and holds everywhere. A Go module of six packages:

| Package | What it does |
| --- | --- |
| `door` | Reads the person the front door signed in (`X-Forwarded-Email`, `X-Forwarded-Groups`) and gates each route on the app's roles. A role is the bare role: the door sends `Admin` for the app role `annals.Admin`, and `NewGate` refuses a role with the app's part still on it. Also `SameOrigin` for changes. |
| `telemetry` | The telemetry standard in one call: `Setup` installs OTLP traces and metrics when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, `<service>.build_info`, and `process.cpu.time` and `process.memory.usage`. `NewLogger` writes JSON lines with `trace_id`. `Handler` serves a port with a span and `http.server.request.duration` per route; `Client` makes outbound calls client spans. |
| `faro` | The browser relay, `POST /api/telemetry`: caps a batch at 256 KB, names the app `<service>-web`, stamps the person a gate admitted or removes any user, relays with the key, and counts `<service>.browser.batches` by outcome. |
| `orrery` | The catalog hub's client for apps, pushers, runners, and shell jobs: paged reads with filters and typed field readers (`Record`), writes with retries (`Put`, `Patch`, `Delete`), snapshot and incremental runs, blueprint offers, catalog files, heartbeats, action run reports, and custom icons (`PutIcon`, `Icon`). For a product that works for people: `RunAction` asks for a run in a person's name, `TargetsFor` reads where a person may ask, and `Tools` and `CallTool` list and call lent tools; a tool call is sent once and waits as long as its context allows. An app does not describe itself through it: redgreen reads each repository's `orrery.yaml` and writes the service. |
| `annals` | Annals' event contract in Go (types, limits, `Validate`) and a client that validates before it posts. A nil client posts nothing. |
| `dispatch` | The incident desk's client for a product: `Send` an event to people, groups, or chains, `DeclareKinds` so people can choose how each kind reaches them, `RecordChange`, `Heartbeat`, `OpenIncidents`, `OpenIncident`, and `OnCall`. A nil client sends and reads nothing. |

## Use

An app takes a release by its tag:

```bash
go get github.com/orchard-suite/platformkit/v3@vX.Y.Z
```

An app that vendors its dependencies commits `vendor/` and builds with
`-mod=vendor`.

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

An app does not describe itself to Orrery at runtime. Its `orrery.yaml`
stays at the repository root, and redgreen, the forge reader, reads it from
the repository and writes the service, its APIs, and its logo to the
catalog. An estate lists only services from repositories it reads.

The client reads and writes icons directly. `PutIcon(ctx, name,
contentType, data)` creates or replaces a custom icon with a token that
may write entities. `Icon(ctx, name, etag)` reads the custom icon, else the
built-in brand file of that name, as an `Image` (`Type`, `Data`, `ETag`,
`NotModified`). Pass the last `ETag` to revalidate: an unchanged icon comes
back with `NotModified` set and no data. A name the hub has no file for is
`ErrIconNotFound`. `IconFor(ctx, name, theme, etag)` reads the copy drawn for a
theme: `ThemeLight` keeps a black brand mark dark for a light background,
where `Icon` gives the hub's default, the same mark in light ink.
