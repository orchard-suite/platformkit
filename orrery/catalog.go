package orrery

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

// Blueprint is a blueprint definition as a catalog YAML file writes it.
type Blueprint map[string]any

// CatalogFile is the blueprints and entities sections of a catalog YAML
// file, the same shape a git source reads.
type CatalogFile struct {
	Blueprints []Blueprint `yaml:"blueprints"`
	Entities   []Entity    `yaml:"entities"`
}

// ParseCatalog parses catalog YAML.
func ParseCatalog(data []byte) (*CatalogFile, error) {
	var file CatalogFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for i, b := range file.Blueprints {
		if id, _ := b["id"].(string); id == "" {
			return nil, fmt.Errorf("blueprint %d needs an id", i)
		}
	}
	for i, e := range file.Entities {
		if e.Blueprint == "" || e.Key == "" {
			return nil, fmt.Errorf("entity %d needs blueprint and key", i)
		}
	}
	return &file, nil
}

// ReadCatalog parses a catalog YAML file.
func ReadCatalog(path string) (*CatalogFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := ParseCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// OfferResult says what the hub did with offered blueprints.
type OfferResult struct {
	Created []string `json:"created"`
	// Extended lists blueprints that existed and gained fields or relations they lacked.
	Extended []string `json:"extended"`
	Kept     []string `json:"kept"`
	Rejected []struct {
		Blueprint string `json:"blueprint"`
		Reason    string `json:"reason"`
	} `json:"rejected"`
}

// OfferBlueprints brings the blueprints a pusher writes. The hub creates the
// ones that do not exist. One that exists gains the fields and relations it
// lacks and is otherwise left as it is, so an edit made on the hub is never
// overwritten and several pushers can share a blueprint. The source must be registered for every
// blueprint it offers; a refused one is an error.
func (c *Client) OfferBlueprints(ctx context.Context, blueprints []Blueprint) (*OfferResult, error) {
	var res OfferResult
	if err := c.do(ctx, http.MethodPut, "/ingest/blueprints", map[string]any{"blueprints": blueprints}, &res); err != nil {
		return nil, err
	}
	c.log.Info("orrery blueprints offered", "created", res.Created, "extended", res.Extended, "kept", res.Kept, "rejected", len(res.Rejected))
	if len(res.Rejected) > 0 {
		return &res, fmt.Errorf("orrery: %d blueprint(s) refused, first: %s: %s", len(res.Rejected), res.Rejected[0].Blueprint, res.Rejected[0].Reason)
	}
	return &res, nil
}

// Action is a self-service action definition as the hub's actions API takes
// it: title, description, group, inputs, backend, and the rest. Leave out
// `access`: who may run an action is a hub admin's to set.
type Action map[string]any

// PutAction defines or changes one action. A product declares its own
// actions this way when it starts, so a button in the hub is a declaration
// the product owns. The token's role must hold actions.manage.
func (c *Client) PutAction(ctx context.Context, id string, action Action) error {
	return c.do(ctx, http.MethodPut, "/api/actions/"+url.PathEscape(id), action, nil)
}
