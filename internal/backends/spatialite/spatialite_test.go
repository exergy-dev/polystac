//go:build cgo && spatialite

package spatialite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/spatialite"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// openTestRepo opens a fresh SpatiaLite-backed repository pointed at a
// temp DB. If mod_spatialite isn't available on the host, the test is
// skipped — operators install it via libsqlite3-mod-spatialite (Debian)
// or libspatialite (Alpine).
func openTestRepo(t *testing.T) repository.Repository {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Backend = "spatialite"
	cfg.BackendConfig = map[string]string{
		"database": filepath.Join(dir, "stac.db"),
	}
	repo, err := backends.Open(context.Background(), "spatialite", cfg)
	if err != nil {
		if strings.Contains(err.Error(), "mod_spatialite") {
			t.Skip("mod_spatialite not available: " + err.Error())
		}
		t.Fatalf("open spatialite: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func seedSmoke(t *testing.T, repo repository.Repository) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "x", License: "y"}); err != nil {
		t.Fatalf("upsert collection: %v", err)
	}
	for _, s := range []struct {
		ID  string
		DT  string
		Cld float64
	}{
		{"i-1", "2024-01-01T00:00:00Z", 10},
		{"i-2", "2024-02-01T00:00:00Z", 20},
		{"i-3", "2024-03-01T00:00:00Z", 30},
	} {
		it := &stac.Item{
			ID: s.ID, Collection: "c1",
			Geometry: &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{0, 0}},
			BBox:     []float64{0, 0, 0, 0},
			Properties: stac.ItemProperties{
				"datetime":       s.DT,
				"eo:cloud_cover": s.Cld,
			},
		}
		if err := repo.UpsertItem(ctx, it); err != nil {
			t.Fatalf("upsert item %s: %v", s.ID, err)
		}
	}
}

func TestOpenMissingDatabase(t *testing.T) {
	cfg := config.Defaults()
	cfg.Backend = "spatialite"
	cfg.BackendConfig = map[string]string{} // no "database"
	_, err := backends.Open(context.Background(), "spatialite", cfg)
	if err == nil {
		t.Fatal("expected error when POLYSTAC_SPATIALITE_DATABASE missing")
	}
	if !strings.Contains(err.Error(), "DATABASE") {
		t.Errorf("error should mention DATABASE: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	repo := openTestRepo(t)
	caps := repo.Capabilities()
	if caps.Backend != "spatialite" {
		t.Errorf("backend: got %q want spatialite", caps.Backend)
	}
	if !caps.SupportsFilterCQL2Text || !caps.SupportsFilterCQL2JSON {
		t.Error("CQL2 text+JSON should both be supported")
	}
	if caps.CountSemantics != repository.CountExact {
		t.Errorf("CountSemantics: got %v want CountExact", caps.CountSemantics)
	}
}

func TestCollectionRoundTrip(t *testing.T) {
	repo := openTestRepo(t)
	ctx := context.Background()
	if err := repo.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "alpha", License: "x"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetCollection(ctx, "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "c1" || got.Description != "alpha" {
		t.Errorf("decoded collection mismatch: %+v", got)
	}

	// not-found
	_, err = repo.GetCollection(ctx, "missing")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestItemRoundTripAndDelete(t *testing.T) {
	repo := openTestRepo(t)
	ctx := context.Background()
	seedSmoke(t, repo)

	got, err := repo.GetItem(ctx, "c1", "i-2")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.ID != "i-2" || got.Collection != "c1" {
		t.Errorf("decoded item mismatch: %+v", got)
	}

	if err := repo.DeleteItem(ctx, "c1", "i-2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetItem(ctx, "c1", "i-2"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSearchAndPaginate(t *testing.T) {
	repo := openTestRepo(t)
	ctx := context.Background()
	seedSmoke(t, repo)

	// All items
	page, err := repo.Search(ctx, repository.SearchRequest{Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := len(page.Items); got != 3 {
		t.Errorf("len: got %d want 3", got)
	}
	if page.Matched == nil || *page.Matched != 3 {
		t.Errorf("matched: got %v want 3", page.Matched)
	}

	// Page size 2 — expect a NextToken and the second page returns the rest.
	first, err := repo.Search(ctx, repository.SearchRequest{
		Limit: 2,
		SortBy: []repository.SortClause{
			{Field: "id", Direction: repository.SortAsc},
		},
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.NextToken == "" {
		t.Fatalf("first page: items=%d token=%q", len(first.Items), first.NextToken)
	}
	second, err := repo.Search(ctx, repository.SearchRequest{
		Limit: 2,
		SortBy: []repository.SortClause{
			{Field: "id", Direction: repository.SortAsc},
		},
		Token: first.NextToken,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := len(second.Items); got != 1 {
		t.Errorf("second page len: got %d want 1", got)
	}
	if second.NextToken != "" {
		t.Errorf("expected empty NextToken on last page, got %q", second.NextToken)
	}
}

func TestDeleteCollectionCascadesItems(t *testing.T) {
	repo := openTestRepo(t)
	ctx := context.Background()
	seedSmoke(t, repo)
	if err := repo.DeleteCollection(ctx, "c1"); err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	page, err := repo.Search(ctx, repository.SearchRequest{Limit: 100})
	if err != nil {
		t.Fatalf("search after cascade: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("expected 0 items after cascade, got %d", len(page.Items))
	}
}

func TestQueryablesIntrospection(t *testing.T) {
	repo := openTestRepo(t)
	ctx := context.Background()
	seedSmoke(t, repo)
	q, ok := repo.(repository.Queryables)
	if !ok {
		t.Fatal("backend should implement Queryables")
	}
	doc, err := q.Queryables(ctx, "c1")
	if err != nil {
		t.Fatalf("queryables: %v", err)
	}
	props, _ := doc.Schema["properties"].(map[string]any)
	if _, ok := props["eo:cloud_cover"]; !ok {
		t.Errorf("queryables should include eo:cloud_cover; got %v", props)
	}
}
