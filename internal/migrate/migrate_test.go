package migrate_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/example/polystac/internal/backends/inmem"
	"github.com/example/polystac/internal/migrate"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

func seedSource(t *testing.T) *inmem.Repo {
	t.Helper()
	src := inmem.New()
	ctx := context.Background()
	for _, colID := range []string{"c1", "c2"} {
		if err := src.UpsertCollection(ctx, &stac.Collection{ID: colID, Description: colID, License: "x"}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 25; i++ {
			it := &stac.Item{
				ID: colID + "-" + strconv.Itoa(i), Collection: colID,
				Properties: stac.ItemProperties{"datetime": "2024-01-01T00:00:00Z"},
			}
			if err := src.UpsertItem(ctx, it); err != nil {
				t.Fatal(err)
			}
		}
	}
	return src
}

func TestMigrateAllCollections(t *testing.T) {
	ctx := context.Background()
	src := seedSource(t)
	dst := inmem.New()

	res, err := migrate.Run(ctx, migrate.Options{
		Source:       src,
		Destination:  dst,
		BatchSize:    10,
		SampleVerify: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, colID := range []string{"c1", "c2"} {
		cr := res.Collections[colID]
		if cr.Read != 25 || cr.Written != 25 || cr.Failed != 0 {
			t.Errorf("collection %s: read=%d written=%d failed=%d", colID, cr.Read, cr.Written, cr.Failed)
		}
	}
	if len(res.VerifyMismatches) != 0 {
		t.Errorf("verify mismatches: %+v", res.VerifyMismatches)
	}
	for _, colID := range []string{"c1", "c2"} {
		dpage, err := dst.Search(ctx, repository.SearchRequest{Collections: []string{colID}, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if int(*dpage.Matched) != 25 {
			t.Errorf("dest %s has %d items, want 25", colID, *dpage.Matched)
		}
	}
}

func TestMigrateResumeFromCursor(t *testing.T) {
	ctx := context.Background()
	src := seedSource(t)
	dst := inmem.New()
	resumePath := filepath.Join(t.TempDir(), "cursors.json")

	// Pre-populate dest collection so the second call's collection-
	// metadata copy is a no-op.
	for _, colID := range []string{"c1", "c2"} {
		if err := dst.UpsertCollection(ctx, &stac.Collection{ID: colID, Description: colID, License: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	// First run: limited collections to one to verify partial state.
	if _, err := migrate.Run(ctx, migrate.Options{
		Source: src, Destination: dst,
		Collections: []string{"c1"},
		BatchSize:   10, ResumePath: resumePath,
	}); err != nil {
		t.Fatal(err)
	}

	// Second run: rest. Resume file should not block forward progress
	// since c1 is already done (cursor was deleted on completion).
	res, err := migrate.Run(ctx, migrate.Options{
		Source: src, Destination: dst,
		BatchSize: 10, ResumePath: resumePath,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Collections["c2"].Written != 25 {
		t.Errorf("c2 written: %d", res.Collections["c2"].Written)
	}
}

