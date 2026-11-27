package change

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"
)

// Digest is the statement's identity: sha256 over its canonical JSON. An
// approval binds to it, and the applier refuses to run when the statement
// it recomputes has another.
//
// The canonical form is RFC 8785 (JSON Canonicalization Scheme) over the
// statement with every empty list, empty object, empty string, and null
// left out, so a writer in any language gets the same digest whether it
// writes an empty list or none.
func (s *Statement) Digest() (string, error) {
	b, err := canonical(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Diff names the top-level fields of the statement that differ between
// the one judged and the one recomputed, in the contract's order. It is
// empty when the two have the same digest.
func Diff(judged, recomputed *Statement) ([]string, error) {
	parts := []struct {
		name string
		a, b any
	}{
		{"key", judged.Key, recomputed.Key},
		{"writer", judged.Writer, recomputed.Writer},
		{"actor", judged.Actor, recomputed.Actor},
		{"things", judged.Things, recomputed.Things},
		{"artifact", judged.Artifact, recomputed.Artifact},
		{"applier", judged.Applier, recomputed.Applier},
		{"effects", judged.Effects, recomputed.Effects},
		{"unknown", judged.Unknown, recomputed.Unknown},
	}
	var out []string
	for _, p := range parts {
		a, err := canonical(p.a)
		if err != nil {
			return nil, err
		}
		b, err := canonical(p.b)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(a, b) {
			out = append(out, p.name)
		}
	}
	return out, nil
}

// canonical writes v as RFC 8785 JSON with empty values left out.
func canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("change: encode: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("change: decode: %w", err)
	}
	var buf bytes.Buffer
	writeCanonical(&buf, prune(doc))
	return buf.Bytes(), nil
}

// prune drops empty lists, empty objects, empty strings, and nulls, deep.
// What is left is nil when nothing is.
func prune(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if p := prune(x); p != nil {
				out[k] = p
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if p := prune(x); p != nil {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return t
	default:
		return v
	}
}

func writeCanonical(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		// A statement holds whole numbers only, which RFC 8785 writes as
		// Go does.
		buf.WriteString(t.String())
	case string:
		writeString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, x)
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// RFC 8785 sorts by UTF-16 code units; the contract's keys are
		// ASCII, where that is byte order.
		slices.Sort(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			writeCanonical(buf, t[k])
		}
		buf.WriteByte('}')
	}
}

// writeString escapes as ECMAScript's JSON.stringify does, which RFC 8785
// requires: the quote, the backslash, and control characters only.
func writeString(buf *bytes.Buffer, s string) {
	const hexDigits = "0123456789abcdef"
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r == '\b':
			buf.WriteString(`\b`)
		case r == '\f':
			buf.WriteString(`\f`)
		case r == '\n':
			buf.WriteString(`\n`)
		case r == '\r':
			buf.WriteString(`\r`)
		case r == '\t':
			buf.WriteString(`\t`)
		case r < 0x20:
			buf.WriteString(`\u00`)
			buf.WriteByte(hexDigits[r>>4])
			buf.WriteByte(hexDigits[r&0xf])
		default:
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}
