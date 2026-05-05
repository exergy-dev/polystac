// Package parity is the cross-backend parity-matrix harness PolyStac
// uses to assert that semantically equivalent requests produce
// equivalent responses across every backend (SDD §14.2).
//
// The harness is driven by a corpus (see corpus.go) and a small
// equivalence relation:
//
//   - Item IDs match exactly, in the order returned (after applying any
//     deterministic-order rule for backends with sort-tiebreak quirks).
//   - numberMatched matches exactly when both sides report
//     CountSemantics == CountExact; otherwise we only assert it is
//     within an absolute tolerance.
//   - Properties of returned items match exactly for every key explicitly
//     listed in the corpus assertion.
//
// Adding a new backend means making the corpus pass — no new harness
// code required (SDD §14.2: "the corpus is the contract").
package parity

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// Suite is the parity contract every backend implementation must satisfy.
// Pass a Seeder that populates the (already-empty) Repository with the
// shared fixture set — typically by calling Seed in this package.
type Suite struct {
	Name   string
	Open   func(t *testing.T) repository.Repository
	Seeder func(ctx context.Context, repo repository.Repository) error
}

// Run executes the corpus against the supplied backend.
func (s Suite) Run(t *testing.T) {
	t.Helper()
	t.Run(s.Name, func(t *testing.T) {
		repo := s.Open(t)
		t.Cleanup(func() { _ = repo.Close() })
		ctx := context.Background()
		seed := s.Seeder
		if seed == nil {
			seed = Seed
		}
		if err := seed(ctx, repo); err != nil {
			t.Fatalf("seed: %v", err)
		}
		caps := repo.Capabilities()
		for _, c := range Corpus {
			c := c
			t.Run(c.Name, func(t *testing.T) {
				if c.SkipIf != nil && c.SkipIf(caps) {
					t.Skipf("skipping under capabilities: %s", c.Reason)
				}
				page, err := repo.Search(ctx, c.Request)
				if err != nil {
					t.Fatalf("search: %v", err)
				}
				assertExpectedItems(t, page, c, caps)
			})
		}
	})
}

func assertExpectedItems(t *testing.T, page *repository.Page[*stac.Item], c Case, caps repository.Capabilities) {
	t.Helper()
	gotIDs := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		gotIDs = append(gotIDs, it.ID)
	}
	if c.OrderInsensitive {
		sort.Strings(gotIDs)
		want := append([]string(nil), c.WantIDs...)
		sort.Strings(want)
		if !reflect.DeepEqual(gotIDs, want) {
			t.Errorf("ids (sorted):\n got:  %v\n want: %v", gotIDs, want)
		}
	} else {
		if !reflect.DeepEqual(gotIDs, c.WantIDs) {
			t.Errorf("ids (ordered):\n got:  %v\n want: %v", gotIDs, c.WantIDs)
		}
	}
	if c.WantMatched != nil && page.Matched != nil {
		switch caps.CountSemantics {
		case repository.CountExact:
			if *page.Matched != *c.WantMatched {
				t.Errorf("numberMatched: got %d want %d", *page.Matched, *c.WantMatched)
			}
		case repository.CountApproximate:
			delta := *page.Matched - *c.WantMatched
			if delta < 0 {
				delta = -delta
			}
			if delta > c.MatchedTolerance {
				t.Errorf("numberMatched out of tolerance: got %d want %d ±%d", *page.Matched, *c.WantMatched, c.MatchedTolerance)
			}
		}
	}
}
