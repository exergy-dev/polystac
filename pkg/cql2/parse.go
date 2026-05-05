// Package cql2 is PolyStac's integration shim over
// github.com/exergy-dev/go-cql2 (the AST + parser).
//
// PolyStac does not ship its own CQL2 grammar. This package:
//
//   - re-exports the upstream AST node types as PolyStac aliases so
//     callers (server, backends) need only import this package;
//   - registers both the CQL2-Text and CQL2-JSON codecs once via a blank
//     import of the upstream codecs package;
//   - normalizes parse errors into PolyStac's error space (see errors.go);
//   - exposes a thin Walk helper plus the upstream Visitor interface for
//     backend translators (see visitor.go).
//
// Why a shim, not a direct dependency? Two reasons. (1) The translator
// boundary is the AST, never the grammar — keeping that boundary in this
// package limits blast radius if upstream's surface changes (SDD risk #1).
// (2) PolyStac normalizes error and parse-mode handling so backends do not
// each re-implement it.
package cql2

import (
	upstream "github.com/exergy-dev/go-cql2"
	_ "github.com/exergy-dev/go-cql2/codecs" // registers CQL2-Text and CQL2-JSON codecs
)

// Expression is the root AST node type. Callers walk it with Walk and
// type-switch on the concrete *Op / *PropertyRef / *NumLit / ... types
// re-exported below.
type Expression = upstream.Node

// Encoding identifies a CQL2 surface syntax.
type Encoding = upstream.Encoding

const (
	EncodingText = upstream.EncodingText
	EncodingJSON = upstream.EncodingJSON
)

// Parse auto-detects the encoding (text vs JSON) and returns an AST.
// Returns a wrapped *ParseError on failure.
func Parse(input []byte) (Expression, error) {
	n, err := upstream.Parse(input)
	if err != nil {
		return nil, wrapParseErr(err)
	}
	return n, nil
}

// ParseText parses a CQL2-Text expression.
func ParseText(input []byte) (Expression, error) {
	n, err := upstream.Parse(input)
	if err != nil {
		return nil, wrapParseErr(err)
	}
	return n, nil
}

// ParseJSON parses a CQL2-JSON expression.
func ParseJSON(input []byte) (Expression, error) {
	n, err := upstream.Parse(input)
	if err != nil {
		return nil, wrapParseErr(err)
	}
	return n, nil
}

// Encode serializes an Expression in the requested encoding.
func Encode(e Expression, enc Encoding) ([]byte, error) {
	b, err := upstream.Encode(e, enc)
	if err != nil {
		return nil, wrapParseErr(err)
	}
	return b, nil
}
