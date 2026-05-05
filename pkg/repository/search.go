package repository

import (
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/stac"
)

// SearchRequest is the canonical, backend-agnostic representation of a
// STAC item search. The service layer parses both the GET form (query
// string) and the POST form (JSON body) into this same shape.
//
// Optional fields are zero-valued when absent. Backends MUST treat the
// zero value as "filter not applied" and MUST NOT mutate the request.
type SearchRequest struct {
	// Collections, if non-empty, restricts the search to these collection
	// IDs. Empty means search across all collections.
	Collections []string

	// IDs, if non-empty, restricts the search to these item IDs.
	IDs []string

	// BBox, if non-empty, holds either 4 (2D) or 6 (3D) coordinates as
	// [west, south, east, north] or [west, south, min_z, east, north,
	// max_z]. Mutually exclusive with Intersects per the spec.
	BBox []float64

	// Intersects, if non-nil, is the GeoJSON geometry the search must
	// intersect. Mutually exclusive with BBox.
	Intersects *stac.Geometry

	// Datetime, if non-nil, is the temporal interval. Either endpoint may
	// be nil to express an open interval.
	Datetime *TemporalInterval

	// Query carries the Query extension's predicate map.
	Query map[string]Predicate

	// Filter carries the parsed CQL2 expression (Filter extension).
	Filter cql2.Expression

	// FilterLang records which surface syntax produced Filter. Backends
	// usually do not care, but the service layer surfaces it in
	// conformance.
	FilterLang FilterLanguage

	// SortBy carries the Sort extension ordering.
	SortBy []SortClause

	// Fields carries the Fields extension include/exclude spec.
	Fields *FieldsSpec

	// Limit is the requested page size. Service layer validates against
	// Capabilities.MaxItemLimit.
	Limit int

	// Token is the opaque pagination token from a prior Page.
	Token string
}

// FilterLanguage identifies the surface syntax used to express Filter.
type FilterLanguage string

const (
	FilterLangNone FilterLanguage = ""
	FilterLangText FilterLanguage = "cql2-text"
	FilterLangJSON FilterLanguage = "cql2-json"
)

// TemporalInterval is a STAC datetime parameter. Either endpoint may be
// nil to express an open (unbounded) interval. A point-in-time is
// expressed as Start == End (both non-nil and equal).
type TemporalInterval struct {
	Start *time.Time
	End   *time.Time
}

// IsPoint reports whether the interval is a single instant.
func (t TemporalInterval) IsPoint() bool {
	return t.Start != nil && t.End != nil && t.Start.Equal(*t.End)
}

// SortClause is one element of a Sort extension sortby array.
type SortClause struct {
	Field     string
	Direction SortDirection
}

// SortDirection is asc or desc.
type SortDirection string

const (
	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

// FieldsSpec is the Fields extension include/exclude pair. Either may be
// empty. When both are empty, all fields are returned.
type FieldsSpec struct {
	Include []string
	Exclude []string
}

// Predicate is one entry in the Query extension's predicate map. A
// predicate carries one or more comparison operators against a property
// (e.g. {"eo:cloud_cover": {"lt": 10, "gt": 0}}).
type Predicate struct {
	Eq        any
	Neq       any
	Lt        any
	Lte       any
	Gt        any
	Gte       any
	StartsWith string
	EndsWith   string
	Contains   string
	In         []any
}

// ListCollectionsOptions controls ListCollections.
type ListCollectionsOptions struct {
	Limit int
	Token string
}

// AggregationRequest is the input to the Aggregation extension.
//
// Different backends expose different aggregation primitives; backends
// validate Aggs against their supported set and return ErrInvalidInput
// (mapped to 400) for unsupported aggs — never a 500. See SDD risk #5.
type AggregationRequest struct {
	Search SearchRequest
	Aggs   []Aggregation
}

// Aggregation is one requested aggregation.
type Aggregation struct {
	Name      string         // user-chosen name for the result key
	AggType   string         // e.g. "datetime_frequency", "frequency", "stats"
	Field     string         // property the aggregation runs on
	Params    map[string]any // backend-specific knobs (e.g. interval=month)
}

// AggregationResponse is the result.
type AggregationResponse struct {
	Aggregations map[string]any
}

// QueryablesDocument is the JSON Schema document returned by /queryables.
// It is opaque at this layer — backends produce backend-flavored schemas
// and the service layer serializes them as JSON. Modeled as map[string]any
// to avoid coupling to a specific schema library.
type QueryablesDocument struct {
	Schema map[string]any
}

// BulkResult is the per-item outcome of BulkUpsertItems.
type BulkResult struct {
	Succeeded int
	Failed    int
	// Errors holds per-item failures keyed by item ID. Implementations
	// MAY truncate the map for very large bulk operations and surface a
	// summary in TruncatedErrors.
	Errors           map[string]error
	TruncatedErrors  bool
}
