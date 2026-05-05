package parity

import (
	"context"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// Case is one corpus entry — a Search request with the expected
// outcome.
type Case struct {
	Name             string
	Request          repository.SearchRequest
	WantIDs          []string
	WantMatched      *int64
	MatchedTolerance int64
	OrderInsensitive bool
	SkipIf           func(repository.Capabilities) bool
	Reason           string
}

func ptrI64(n int64) *int64 { return &n }

// Corpus is the curated list of cases the parity matrix runs against
// every backend. The fixture set (Seed) is deliberately small so a
// failure points at the case rather than the data.
//
// Add cases here, not in backend-specific tests — the corpus is the
// contract (SDD §14.2).
var Corpus = []Case{
	{
		Name:             "all-items",
		Request:          repository.SearchRequest{Limit: 100},
		WantIDs:          []string{"a-1", "a-2", "a-3", "b-1", "b-2"},
		WantMatched:      ptrI64(5),
		OrderInsensitive: true,
	},
	{
		Name:             "single-collection",
		Request:          repository.SearchRequest{Collections: []string{"a"}, Limit: 100},
		WantIDs:          []string{"a-1", "a-2", "a-3"},
		WantMatched:      ptrI64(3),
		OrderInsensitive: true,
	},
	{
		Name:             "ids-filter",
		Request:          repository.SearchRequest{IDs: []string{"a-2", "b-1"}, Limit: 100},
		WantIDs:          []string{"a-2", "b-1"},
		WantMatched:      ptrI64(2),
		OrderInsensitive: true,
	},
	{
		Name: "cloud-cover-lt",
		Request: repository.SearchRequest{
			Filter:     mustParse(`"eo:cloud_cover" < 30`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-1", "a-2", "b-1"},
		WantMatched:      ptrI64(3),
		OrderInsensitive: true,
		SkipIf:           func(c repository.Capabilities) bool { return !c.SupportsFilterCQL2Text && !c.SupportsFilterCQL2JSON },
		Reason:           "backend lacks CQL2 filter support",
	},
	{
		Name: "platform-equals",
		Request: repository.SearchRequest{
			Filter:     mustParse(`platform = 'S2A'`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-1", "a-3", "b-1"},
		WantMatched:      ptrI64(3),
		OrderInsensitive: true,
	},
	{
		Name: "and-combination",
		Request: repository.SearchRequest{
			Filter:     mustParse(`platform = 'S2A' and "eo:cloud_cover" < 30`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-1", "b-1"},
		WantMatched:      ptrI64(2),
		OrderInsensitive: true,
	},
	{
		Name: "in-list",
		Request: repository.SearchRequest{
			Filter:     mustParse(`platform in ('S2B', 'L8')`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-2", "b-2"},
		WantMatched:      ptrI64(2),
		OrderInsensitive: true,
	},
	{
		Name: "between",
		Request: repository.SearchRequest{
			Filter:     mustParse(`"eo:cloud_cover" between 30 and 70`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-3", "b-2"},
		WantMatched:      ptrI64(2),
		OrderInsensitive: true,
	},
	{
		Name: "is-null",
		Request: repository.SearchRequest{
			Filter:     mustParse(`missing_prop is null`),
			FilterLang: repository.FilterLangText,
			Limit:      100,
		},
		WantIDs:          []string{"a-1", "a-2", "a-3", "b-1", "b-2"},
		WantMatched:      ptrI64(5),
		OrderInsensitive: true,
	},
	{
		Name:             "page-size-2",
		Request:          repository.SearchRequest{Limit: 2, SortBy: []repository.SortClause{{Field: "id", Direction: repository.SortAsc}}},
		WantIDs:          []string{"a-1", "a-2"},
		WantMatched:      ptrI64(5),
		OrderInsensitive: false,
	},
}

// Seed installs a deterministic small fixture: 2 collections (a, b),
// 5 items total with carefully-chosen properties so each corpus case
// has a non-trivial expected result.
func Seed(ctx context.Context, repo repository.Repository) error {
	type spec struct {
		Col, ID  string
		Cloud    float64
		Platform string
	}
	specs := []spec{
		{"a", "a-1", 10, "S2A"},
		{"a", "a-2", 20, "S2B"},
		{"a", "a-3", 50, "S2A"},
		{"b", "b-1", 5, "S2A"},
		{"b", "b-2", 70, "L8"},
	}
	if err := repo.UpsertCollection(ctx, &stac.Collection{ID: "a", Description: "alpha", License: "x"}); err != nil {
		return err
	}
	if err := repo.UpsertCollection(ctx, &stac.Collection{ID: "b", Description: "beta", License: "x"}); err != nil {
		return err
	}
	for _, s := range specs {
		it := &stac.Item{
			ID: s.ID, Collection: s.Col,
			Geometry: &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{0, 0}},
			BBox:     []float64{0, 0, 0, 0},
			Properties: stac.ItemProperties{
				"datetime":       "2024-01-01T00:00:00Z",
				"eo:cloud_cover": s.Cloud,
				"platform":       s.Platform,
			},
		}
		if err := repo.UpsertItem(ctx, it); err != nil {
			return err
		}
	}
	return nil
}

func mustParse(s string) cql2.Expression {
	e, err := cql2.Parse([]byte(s))
	if err != nil {
		panic(err)
	}
	return e
}
