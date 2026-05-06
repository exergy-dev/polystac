// Package inmem implements an in-memory Repository for tests and the
// dev-mode default. Items live in a slice keyed by (collection, id);
// filtering uses pkg/cql2/eval as the truth oracle. The full read +
// write + Aggregator + Queryables surface is implemented so the parity
// matrix and the server's e2e tests can run without docker.
package inmem

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gtsgeom "github.com/exergy-dev/go-topology-suite/geom"
	gtspredicate "github.com/exergy-dev/go-topology-suite/predicate"

	"github.com/example/polystac/internal/backends"
	"github.com/example/polystac/pkg/cql2/eval"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/spatial"
	"github.com/example/polystac/pkg/stac"
)

const backendName = "inmem"

func init() {
	backends.Register(backendName, Open)
}

// Open is the registry constructor. It accepts (and ignores) any cfg —
// the in-memory backend has no configurable knobs.
func Open(_ context.Context, _ any) (repository.Repository, error) {
	return New(), nil
}

// Repo is the in-memory Repository. The zero value is not usable; call New.
type Repo struct {
	mu          sync.RWMutex
	collections map[string]*stac.Collection
	items       map[string]map[string]*stac.Item // collectionID → itemID → *Item
}

// New returns an empty in-memory repository.
func New() *Repo {
	return &Repo{
		collections: make(map[string]*stac.Collection),
		items:       make(map[string]map[string]*stac.Item),
	}
}

// Capabilities reports the in-memory backend's full feature set.
func (r *Repo) Capabilities() repository.Capabilities {
	return repository.Capabilities{
		Backend:                  backendName,
		SupportsTransactions:     true,
		SupportsBulkTransactions: true,
		SupportsFreeTextSearch:   true,
		SupportsFilterCQL2Text:   true,
		SupportsFilterCQL2JSON:   true,
		SupportedSortFields:      repository.SortFieldsAll,
		CountSemantics:           repository.CountExact,
		MaxItemLimit:             10000,
		Notes:                    []string{"in-memory backend; intended for tests and demos only"},
	}
}

// Health is a no-op.
func (r *Repo) Health(_ context.Context) error { return nil }

// Close is a no-op.
func (r *Repo) Close() error { return nil }

// ---------- Collections --------------------------------------------------

// GetCollection returns a deep-cloned collection.
func (r *Repo) GetCollection(_ context.Context, id string) (*stac.Collection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.collections[id]
	if !ok {
		return nil, fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
	}
	return c.Clone(), nil
}

// ListCollections returns a deterministic page (sorted by id).
func (r *Repo) ListCollections(_ context.Context, opts repository.ListCollectionsOptions) (*repository.Page[*stac.Collection], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.collections))
	for id := range r.collections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start, err := decodeOffset(opts.Token)
	if err != nil {
		return nil, fmt.Errorf("inmem: bad token: %w", repository.ErrInvalidInput)
	}
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	out := make([]*stac.Collection, 0, end-start)
	for _, id := range ids[start:end] {
		out = append(out, r.collections[id].Clone())
	}
	page := &repository.Page[*stac.Collection]{Items: out}
	if end < len(ids) {
		page.NextToken = encodeOffset(end)
	}
	if start > 0 {
		prev := start - limit
		if prev < 0 {
			prev = 0
		}
		page.PrevToken = encodeOffset(prev)
	}
	total := int64(len(ids))
	page.Matched = &total
	return page, nil
}

// UpsertCollection inserts or replaces a collection.
func (r *Repo) UpsertCollection(_ context.Context, c *stac.Collection) error {
	if c == nil || c.ID == "" {
		return fmt.Errorf("inmem: collection.id required: %w", repository.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collections[c.ID] = c.Clone()
	if _, ok := r.items[c.ID]; !ok {
		r.items[c.ID] = make(map[string]*stac.Item)
	}
	return nil
}

// DeleteCollection removes a collection and all of its items.
func (r *Repo) DeleteCollection(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.collections[id]; !ok {
		return fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
	}
	delete(r.collections, id)
	delete(r.items, id)
	return nil
}

// ---------- Items --------------------------------------------------------

func (r *Repo) GetItem(_ context.Context, collectionID, itemID string) (*stac.Item, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	col, ok := r.items[collectionID]
	if !ok {
		return nil, fmt.Errorf("collection %q: %w", collectionID, repository.ErrNotFound)
	}
	it, ok := col[itemID]
	if !ok {
		return nil, fmt.Errorf("item %q in %q: %w", itemID, collectionID, repository.ErrNotFound)
	}
	return it.Clone(), nil
}

func (r *Repo) UpsertItem(_ context.Context, item *stac.Item) error {
	if item == nil || item.ID == "" {
		return fmt.Errorf("inmem: item.id required: %w", repository.ErrInvalidInput)
	}
	if item.Collection == "" {
		return fmt.Errorf("inmem: item.collection required: %w", repository.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.collections[item.Collection]; !ok {
		return fmt.Errorf("collection %q: %w", item.Collection, repository.ErrNotFound)
	}
	col := r.items[item.Collection]
	if col == nil {
		col = make(map[string]*stac.Item)
		r.items[item.Collection] = col
	}
	col[item.ID] = item.Clone()
	return nil
}

func (r *Repo) DeleteItem(_ context.Context, collectionID, itemID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	col, ok := r.items[collectionID]
	if !ok {
		return fmt.Errorf("collection %q: %w", collectionID, repository.ErrNotFound)
	}
	if _, ok := col[itemID]; !ok {
		return fmt.Errorf("item %q in %q: %w", itemID, collectionID, repository.ErrNotFound)
	}
	delete(col, itemID)
	return nil
}

// BulkUpsertItems consumes the iterator and reports per-item failures.
func (r *Repo) BulkUpsertItems(ctx context.Context, items iter.Seq2[*stac.Item, error]) (*repository.BulkResult, error) {
	res := &repository.BulkResult{Errors: map[string]error{}}
	for item, err := range items {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if err != nil {
			id := "<unknown>"
			if item != nil {
				id = item.ID
			}
			res.Errors[id] = err
			res.Failed++
			continue
		}
		if err := r.UpsertItem(ctx, item); err != nil {
			res.Errors[item.ID] = err
			res.Failed++
			continue
		}
		res.Succeeded++
	}
	return res, nil
}

// ---------- Search -------------------------------------------------------

// Search returns a deterministic page of items matching req.
func (r *Repo) Search(_ context.Context, req repository.SearchRequest) (*repository.Page[*stac.Item], error) {
	r.mu.RLock()
	candidates := r.collectCandidates(req)
	r.mu.RUnlock()

	// Build the query-side gts geometry once; matchItem is called
	// per-item and we don't want to re-marshal it every time.
	var qBBoxGeom, qIntersectsGeom gtsgeom.Geometry
	if len(req.BBox) >= 4 {
		qBBoxGeom = spatial.BBoxPolygon(req.BBox[0], req.BBox[1], req.BBox[2], req.BBox[3])
	}
	if req.Intersects != nil {
		qIntersectsGeom, _ = spatial.FromSTAC(req.Intersects)
	}

	filtered := make([]*stac.Item, 0, len(candidates))
	for _, it := range candidates {
		ok, err := matchItem(it, req, qBBoxGeom, qIntersectsGeom)
		if err != nil {
			return nil, fmt.Errorf("inmem: filter: %w", err)
		}
		if ok {
			filtered = append(filtered, it)
		}
	}

	applySort(filtered, req.SortBy)

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 10000 {
		limit = 10000
	}
	start, err := decodeOffset(req.Token)
	if err != nil {
		return nil, fmt.Errorf("inmem: bad token: %w", repository.ErrInvalidInput)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := &repository.Page[*stac.Item]{
		Items: cloneItems(filtered[start:end]),
	}
	if end < len(filtered) {
		page.NextToken = encodeOffset(end)
	}
	if start > 0 {
		prev := start - limit
		if prev < 0 {
			prev = 0
		}
		page.PrevToken = encodeOffset(prev)
	}
	total := int64(len(filtered))
	page.Matched = &total
	return page, nil
}

// collectCandidates pulls the relevant items out of the store before
// applying the heavier per-item filters. Reads under r.mu.RLock.
func (r *Repo) collectCandidates(req repository.SearchRequest) []*stac.Item {
	scopeCollections := req.Collections
	if len(scopeCollections) == 0 {
		scopeCollections = make([]string, 0, len(r.items))
		for id := range r.items {
			scopeCollections = append(scopeCollections, id)
		}
	}
	idSet := map[string]struct{}{}
	for _, id := range req.IDs {
		idSet[id] = struct{}{}
	}
	out := make([]*stac.Item, 0, 64)
	for _, cid := range scopeCollections {
		col, ok := r.items[cid]
		if !ok {
			continue
		}
		for _, it := range col {
			if len(idSet) > 0 {
				if _, hit := idSet[it.ID]; !hit {
					continue
				}
			}
			out = append(out, it)
		}
	}
	return out
}

func matchItem(it *stac.Item, req repository.SearchRequest, qBBox, qIntersects gtsgeom.Geometry) (bool, error) {
	if qBBox != nil {
		if !intersectsBBox(it, req.BBox, qBBox) {
			return false, nil
		}
	}
	if req.Intersects != nil {
		if !intersectsGeometry(it, req.Intersects, qIntersects) {
			return false, nil
		}
	}
	if req.Datetime != nil {
		if !datetimeMatches(it, *req.Datetime) {
			return false, nil
		}
	}
	if req.Filter != nil {
		ok, err := eval.Match(req.Filter, it)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if len(req.Query) > 0 {
		if !queryMatches(it, req.Query) {
			return false, nil
		}
	}
	return true, nil
}

func intersectsBBox(it *stac.Item, bb []float64, qBBox gtsgeom.Geometry) bool {
	if itemG, ok := spatial.FromSTAC(it.Geometry); ok {
		if hit, err := gtspredicate.Intersects(itemG, qBBox); err == nil {
			return hit
		}
	}
	return bboxOverlaps(it, bb)
}

func intersectsGeometry(it *stac.Item, queryStac *stac.Geometry, qGeom gtsgeom.Geometry) bool {
	if qGeom != nil {
		if itemG, ok := spatial.FromSTAC(it.Geometry); ok {
			if hit, err := gtspredicate.Intersects(itemG, qGeom); err == nil {
				return hit
			}
		}
	}
	// Fallback path: when either side can't be rendered as a full
	// geometry, approximate with bbox overlap so empty/malformed
	// inputs don't silently drop matches.
	gb, ok := queryStac.BBox()
	if !ok {
		return true
	}
	if len(it.BBox) >= 4 {
		return bboxOverlaps(it, gb[:])
	}
	if it.Geometry != nil {
		if ib, ok := it.Geometry.BBox(); ok {
			return !(ib[2] < gb[0] || ib[0] > gb[2] || ib[3] < gb[1] || ib[1] > gb[3])
		}
	}
	return true
}

func bboxOverlaps(it *stac.Item, bb []float64) bool {
	if len(it.BBox) < 4 {
		return false
	}
	return !(it.BBox[2] < bb[0] || it.BBox[0] > bb[2] || it.BBox[3] < bb[1] || it.BBox[1] > bb[3])
}

func datetimeMatches(it *stac.Item, ti repository.TemporalInterval) bool {
	dt, ok := it.Properties["datetime"].(string)
	if !ok {
		return false
	}
	t, err := time.Parse(time.RFC3339, dt)
	if err != nil {
		return false
	}
	if ti.Start != nil && t.Before(*ti.Start) {
		return false
	}
	if ti.End != nil && t.After(*ti.End) {
		return false
	}
	return true
}

func queryMatches(it *stac.Item, q map[string]repository.Predicate) bool {
	for field, pred := range q {
		got := it.Properties[field]
		if !predicateMatches(got, pred) {
			return false
		}
	}
	return true
}

func predicateMatches(got any, pred repository.Predicate) bool {
	if pred.Eq != nil && !equalsAny(got, pred.Eq) {
		return false
	}
	if pred.Neq != nil && equalsAny(got, pred.Neq) {
		return false
	}
	if pred.Lt != nil && !numLess(got, pred.Lt) {
		return false
	}
	if pred.Lte != nil && !numLessEq(got, pred.Lte) {
		return false
	}
	if pred.Gt != nil && !numLess(pred.Gt, got) {
		return false
	}
	if pred.Gte != nil && !numLessEq(pred.Gte, got) {
		return false
	}
	if len(pred.In) > 0 {
		hit := false
		for _, v := range pred.In {
			if equalsAny(got, v) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if pred.StartsWith != "" {
		s, ok := got.(string)
		if !ok || !strings.HasPrefix(s, pred.StartsWith) {
			return false
		}
	}
	if pred.EndsWith != "" {
		s, ok := got.(string)
		if !ok || !strings.HasSuffix(s, pred.EndsWith) {
			return false
		}
	}
	if pred.Contains != "" {
		s, ok := got.(string)
		if !ok || !strings.Contains(s, pred.Contains) {
			return false
		}
	}
	return true
}

func equalsAny(a, b any) bool {
	if af, ok := eval.ToFloat64(a); ok {
		if bf, ok := eval.ToFloat64(b); ok {
			return af == bf
		}
	}
	return a == b
}

func numLess(a, b any) bool {
	cmp, ok := eval.CompareValues(a, b)
	return ok && cmp < 0
}

func numLessEq(a, b any) bool {
	cmp, ok := eval.CompareValues(a, b)
	return ok && cmp <= 0
}

func applySort(items []*stac.Item, sortBy []repository.SortClause) {
	if len(sortBy) == 0 {
		// Deterministic default: by id ascending.
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		for _, s := range sortBy {
			a := propertyOf(items[i], s.Field)
			b := propertyOf(items[j], s.Field)
			cmp := compareSortValues(a, b)
			if cmp == 0 {
				continue
			}
			if s.Direction == repository.SortDesc {
				return cmp > 0
			}
			return cmp < 0
		}
		return items[i].ID < items[j].ID
	})
}

func propertyOf(it *stac.Item, name string) any {
	switch name {
	case "id":
		return it.ID
	case "collection":
		return it.Collection
	}
	return it.Properties[name]
}

func compareSortValues(a, b any) int {
	if cmp, ok := eval.CompareValues(a, b); ok {
		return cmp
	}
	return 0
}

// ---------- Aggregator ---------------------------------------------------

// Aggregate implements the Aggregation extension. The supported AggTypes
// are: "frequency" (count by exact value of a property) and "stats"
// (min/max/avg/count for numeric properties).
func (r *Repo) Aggregate(ctx context.Context, req repository.AggregationRequest) (*repository.AggregationResponse, error) {
	page, err := r.Search(ctx, req.Search)
	if err != nil {
		return nil, err
	}
	out := &repository.AggregationResponse{Aggregations: map[string]any{}}
	for _, a := range req.Aggs {
		switch a.AggType {
		case "frequency":
			counts := map[string]int{}
			for _, it := range page.Items {
				v := propertyOf(it, a.Field)
				key := fmt.Sprint(v)
				counts[key]++
			}
			out.Aggregations[a.Name] = counts
		case "stats":
			var n int
			var sum, mn, mx float64
			mn = +1e308
			mx = -1e308
			for _, it := range page.Items {
				v := propertyOf(it, a.Field)
				f, ok := eval.ToFloat64(v)
				if !ok {
					continue
				}
				if f < mn {
					mn = f
				}
				if f > mx {
					mx = f
				}
				sum += f
				n++
			}
			s := map[string]any{"count": n}
			if n > 0 {
				s["min"] = mn
				s["max"] = mx
				s["avg"] = sum / float64(n)
			}
			out.Aggregations[a.Name] = s
		default:
			return nil, fmt.Errorf("inmem: unknown agg type %q: %w", a.AggType, repository.ErrInvalidInput)
		}
	}
	return out, nil
}

// ---------- Queryables ---------------------------------------------------

// Queryables returns a JSON-Schema-shaped document listing the property
// keys observed across the collection's items. It is intentionally
// minimal — sufficient to satisfy the Filter extension's discovery
// endpoint without claiming richer schema information than the in-memory
// store actually has.
func (r *Repo) Queryables(_ context.Context, collectionID string) (*repository.QueryablesDocument, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	props := map[string]any{
		"id":         map[string]any{"type": "string"},
		"collection": map[string]any{"type": "string"},
		"datetime":   map[string]any{"type": "string", "format": "date-time"},
	}
	source := r.items
	if collectionID != "" {
		col, ok := r.items[collectionID]
		if !ok {
			return nil, fmt.Errorf("collection %q: %w", collectionID, repository.ErrNotFound)
		}
		source = map[string]map[string]*stac.Item{collectionID: col}
	}
	for _, col := range source {
		for _, it := range col {
			for k := range it.Properties {
				if _, exists := props[k]; exists {
					continue
				}
				props[k] = map[string]any{}
			}
		}
	}
	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2019-09/schema",
		"$id":         fmt.Sprintf("/collections/%s/queryables", collectionID),
		"type":        "object",
		"title":       "Queryables for " + collectionID,
		"properties":  props,
		"description": "queryable properties (in-memory backend)",
	}
	return &repository.QueryablesDocument{Schema: schema}, nil
}

// ---------- helpers ------------------------------------------------------

func encodeOffset(n int) string {
	if n <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(n)))
}

func decodeOffset(tok string) (int, error) {
	if tok == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return 0, errors.New("invalid token encoding")
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, errors.New("invalid token")
	}
	return n, nil
}

func cloneItems(in []*stac.Item) []*stac.Item {
	out := make([]*stac.Item, len(in))
	for i, it := range in {
		out[i] = it.Clone()
	}
	return out
}
