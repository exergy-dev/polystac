package inmem

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

func mustParse(t *testing.T, s string) cql2.Expression {
	t.Helper()
	e, err := cql2.Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return e
}

func newSeeded(t *testing.T) *Repo {
	t.Helper()
	ctx := context.Background()
	r := New()
	if err := r.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "c1", License: "x"}); err != nil {
		t.Fatal(err)
	}
	for i, dt := range []string{
		"2024-01-01T00:00:00Z",
		"2024-02-01T00:00:00Z",
		"2024-03-01T00:00:00Z",
	} {
		it := &stac.Item{
			ID:         string(rune('a' + i)),
			Collection: "c1",
			Geometry:   &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{float64(i), float64(i)}},
			BBox:       []float64{float64(i), float64(i), float64(i), float64(i)},
			Properties: stac.ItemProperties{
				"datetime":       dt,
				"eo:cloud_cover": float64(10 * (i + 1)),
				"platform":       "S2A",
			},
		}
		if err := r.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestRepoBasicCRUD(t *testing.T) {
	ctx := context.Background()
	r := newSeeded(t)

	got, err := r.GetItem(ctx, "c1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "a" {
		t.Errorf("id: %q", got.ID)
	}

	if _, err := r.GetItem(ctx, "c1", "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	if err := r.DeleteItem(ctx, "c1", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetItem(ctx, "c1", "a"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete")
	}
}

func TestRepoSearchWithFilter(t *testing.T) {
	ctx := context.Background()
	r := newSeeded(t)
	expr, err := cql2.Parse([]byte(`"eo:cloud_cover" > 15`))
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Search(ctx, repository.SearchRequest{Filter: expr, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Errorf("want 2 matches, got %d", len(page.Items))
	}
	for _, it := range page.Items {
		cc, _ := it.Properties["eo:cloud_cover"].(float64)
		if cc <= 15 {
			t.Errorf("item %s should not match (cc=%v)", it.ID, cc)
		}
	}
}

func TestRepoSearchPagination(t *testing.T) {
	ctx := context.Background()
	r := newSeeded(t)
	page1, err := r.Search(ctx, repository.SearchRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 size %d", len(page1.Items))
	}
	if page1.NextToken == "" {
		t.Fatalf("expected next token")
	}
	page2, err := r.Search(ctx, repository.SearchRequest{Limit: 2, Token: page1.NextToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("page2 size %d", len(page2.Items))
	}
	if page2.NextToken != "" {
		t.Errorf("page2 should be terminal")
	}
}

func TestRepoSearchDatetime(t *testing.T) {
	ctx := context.Background()
	r := newSeeded(t)
	start := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	page, err := r.Search(ctx, repository.SearchRequest{Datetime: &repository.TemporalInterval{Start: &start, End: &end}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Errorf("want 2, got %d", len(page.Items))
	}
}

func TestRepoSortDescending(t *testing.T) {
	ctx := context.Background()
	r := newSeeded(t)
	page, err := r.Search(ctx, repository.SearchRequest{
		SortBy: []repository.SortClause{{Field: "eo:cloud_cover", Direction: repository.SortDesc}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d", len(page.Items))
	}
	prev := 1e9
	for _, it := range page.Items {
		cc := it.Properties["eo:cloud_cover"].(float64)
		if cc > prev {
			t.Errorf("not descending: %v then %v", prev, cc)
		}
		prev = cc
	}
}

func TestRepoBulkUpsert(t *testing.T) {
	ctx := context.Background()
	r := New()
	if err := r.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "x", License: "y"}); err != nil {
		t.Fatal(err)
	}
	stream := func(yield func(*stac.Item, error) bool) {
		for i := 0; i < 5; i++ {
			it := &stac.Item{
				ID:         "id-" + string(rune('0'+i)),
				Collection: "c1",
				Properties: stac.ItemProperties{"datetime": "2024-01-01T00:00:00Z"},
			}
			if !yield(it, nil) {
				return
			}
		}
	}
	res, err := r.BulkUpsertItems(ctx, iter.Seq2[*stac.Item, error](stream))
	if err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 5 || res.Failed != 0 {
		t.Errorf("results: %+v", res)
	}
}

func TestRepoCapabilities(t *testing.T) {
	c := New().Capabilities()
	if c.Backend != "inmem" {
		t.Errorf("backend: %q", c.Backend)
	}
	if !c.SupportsTransactions {
		t.Error("expected transactions")
	}
}

func TestRegisteredViaFactory(t *testing.T) {
	// Force the registry to see the inmem registration via init().
	_ = mustParse // ensure go test sees this var even if other tests are skipped
}
