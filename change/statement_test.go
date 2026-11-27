package change

import (
	"strings"
	"testing"
)

func good() Statement {
	size := int64(2)
	return Statement{
		Key:      "plan-1",
		Writer:   Writer{Name: "infra", Version: "3.1.0"},
		Actor:    Actor{Kind: ActorAgent, ID: "agent-1", OnBehalfOf: "user@example.com"},
		Things:   []Ref{{Blueprint: "workload", Key: "acme-api"}},
		Artifact: Artifact{Kind: "plan", Digest: "sha256:" + strings.Repeat("a", 64)},
		Applier:  ApplierHub,
		Effects: []Effect{{
			Kind: "stops", Subject: Ref{Blueprint: "workload", Key: "acme-api"},
			Path: PathInPlace, Size: &size, Undo: UndoYes, Claim: Claim{From: ClaimWriter},
		}},
	}
}

// Rules the schema cannot state are the validator's alone.
func TestValidatorOnlyRules(t *testing.T) {
	cases := []struct {
		reason string
		mutate func(*Statement)
	}{
		{"unknown_kind", func(s *Statement) { s.Effects[0].Kind = "explodes" }},
		{"size_without_unit", func(s *Statement) { s.Effects[0].Kind = "opens" }},
		{"duplicate_thing", func(s *Statement) { s.Things = append(s.Things, s.Things[0]) }},
		{"email_in_name", func(s *Statement) { s.Things[0].Key = "owner-user@example.com" }},
		{"email_in_name", func(s *Statement) { s.Effects[0].Claim.Rule = "ask user@example.com" }},
		{"secret_in_name", func(s *Statement) { s.Unknown = []Unknown{{Part: "xK9mQ2pL7vR4tY8wZ3nB6cF1hJ5dG0sA", Why: WhyUnnamed}} }},
		{"secret_in_name", func(s *Statement) { s.Key = "-----BEGIN PRIVATE KEY" }},
		{"secret_in_name", func(s *Statement) { s.Key = "eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0" }},
		{"value_in_name", func(s *Statement) { s.Unknown = []Unknown{{Part: "app_settings.MODE=fast", Why: WhyUnnamed}} }},
		{"bad_on_behalf_of", func(s *Statement) { s.Actor = Actor{Kind: ActorPerson, ID: "p", OnBehalfOf: "q"} }},
	}
	for _, c := range cases {
		s := good()
		c.mutate(&s)
		if got := Reason(s.Validate(nil)); got != c.reason {
			t.Errorf("want %s, got %q", c.reason, got)
		}
	}
	s := good()
	if err := s.Validate(nil); err != nil {
		t.Fatalf("the good statement is refused: %v", err)
	}
}

// A person may be named as the actor, and nowhere else. Names that are
// long but plain, as resource names are, pass.
func TestPlainNamesPass(t *testing.T) {
	s := good()
	s.Actor = Actor{Kind: ActorPerson, ID: "user@example.com"}
	s.Things[0].Key = "/subscriptions/00000000-0000-4000-8000-000000000001/resourceGroups/rg-acme-prod/providers/Microsoft.Web/sites/acme-api"
	s.Unknown = []Unknown{{Part: `azurerm_linux_web_app.api["primary"].site_config.application_stack`, Why: WhyUnnamed}}
	if err := s.Validate(nil); err != nil {
		t.Errorf("plain names refused: %v", err)
	}
}

// An estate's own kind is known once it is loaded, and a size counts in
// its unit.
func TestEstateKindValidates(t *testing.T) {
	k := Base()
	if err := k.Add(Kind{Name: "sends", Family: FamilyExposure, Means: "People outside receive it.", Unit: "recipients"}); err != nil {
		t.Fatal(err)
	}
	s := good()
	s.Effects[0].Kind = "sends"
	if err := s.Validate(k); err != nil {
		t.Errorf("an estate kind is refused: %v", err)
	}
	if Reason(s.Validate(nil)) != "unknown_kind" {
		t.Error("an estate kind is unknown to the base set")
	}
}

func TestRefsValidateAlone(t *testing.T) {
	if err := (Ref{Blueprint: "sw_service", Key: "shop"}).Validate("things[0]"); err != nil {
		t.Errorf("a good ref: %v", err)
	}
	if r := Reason((Ref{Blueprint: "SW Service", Key: "shop"}).Validate("things[0]")); r != "bad_slug" {
		t.Errorf("a bad blueprint: %q", r)
	}
	if r := Reason((Ref{Blueprint: "sw_service", Key: "user@example.com"}).Validate("things[0]")); r != "email_in_name" {
		t.Errorf("an address in a key: %q", r)
	}
	if err := (EventRef{Sender: "orrery", Key: "run-1.stated"}).Validate("record"); err != nil {
		t.Errorf("a good event ref: %v", err)
	}
	if r := Reason((EventRef{Sender: "orrery"}).Validate("record")); r != "missing_record_key" {
		t.Errorf("an event ref with no key: %q", r)
	}
}
