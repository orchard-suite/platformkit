package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestLoggerWritesTheStandardFields(t *testing.T) {
	defer func(zone *time.Location) { time.Local = zone }(time.Local)
	time.Local = time.FixedZone("CST", -6*60*60)
	var buf bytes.Buffer
	log := NewLogger(&buf, "app", "1.2.3", slog.LevelInfo)
	ctx, span := sdktrace.NewTracerProvider().Tracer("t").Start(context.Background(), "op")
	log.InfoContext(ctx, "hello", "n", 1)
	span.End()
	log.Debug("hidden")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("one JSON line: %v: %s", err, buf.String())
	}
	for k, want := range map[string]any{"level": "info", "msg": "hello", "service": "app", "version": "1.2.3", "trace_id": span.SpanContext().TraceID().String()} {
		if line[k] != want {
			t.Fatalf("%s = %v, want %v", k, line[k], want)
		}
	}
	if ts, _ := line["timestamp"].(string); !strings.HasSuffix(ts, "Z") {
		t.Fatalf("the timestamp is UTC: %q", ts)
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "": slog.LevelInfo, "loud": slog.LevelInfo} {
		if got := ParseLevel(in); got != want {
			t.Fatalf("%q: %v", in, got)
		}
	}
}

func TestBuildInfoAndProcessMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("app")
	if err := registerBuildInfo(m, "app", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := registerProcess(m); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			seen[md.Name] = true
			if md.Name == "process.memory.usage" {
				kinds := map[string]bool{}
				for _, dp := range md.Data.(metricdata.Gauge[int64]).DataPoints {
					k, _ := dp.Attributes.Value("process.memory.kind")
					kinds[k.AsString()] = dp.Value > 0
				}
				if !kinds["heap"] || !kinds["rss"] || len(kinds) != 2 {
					t.Fatalf("memory kinds %v", kinds)
				}
			}
			if md.Name == "app.build_info" {
				dp := md.Data.(metricdata.Gauge[int64]).DataPoints[0]
				if v, _ := dp.Attributes.Value("version"); v.AsString() != "1.2.3" || dp.Value != 1 {
					t.Fatalf("build_info %+v", dp)
				}
			}
		}
	}
	for _, name := range []string{"app.build_info", "process.cpu.time", "process.memory.usage"} {
		if !seen[name] {
			t.Fatalf("%s missing from %v", name, seen)
		}
	}
}
