package cql2

import (
	"errors"
	"fmt"

	upstream "github.com/exergy-dev/go-cql2"
)

// ParseError is the unified parse-time error returned by Parse,
// ParseText, ParseJSON, and Encode. It wraps the underlying upstream
// error so callers can use errors.As for the upstream-specific types
// (SyntaxError, ConformanceError, GeometryError, ...).
type ParseError struct {
	Encoding Encoding
	Msg      string
	Err      error
}

func (e *ParseError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("cql2: %s", e.Msg)
	}
	return fmt.Sprintf("cql2 (%s): %v", encodingName(e.Encoding), e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// TranslationError is returned by backend translators when an otherwise
// well-formed CQL2 expression cannot be expressed in the target backend's
// query language. Backends MUST return a TranslationError (not a parse
// error or a generic error) so the service layer can map it to a 400
// Bad Request with a precise reason.
type TranslationError struct {
	Backend string // e.g. "pgstac", "opensearch"
	Reason  string
	Op      Operator // optional — empty if not operator-specific
}

func (e *TranslationError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("cql2: %s does not support operator %q: %s", e.Backend, e.Op, e.Reason)
	}
	return fmt.Sprintf("cql2: %s: %s", e.Backend, e.Reason)
}

// IsParseError reports whether err is a *ParseError.
func IsParseError(err error) bool {
	var p *ParseError
	return errors.As(err, &p)
}

// IsTranslationError reports whether err is a *TranslationError.
func IsTranslationError(err error) bool {
	var t *TranslationError
	return errors.As(err, &t)
}

func encodingName(e Encoding) string {
	switch e {
	case EncodingText:
		return "text"
	case EncodingJSON:
		return "json"
	default:
		return "unknown"
	}
}

func wrapParseErr(err error) error {
	if err == nil {
		return nil
	}
	enc := EncodingText
	var se *upstream.SyntaxError
	if errors.As(err, &se) {
		enc = se.Encoding
	}
	return &ParseError{Encoding: enc, Msg: err.Error(), Err: err}
}
