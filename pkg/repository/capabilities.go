package repository

// Capabilities is what each backend declares about itself. The service
// layer reads this at startup to:
//
//  1. Assemble the conformance class set advertised on / and /conformance.
//  2. Wire only the extension routes the backend can serve.
//  3. Document caveats via Notes (surfaced in the OpenAPI description and
//     landing page metadata).
type Capabilities struct {
	// Backend is a short identifier (e.g. "pgstac", "opensearch").
	Backend string

	SupportsTransactions     bool
	SupportsBulkTransactions bool
	SupportsFreeTextSearch   bool
	SupportsFilterCQL2Text   bool
	SupportsFilterCQL2JSON   bool

	SupportedSortFields SortFieldPolicy
	CountSemantics      CountSemantics

	// MaxItemLimit caps the per-page item count the backend will honor.
	// 0 means use the service-layer default.
	MaxItemLimit int

	// Notes carries human-readable caveats. Examples:
	//   - "numberMatched is approximate above 10000 (track_total_hits=10000)"
	//   - "sort on text fields rejected; use a keyword sub-field"
	Notes []string
}

// SortFieldPolicy describes which fields the backend accepts in SortBy.
type SortFieldPolicy int

const (
	// SortFieldsAll: any field is acceptable; the backend will best-effort
	// it (may be slow on un-indexed fields).
	SortFieldsAll SortFieldPolicy = iota
	// SortFieldsIndexedOnly: only fields with a backend-side index are
	// accepted. Others return ErrInvalidInput at request time.
	SortFieldsIndexedOnly
	// SortFieldsDatetimeOnly: only datetime/created/updated are accepted.
	SortFieldsDatetimeOnly
)

// CountSemantics describes the semantic of `numberMatched` in responses.
type CountSemantics int

const (
	// CountExact: numberMatched is the exact total.
	CountExact CountSemantics = iota
	// CountApproximate: numberMatched is approximate; an exact count would
	// be prohibitive. The service layer tags the response accordingly.
	CountApproximate
	// CountNone: backend cannot compute totals; numberMatched is omitted.
	CountNone
)
