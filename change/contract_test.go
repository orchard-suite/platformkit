package change

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The schema and the Go validator are one contract in two forms. Every
// worked example in schema/examples passes both; every one in
// schema/examples/bad fails both.

func compile(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.NewCompiler().Compile(filepath.Join("schema", name))
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return s
}

func readExamples(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples in %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = b
	}
	return out
}

func estateKinds(t *testing.T) *Kinds {
	t.Helper()
	f, err := os.Open("schema/examples/kinds-estate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	k, err := LoadKinds(f)
	if err != nil {
		t.Fatalf("estate kinds: %v", err)
	}
	return k
}

type checker struct {
	schemas map[string]*jsonschema.Schema
	kinds   *Kinds
}

func newChecker(t *testing.T) checker {
	return checker{
		schemas: map[string]*jsonschema.Schema{
			"statement": compile(t, "statement.json"),
			"event":     compile(t, "event.json"),
			"kinds":     compile(t, "kinds.json"),
		},
		kinds: estateKinds(t),
	}
}

// check runs an example through the schema and the Go validator, and says
// what each made of it.
func (c checker) check(t *testing.T, name string, b []byte) (schemaErr, goErr error) {
	t.Helper()
	form := strings.SplitN(name, "-", 2)[0]
	schema, ok := c.schemas[form]
	if !ok {
		t.Fatalf("%s: an example's name starts with statement-, event-, or kinds-", name)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	schemaErr = schema.Validate(doc)
	switch form {
	case "statement":
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		var s Statement
		if goErr = dec.Decode(&s); goErr == nil {
			goErr = s.Validate(c.kinds)
		}
	case "event":
		var e Event
		if e, goErr = Decode(b); goErr == nil {
			goErr = e.Validate(c.kinds)
		}
	case "kinds":
		_, goErr = LoadKinds(bytes.NewReader(b))
	}
	return schemaErr, goErr
}

func TestExamplesPassSchemaAndValidator(t *testing.T) {
	c := newChecker(t)
	for name, b := range readExamples(t, "schema/examples") {
		schemaErr, goErr := c.check(t, name, b)
		if schemaErr != nil {
			t.Errorf("%s: schema: %v", name, schemaErr)
		}
		if goErr != nil {
			t.Errorf("%s: validator: %v", name, goErr)
		}
	}
}

func TestBadExamplesFailSchemaAndValidator(t *testing.T) {
	c := newChecker(t)
	for name, b := range readExamples(t, "schema/examples/bad") {
		schemaErr, goErr := c.check(t, name, b)
		if schemaErr == nil {
			t.Errorf("%s: the schema took it", name)
		}
		if goErr == nil {
			t.Errorf("%s: the validator took it", name)
		}
	}
}

// The base kinds the package embeds are a valid kinds file.
func TestBaseKindsMatchSchema(t *testing.T) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(baseKinds))
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, "kinds.json").Validate(doc); err != nil {
		t.Errorf("base-kinds.json: %v", err)
	}
}

// A statement Go writes passes the schema, whatever it leaves empty.
func TestGoOutputMatchesSchema(t *testing.T) {
	s := Statement{
		Key:      "k",
		Writer:   Writer{Name: "infra", Version: "1"},
		Actor:    Actor{Kind: ActorSystem, ID: "ci"},
		Things:   []Ref{{Blueprint: "workload", Key: "a"}},
		Artifact: Artifact{Kind: "plan", Digest: "sha256:" + strings.Repeat("0", 64)},
		Applier:  ApplierHub,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := compile(t, "statement.json").Validate(doc); err != nil {
		t.Errorf("Go's statement fails the schema: %v\n%s", err, b)
	}
}
