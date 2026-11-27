package change

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Kind is one registered effect kind. The suite ships a base set
// (base-kinds.json); an estate adds its own as data, never as code.
type Kind struct {
	Name string `json:"name"`
	// Family is FamilyExposure or FamilyDisruption: how the kind is proven.
	Family string `json:"family"`
	// Means says in a few words what the kind does to a live thing.
	Means string `json:"means"`
	// Unit is what an effect's size counts, when the kind has a size.
	Unit string `json:"unit,omitempty"`
	// Watch is how long the watch reads a subject's signals after a change
	// ends. Zero means the kind is not watched.
	Watch Duration `json:"watch,omitempty"`
}

// The two families. Exposure is proven by the artifact itself, once the
// applier recomputes the statement from it. Disruption is proven only by
// watching what the change did.
const (
	FamilyExposure   = "exposure"
	FamilyDisruption = "disruption"
)

var families = []string{FamilyExposure, FamilyDisruption}

// Duration is a length of time written as Go writes one, such as "30m".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"30m\": %w", err)
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Kinds is the effect kinds an estate recognizes: the base set and its own.
type Kinds struct {
	byName map[string]Kind
	base   map[string]bool
}

//go:embed base-kinds.json
var baseKinds []byte

// KindsFile is the shape of a file of effect kinds, the suite's or an
// estate's.
type KindsFile struct {
	Kinds []Kind `json:"kinds"`
}

// Base returns a fresh registry holding the suite's base kinds alone.
func Base() *Kinds {
	var f KindsFile
	if err := json.Unmarshal(baseKinds, &f); err != nil {
		panic("change: base-kinds.json does not parse: " + err.Error())
	}
	k := &Kinds{byName: map[string]Kind{}, base: map[string]bool{}}
	for _, kind := range f.Kinds {
		if err := k.Add(kind); err != nil {
			panic("change: base-kinds.json: " + err.Error())
		}
		k.base[kind.Name] = true
	}
	return k
}

// LoadKinds returns the base kinds with an estate's file of kinds added.
func LoadKinds(r io.Reader) (*Kinds, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var f KindsFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("change: read kinds: %w", err)
	}
	k := Base()
	named := map[string]bool{}
	for _, kind := range f.Kinds {
		if named[kind.Name] {
			return nil, refuse("duplicate_kind", "the kinds file names %q twice", kind.Name)
		}
		named[kind.Name] = true
		if err := k.Add(kind); err != nil {
			return nil, err
		}
	}
	return k, nil
}

// Add registers a kind. A base kind may be given a new watch window, and
// nothing else: its family, meaning, and unit are the suite's, so every
// estate's sizes and proof mean the same thing.
func (k *Kinds) Add(kind Kind) error {
	if err := kind.validate(); err != nil {
		return err
	}
	if k.base[kind.Name] {
		was := k.byName[kind.Name]
		if was.Family != kind.Family || was.Unit != kind.Unit {
			return refuse("base_kind_changed", "%q is a base kind; an estate may change only its watch", kind.Name)
		}
		was.Watch = kind.Watch
		k.byName[kind.Name] = was
		return nil
	}
	k.byName[kind.Name] = kind
	return nil
}

// Get returns a registered kind by name.
func (k *Kinds) Get(name string) (Kind, bool) {
	kind, ok := k.byName[name]
	return kind, ok
}

// All returns every registered kind, by name.
func (k *Kinds) All() []Kind {
	out := make([]Kind, 0, len(k.byName))
	for _, kind := range k.byName {
		out = append(out, kind)
	}
	slices.SortFunc(out, func(a, b Kind) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (kind *Kind) validate() error {
	if err := slug("kind.name", kind.Name); err != nil {
		return err
	}
	if !oneOf(kind.Family, families) {
		return refuse("bad_family", "kind %q: family must be one of %s", kind.Name, strings.Join(families, ", "))
	}
	if kind.Means == "" {
		return refuse("missing_means", "kind %q says what it means", kind.Name)
	}
	if len(kind.Means) > MaxText || strings.ContainsAny(kind.Means, "\n\r\x00") {
		return refuse("bad_means", "kind %q: means is one line of at most %d characters", kind.Name, MaxText)
	}
	if kind.Unit != "" {
		if err := slug("kind.unit", kind.Unit); err != nil {
			return err
		}
	}
	if kind.Watch < 0 {
		return refuse("bad_watch", "kind %q: watch is never below zero", kind.Name)
	}
	return nil
}
