package repository

// Page is the result envelope returned by paginated reads. It is generic
// over the element type (typically *stac.Item or *stac.Collection).
//
// Tokens are opaque to callers above the Repository. Implementations are
// free to choose their own token format (pgstac's native token, an
// HMAC-signed search_after for ES/OS, etc.) but MUST round-trip — calling
// the same paginated method again with NextToken MUST return the next
// page deterministically.
type Page[T any] struct {
	// Items is the slice of results on this page.
	Items []T

	// NextToken is the token to pass to retrieve the next page.
	// Empty if this is the last page.
	NextToken string

	// PrevToken, if non-empty, is the token to retrieve the previous
	// page. Backends that cannot synthesize a prev token leave this empty
	// and the service layer suppresses the prev link.
	PrevToken string

	// Matched is the total count when the backend can compute one. The
	// pointer distinguishes "0 matches" (Matched != nil, *Matched == 0)
	// from "count not available" (Matched == nil — see Capabilities.
	// CountSemantics == CountNone).
	Matched *int64

	// Approximate is true when Matched is an approximation (e.g.,
	// OpenSearch with track_total_hits capped). Read alongside
	// Capabilities.CountSemantics; the per-Page flag lets the service
	// layer flip a request from approximate to exact when the user opts
	// in.
	Approximate bool
}
