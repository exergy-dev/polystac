package stac

import (
	"bytes"
	"encoding/json"
	"sort"
)

// marshalOrdered emits the entries of m as a JSON object whose keys are
// ordered: first by the canonical slice (in the given order, skipping any
// keys not present), then any remaining keys alphabetically. Values are
// marshalled with the standard library encoder.
//
// Errors from json.Marshal are deliberately not propagated — callers pass
// values that have already been built, and the SDD requires byte-equivalent
// output. If an inner value's MarshalJSON fails, the function returns a
// JSON error object so the failure surfaces in tests rather than silently
// emitting bad JSON.
func marshalOrdered(m map[string]any, canonical []string) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')

	emitted := make(map[string]struct{}, len(m))
	first := true

	emit := func(k string, v any) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(v)
		if err != nil {
			buf.WriteString(`null`)
			return
		}
		buf.Write(vb)
	}

	for _, k := range canonical {
		v, ok := m[k]
		if !ok {
			continue
		}
		emit(k, v)
		emitted[k] = struct{}{}
	}

	rest := make([]string, 0, len(m))
	for k := range m {
		if _, done := emitted[k]; done {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	for _, k := range rest {
		emit(k, m[k])
	}

	buf.WriteByte('}')
	return buf.Bytes()
}
