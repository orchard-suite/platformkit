package orrery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gopkg.in/yaml.v3"
)

// Descriptor is what an app says about itself in the orrery.yaml at its
// repository root: the service it is, the APIs it
// provides, and the APIs it calls. It is the software side of the app, the
// same in every estate that runs it.
type Descriptor struct {
	// Service is the service's key: its repository path, such as
	// github/acme/tally/cmd/tally.
	Service string `yaml:"service"`
	// Fields and Relations place the service in one estate's own scheme
	// (its pillars, its capabilities, its box inside them). The estate
	// defines them; left out, they are never written.
	Fields    map[string]any `yaml:"fields"`
	Relations map[string]any `yaml:"relations"`
	// Provides lists the APIs it offers others. Each is written as an api
	// keyed <service>/<id>.
	Provides []ProvidedAPI `yaml:"provides"`
	// Consumes lists the keys of the APIs it calls today.
	Consumes []string `yaml:"consumes"`
	// WillConsume lists the keys of the APIs it does not call yet but should.
	WillConsume []string `yaml:"will_consume"`
}

// ProvidedAPI is one API a service offers.
type ProvidedAPI struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Kind        string `yaml:"kind"`
	Description string `yaml:"description"`
}

var apiKinds = map[string]bool{"rest": true, "graphql": true, "grpc": true, "event": true, "web": true}

// ParseDescriptor reads an orrery.yaml and refuses one the hub would refuse.
func ParseDescriptor(data []byte) (*Descriptor, error) {
	var d Descriptor
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("orrery.yaml: %w", err)
	}
	if d.Service == "" {
		return nil, errors.New("orrery.yaml: service is required")
	}
	seen := map[string]bool{}
	for i, a := range d.Provides {
		if a.ID == "" || a.Title == "" {
			return nil, fmt.Errorf("orrery.yaml: provides[%d] needs an id and a title", i)
		}
		if !apiKinds[a.Kind] {
			return nil, fmt.Errorf("orrery.yaml: provides[%d] kind %q is not rest, graphql, grpc, event, or web", i, a.Kind)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("orrery.yaml: api %q is listed twice", a.ID)
		}
		seen[a.ID] = true
	}
	return &d, nil
}

// APIKey is the key of one API the service provides.
func (d *Descriptor) APIKey(id string) string { return d.Service + "/" + id }

// Entities are the writes that describe the app: its service, patched so the
// fields other sources write stay theirs, then one api per provided API.
// An empty list of connectors is written as an empty list, so a connector
// removed from the file is removed from the catalog.
func (d *Descriptor) Entities() []Entity {
	list := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	service := Entity{Blueprint: "sw_service", Key: d.Service, Fields: map[string]any{}, Relations: map[string]any{}}
	for k, v := range d.Fields {
		service.Fields[k] = v
	}
	for k, v := range d.Relations {
		service.Relations[k] = v
	}
	service.Fields["origin"] = "built"
	service.Relations["consumes"] = list(d.Consumes)
	service.Relations["will_consume"] = list(d.WillConsume)
	out := []Entity{service}
	for _, a := range d.Provides {
		out = append(out, Entity{
			Blueprint: "sw_api",
			Key:       d.APIKey(a.ID),
			Title:     a.Title,
			Fields:    map[string]any{"kind": a.Kind, "description": a.Description, "visibility": "internal", "external": false},
			Relations: map[string]any{"service": d.Service},
		})
	}
	return out
}

// describeBlueprints are the blueprints a description writes, named as the
// software pack of the blueprint standard names them, offered so a hub
// without them gains them. Formerly takes over a hub's older service and api.
var describeBlueprints = []Blueprint{
	{"id": "sw_service", "title": "Service", "group": "Software", "formerly": []string{"service"},
		"description": "One deployable piece of software, keyed by its repository path. Built here or adopted open source.",
		"fields": map[string]any{
			"origin": map[string]any{"type": "enum", "values": []string{"built", "adopted"}, "chip": true, "description": "Built here, or open source the estate runs."},
		}, "relations": map[string]any{
			"consumes":     map[string]any{"target": "sw_api", "many": true, "title": "Consumes", "inverseTitle": "Consumed by"},
			"will_consume": map[string]any{"target": "sw_api", "many": true, "title": "Will consume", "inverseTitle": "Wanted by"},
		}},
	{"id": "sw_api", "title": "API", "group": "Software", "formerly": []string{"api"},
		"description": "An interface a service offers others, keyed <service key>/<id>.",
		"fields": map[string]any{
			"description": map[string]any{"type": "string"},
			"kind":        map[string]any{"type": "enum", "values": []string{"rest", "graphql", "grpc", "event", "web"}},
			"visibility":  map[string]any{"type": "enum", "values": []string{"internal", "partner", "public"}},
			"external":    map[string]any{"type": "boolean"},
		}, "relations": map[string]any{
			"service": map[string]any{"target": "sw_service", "title": "Provided by", "inverseTitle": "Provides"},
		}},
}

// Describe writes the app's description once: it offers the fields the
// description needs, patches the service, and puts each provided API. It
// never opens a snapshot, so it removes nothing another source wrote.
func (c *Client) Describe(ctx context.Context, d *Descriptor) error {
	if _, err := c.OfferBlueprints(ctx, describeBlueprints); err != nil {
		return err
	}
	for _, e := range d.Entities() {
		write := c.Put
		if e.Blueprint == "sw_service" {
			write = c.Patch
		}
		if err := write(ctx, e); err != nil {
			return fmt.Errorf("describe %s: %w", e.ID(), err)
		}
	}
	return nil
}

// Describer keeps an app described for as long as it runs.
type Describer struct {
	client  *Client
	desc    *Descriptor
	log     *slog.Logger
	writes  metric.Int64Counter
	every   time.Duration
	backoff []time.Duration
}

// NewDescriber describes d through c. Its metric is <service>.orrery.describe
// with the app's own service name, counted by outcome.
func NewDescriber(c *Client, d *Descriptor, service string, log *slog.Logger) (*Describer, error) {
	writes, err := otel.Meter(service).Int64Counter(service+".orrery.describe",
		metric.WithDescription("Writes of the app's own description to Orrery, by outcome."))
	if err != nil {
		return nil, err
	}
	return &Describer{
		client:  c,
		desc:    d,
		log:     log,
		writes:  writes,
		every:   6 * time.Hour,
		backoff: []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute},
	}, nil
}

// Run describes the app now, again after each failure with a growing wait,
// and again every six hours so the catalog keeps seeing it. It returns when
// ctx ends.
func (r *Describer) Run(ctx context.Context) {
	failures := 0
	for {
		wait := r.every
		if err := r.once(ctx); err != nil {
			wait = r.backoff[min(failures, len(r.backoff)-1)]
			failures++
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (r *Describer) once(ctx context.Context) error {
	err := r.client.Describe(ctx, r.desc)
	outcome := "ok"
	if err != nil {
		outcome = "failed"
		r.log.WarnContext(ctx, "orrery describe failed", "catalog_service", r.desc.Service, "err", err)
	} else {
		r.log.InfoContext(ctx, "orrery described", "catalog_service", r.desc.Service, "apis", len(r.desc.Provides), "consumes", len(r.desc.Consumes), "will_consume", len(r.desc.WillConsume))
	}
	r.writes.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	return err
}
