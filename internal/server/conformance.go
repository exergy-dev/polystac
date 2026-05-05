package server

import "github.com/example/polystac/pkg/repository"

// Standard STAC API and OGC API conformance class URIs PolyStac may
// advertise. Conformance is computed at startup by intersecting (a) what
// the spec says exists, (b) Capabilities reported by the backend, and
// (c) optional sub-interfaces actually implemented (Aggregator, Queryables).
const (
	confCore         = "https://api.stacspec.org/v1.0.0/core"
	confCollections  = "https://api.stacspec.org/v1.0.0/collections"
	confFeatures     = "https://api.stacspec.org/v1.0.0/ogcapi-features"
	confItemSearch   = "https://api.stacspec.org/v1.0.0/item-search"
	confSort         = "https://api.stacspec.org/v1.0.0/item-search#sort"
	confQuery        = "https://api.stacspec.org/v1.0.0/item-search#query"
	confFields       = "https://api.stacspec.org/v1.0.0/item-search#fields"
	confFilterText   = "https://api.stacspec.org/v1.0.0/item-search#filter:cql2-text"
	confFilterJSON   = "https://api.stacspec.org/v1.0.0/item-search#filter:cql2-json"
	confTransaction  = "https://api.stacspec.org/v1.0.0/ogcapi-features/extensions/transaction"
	confAggregation  = "https://api.stacspec.org/v0.3.0/aggregation"
	confFreeText     = "https://api.stacspec.org/v1.0.0/item-search#free-text"

	confOGCFeatures1 = "http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/core"
	confOGCFeaturesG = "http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/geojson"
)

// Conformance assembles the conformance class set this server advertises
// based on backend capabilities and the optional sub-interfaces it
// actually implements.
func Conformance(repo repository.Repository) []string {
	caps := repo.Capabilities()
	out := []string{
		confCore,
		confCollections,
		confFeatures,
		confItemSearch,
		confOGCFeatures1,
		confOGCFeaturesG,
		confSort,
		confQuery,
		confFields,
	}
	if caps.SupportsFilterCQL2Text {
		out = append(out, confFilterText)
	}
	if caps.SupportsFilterCQL2JSON {
		out = append(out, confFilterJSON)
	}
	if caps.SupportsTransactions {
		out = append(out, confTransaction)
	}
	if caps.SupportsFreeTextSearch {
		out = append(out, confFreeText)
	}
	if _, ok := repo.(repository.Aggregator); ok {
		out = append(out, confAggregation)
	}
	return out
}
