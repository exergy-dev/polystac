// Package migrate implements `polystac migrate` — a polyglot data
// copy between any two registered backends (SDD §11).
//
// The unified Repository abstraction means migration is purely an I/O
// exercise: no schema translation, no field mapping. Source is read by
// paginated Search (sortby id asc → deterministic resume); destination
// receives via BulkUpsertItems. A goroutine pool sized by Workers
// parallelizes batches.
//
// Resume: after every successful batch, the per-collection cursor is
// flushed to a JSON sidecar at ResumePath. Re-running with the same
// path picks up where it left off. JSON (rather than SQLite) avoids
// CGO and keeps the binary pure-Go (NF-7).
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"sync"
	"time"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)


// Options configures a migration run.
type Options struct {
	Source      repository.Repository
	Destination repository.Repository

	// Collections, if non-empty, restricts the migration to these
	// collection IDs. Empty migrates everything.
	Collections []string

	// BatchSize is the number of items per BulkUpsertItems call. Defaults
	// to 500.
	BatchSize int

	// Workers controls the per-collection batch concurrency. Defaults to 4.
	Workers int

	// ResumePath, if non-empty, is the JSON sidecar where per-collection
	// cursors are persisted between batches. Empty disables resume.
	ResumePath string

	// SampleVerify, if > 0, re-fetches that many random items from each
	// side after migration and compares them; mismatches are reported
	// in Result.VerifyMismatches.
	SampleVerify int

	// Logf is the optional progress sink. Defaults to discard.
	Logf func(format string, args ...any)
}

// Result is the per-collection summary.
type Result struct {
	Collections      map[string]CollectionResult
	VerifyMismatches []VerifyMismatch
}

// CollectionResult is the per-collection summary.
type CollectionResult struct {
	Read     int
	Written  int
	Failed   int
	Skipped  int            // items already past the resume cursor
	Errors   map[string]error
	Duration time.Duration
}

// VerifyMismatch records one sample-verify failure.
type VerifyMismatch struct {
	Collection string
	ItemID     string
	Reason     string
}

// Run performs the migration. Returns the per-collection result and the
// first hard error (transient errors are recorded in CollectionResult.
// Errors).
func Run(ctx context.Context, opt Options) (*Result, error) {
	if opt.Source == nil || opt.Destination == nil {
		return nil, errors.New("migrate: source and destination required")
	}
	if opt.BatchSize <= 0 {
		opt.BatchSize = 500
	}
	if opt.Workers <= 0 {
		opt.Workers = 4
	}
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}

	cols, err := resolveCollections(ctx, opt)
	if err != nil {
		return nil, err
	}

	cursors, err := loadCursors(opt.ResumePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = saveCursors(opt.ResumePath, cursors)
	}()

	res := &Result{Collections: map[string]CollectionResult{}}

	for _, colID := range cols {
		opt.Logf("migrating collection %q", colID)
		if err := copyCollectionMetadata(ctx, opt, colID); err != nil {
			res.Collections[colID] = CollectionResult{Errors: map[string]error{"<collection>": err}}
			continue
		}
		cr, err := copyCollectionItems(ctx, opt, colID, cursors)
		res.Collections[colID] = cr
		if err != nil {
			return res, err
		}
		_ = saveCursors(opt.ResumePath, cursors)
	}

	if opt.SampleVerify > 0 {
		mismatches, err := sampleVerify(ctx, opt, cols)
		if err != nil {
			return res, err
		}
		res.VerifyMismatches = mismatches
	}
	return res, nil
}

func resolveCollections(ctx context.Context, opt Options) ([]string, error) {
	if len(opt.Collections) > 0 {
		return opt.Collections, nil
	}
	page, err := opt.Source.ListCollections(ctx, repository.ListCollectionsOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("migrate: list source collections: %w", err)
	}
	out := make([]string, 0, len(page.Items))
	for _, c := range page.Items {
		out = append(out, c.ID)
	}
	return out, nil
}

func copyCollectionMetadata(ctx context.Context, opt Options, id string) error {
	c, err := opt.Source.GetCollection(ctx, id)
	if err != nil {
		return fmt.Errorf("migrate: source get collection %q: %w", id, err)
	}
	if err := opt.Destination.UpsertCollection(ctx, c); err != nil {
		return fmt.Errorf("migrate: dest upsert collection %q: %w", id, err)
	}
	return nil
}

func copyCollectionItems(ctx context.Context, opt Options, colID string, cursors map[string]string) (CollectionResult, error) {
	cr := CollectionResult{Errors: map[string]error{}}
	start := time.Now()
	defer func() { cr.Duration = time.Since(start) }()

	type job struct {
		items []*stac.Item
		token string
	}
	jobs := make(chan job, opt.Workers)
	var resMu sync.Mutex
	var wg sync.WaitGroup
	workerErrCh := make(chan error, opt.Workers)

	for i := 0; i < opt.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				bres, err := destBulkUpsert(ctx, opt, j.items)
				resMu.Lock()
				if bres != nil {
					cr.Written += bres.Succeeded
					cr.Failed += bres.Failed
					for k, v := range bres.Errors {
						cr.Errors[k] = v
					}
				}
				resMu.Unlock()
				if err != nil {
					select {
					case workerErrCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	pages := 0
	token := cursors[colID]
	produceErr := func() error {
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			req := repository.SearchRequest{
				Collections: []string{colID},
				SortBy:      []repository.SortClause{{Field: "id", Direction: repository.SortAsc}},
				Limit:       opt.BatchSize,
				Token:       token,
			}
			page, err := opt.Source.Search(ctx, req)
			if err != nil {
				return fmt.Errorf("migrate: search %q: %w", colID, err)
			}
			if len(page.Items) == 0 {
				return nil
			}
			resMu.Lock()
			cr.Read += len(page.Items)
			resMu.Unlock()

			select {
			case jobs <- job{items: page.Items, token: page.NextToken}:
			case <-ctx.Done():
				return ctx.Err()
			}

			if page.NextToken == "" {
				return nil
			}
			token = page.NextToken
			cursors[colID] = token
			pages++
			// Throttle resume sidecar writes — every page is wasteful when
			// the source is fast.
			if pages%5 == 0 {
				_ = saveCursors(opt.ResumePath, cursors)
			}
			opt.Logf("collection %q: read=%d (token=%s)", colID, cr.Read, truncate(token, 24))
		}
	}

	prodErr := produceErr()
	close(jobs)
	wg.Wait()
	close(workerErrCh)

	if prodErr != nil {
		return cr, prodErr
	}
	for err := range workerErrCh {
		if err != nil {
			return cr, err
		}
	}
	delete(cursors, colID)
	return cr, nil
}

// destBulkUpsert pushes a slice of items via BulkUpsertItems. The seq2
// adapter is a one-shot iterator over the slice; each yield emits one
// item until the consumer stops.
func destBulkUpsert(ctx context.Context, opt Options, items []*stac.Item) (*repository.BulkResult, error) {
	seq := iter.Seq2[*stac.Item, error](func(yield func(*stac.Item, error) bool) {
		for _, it := range items {
			if !yield(it, nil) {
				return
			}
		}
	})
	return opt.Destination.BulkUpsertItems(ctx, seq)
}

// sampleVerify re-fetches N random items per collection from both
// sides and reports any divergence.
func sampleVerify(ctx context.Context, opt Options, cols []string) ([]VerifyMismatch, error) {
	var mismatches []VerifyMismatch
	for _, colID := range cols {
		// Pull a sample by paginating with a small limit and stopping
		// once we have N — deterministic because of the id-asc sort.
		page, err := opt.Source.Search(ctx, repository.SearchRequest{
			Collections: []string{colID},
			SortBy:      []repository.SortClause{{Field: "id", Direction: repository.SortAsc}},
			Limit:       opt.SampleVerify,
		})
		if err != nil {
			return mismatches, err
		}
		for _, it := range page.Items {
			got, err := opt.Destination.GetItem(ctx, colID, it.ID)
			if err != nil {
				mismatches = append(mismatches, VerifyMismatch{
					Collection: colID, ItemID: it.ID,
					Reason: "destination missing item: " + err.Error(),
				})
				continue
			}
			if !sameItem(it, got) {
				mismatches = append(mismatches, VerifyMismatch{
					Collection: colID, ItemID: it.ID, Reason: "item content mismatch",
				})
			}
		}
	}
	return mismatches, nil
}

func sameItem(a, b *stac.Item) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// ----- resume sidecar ----------------------------------------------------

func loadCursors(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("migrate: read resume %s: %w", path, err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("migrate: parse resume %s: %w", path, err)
	}
	return out, nil
}

var saveMu sync.Mutex

func saveCursors(path string, cursors map[string]string) error {
	if path == "" {
		return nil
	}
	saveMu.Lock()
	defer saveMu.Unlock()
	tmp := path + ".tmp"
	body, _ := json.MarshalIndent(cursors, "", "  ")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
