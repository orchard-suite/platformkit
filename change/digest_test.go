package change

import (
	"slices"
	"strings"
	"testing"
)

// An empty list and no list are the same statement, so a writer in any
// language gets one digest for it.
func TestDigestIgnoresEmptyLists(t *testing.T) {
	a := good()
	b := good()
	a.Unknown = nil
	b.Unknown = []Unknown{}
	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Errorf("empty and absent lists differ: %s %s", da, db)
	}
	if !strings.HasPrefix(da, "sha256:") || len(da) != len("sha256:")+64 {
		t.Errorf("digest form: %s", da)
	}
}

// Any change to what the statement says moves its digest, and a size of
// zero is said, not left out.
func TestDigestMoves(t *testing.T) {
	base, _ := ptr(good()).Digest()
	zero := int64(0)
	for name, f := range map[string]func(*Statement){
		"applier":  func(s *Statement) { s.Applier = ApplierAsker },
		"effect":   func(s *Statement) { s.Effects[0].Undo = UndoNo },
		"size":     func(s *Statement) { s.Effects[0].Size = &zero },
		"no size":  func(s *Statement) { s.Effects[0].Size = nil },
		"artifact": func(s *Statement) { s.Artifact.Digest = "sha256:" + strings.Repeat("b", 64) },
	} {
		s := good()
		f(&s)
		if d, _ := s.Digest(); d == base {
			t.Errorf("%s: the digest did not move", name)
		}
	}
}

// The digest is RFC 8785 over the statement: sorted keys, no spaces, and
// only the quote, the backslash, and control characters escaped.
func TestCanonicalForm(t *testing.T) {
	b, err := canonical(map[string]any{"b": "<&> ", "a": []any{}, "c": "x\"\\\n", "d": ""})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"b\":\"<&> \",\"c\":\"x\\\"\\\\\\n\"}"
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestDiffNamesWhatChanged(t *testing.T) {
	a := good()
	b := good()
	b.Artifact.Digest = "sha256:" + strings.Repeat("c", 64)
	b.Effects = nil
	d, err := Diff(&a, &b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d, []string{"artifact", "effects"}) {
		t.Errorf("diff: %v", d)
	}
	same := good()
	if d, _ := Diff(&a, &same); len(d) != 0 {
		t.Errorf("the same statement differs: %v", d)
	}
}

func ptr[T any](v T) *T { return &v }
