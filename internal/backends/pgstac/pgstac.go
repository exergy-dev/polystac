package pgstac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/polystac/internal/backends"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

const backendName = "pgstac"

// MinSchemaVersion is the lowest pgstac schema version PolyStac supports.
// Bumped together with any pgstac SQL surface PolyStac depends on.
const MinSchemaVersion = "0.7.0"

func init() {
	backends.Register(backendName, Open)
}

// Open is the registry constructor. It reads the POLYSTAC_PG_* subtree
// from the *config.Config, opens a pool, validates the pgstac schema
// version, and returns the Repository.
func Open(ctx context.Context, anyCfg any) (repository.Repository, error) {
	cfg, ok := anyCfg.(*config.Config)
	if !ok {
		return nil, fmt.Errorf("pgstac: expected *config.Config, got %T", anyCfg)
	}
	pc, err := configFromMap(cfg.BackendConfig)
	if err != nil {
		return nil, err
	}
	pool, err := newPool(ctx, pc)
	if err != nil {
		return nil, err
	}
	useAPIHydrate := strings.EqualFold(cfg.BackendConfig["use_api_hydrate"], "true")
	r := &Repo{pool: pool, useAPIHydrate: useAPIHydrate}
	if err := r.checkSchema(ctx); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

// Repo implements repository.Repository against pgstac.
type Repo struct {
	pool          *pgxpool.Pool
	useAPIHydrate bool
}

// Capabilities reports the pgstac feature set.
func (r *Repo) Capabilities() repository.Capabilities {
	return repository.Capabilities{
		Backend:                  backendName,
		SupportsTransactions:     true,
		SupportsBulkTransactions: true,
		SupportsFreeTextSearch:   false, // off by default; opt-in with pg_trgm
		SupportsFilterCQL2Text:   true,
		SupportsFilterCQL2JSON:   true,
		SupportedSortFields:      repository.SortFieldsAll,
		CountSemantics:           repository.CountExact,
		MaxItemLimit:             10000,
		Notes: []string{
			"counts are exact but expensive at scale (>10M items)",
			"set USE_API_HYDRATE=true to hydrate items in PolyStac instead of in pgstac",
		},
	}
}

// Health pings the pool.
func (r *Repo) Health(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// Close releases the pool.
func (r *Repo) Close() error {
	if r.pool != nil {
		r.pool.Close()
	}
	return nil
}

// checkSchema reads pgstac.get_version() and refuses to start on a
// version older than MinSchemaVersion. Operators run the upstream
// pypgstac migration tool to bring the schema up to date — PolyStac
// does not embed migrations (SDD §8.1).
func (r *Repo) checkSchema(ctx context.Context) error {
	var version string
	err := r.pool.QueryRow(ctx, `SELECT pgstac.get_version()`).Scan(&version)
	if err != nil {
		return fmt.Errorf("pgstac: schema check: %w", err)
	}
	if !versionAtLeast(version, MinSchemaVersion) {
		return fmt.Errorf("pgstac: schema version %s < required %s — run pypgstac migrate", version, MinSchemaVersion)
	}
	return nil
}

// ---------- collections --------------------------------------------------

// GetCollection reads a single collection.
func (r *Repo) GetCollection(ctx context.Context, id string) (*stac.Collection, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `SELECT pgstac.get_collection($1)`, id).Scan(&raw)
	if err != nil {
		return nil, mapPgErr(err, "collection "+id)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
	}
	var c stac.Collection
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("pgstac: decode collection: %w", err)
	}
	return &c, nil
}

// ListCollections fetches the collection list. pgstac.all_collections()
// returns a JSON array; PolyStac slices it into pages locally because
// the collection count is bounded.
func (r *Repo) ListCollections(ctx context.Context, opts repository.ListCollectionsOptions) (*repository.Page[*stac.Collection], error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `SELECT pgstac.all_collections()`).Scan(&raw)
	if err != nil {
		return nil, mapPgErr(err, "list collections")
	}
	var all []*stac.Collection
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("pgstac: decode collections: %w", err)
	}
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	end := limit
	if end > len(all) {
		end = len(all)
	}
	page := &repository.Page[*stac.Collection]{Items: all[:end]}
	if end < len(all) {
		page.NextToken = "offset:" + fmt.Sprint(end)
	}
	total := int64(len(all))
	page.Matched = &total
	return page, nil
}

// UpsertCollection creates or replaces a collection.
func (r *Repo) UpsertCollection(ctx context.Context, c *stac.Collection) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `SELECT pgstac.upsert_collection($1::jsonb)`, body)
	return mapPgErr(err, "upsert_collection")
}

// DeleteCollection removes a collection (and its items, per pgstac).
func (r *Repo) DeleteCollection(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `SELECT pgstac.delete_collection($1)`, id)
	return mapPgErr(err, "delete_collection "+id)
}

// ---------- items --------------------------------------------------------

// GetItem reads a single item.
func (r *Repo) GetItem(ctx context.Context, collectionID, itemID string) (*stac.Item, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `SELECT pgstac.get_item($1, $2)`, itemID, collectionID).Scan(&raw)
	if err != nil {
		return nil, mapPgErr(err, "item "+itemID)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("item %q in %q: %w", itemID, collectionID, repository.ErrNotFound)
	}
	var it stac.Item
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, fmt.Errorf("pgstac: decode item: %w", err)
	}
	return &it, nil
}

// UpsertItem creates or updates a single item.
func (r *Repo) UpsertItem(ctx context.Context, item *stac.Item) error {
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `SELECT pgstac.upsert_item($1::jsonb)`, body)
	return mapPgErr(err, "upsert_item")
}

// DeleteItem removes a single item.
func (r *Repo) DeleteItem(ctx context.Context, collectionID, itemID string) error {
	_, err := r.pool.Exec(ctx, `SELECT pgstac.delete_item($1, $2)`, itemID, collectionID)
	return mapPgErr(err, "delete_item")
}

// BulkUpsertItems streams items into pgstac.create_items in chunks.
// Bounded chunk size keeps payload size manageable; pgstac performs the
// per-row work in a server-side loop.
func (r *Repo) BulkUpsertItems(ctx context.Context, items iter.Seq2[*stac.Item, error]) (*repository.BulkResult, error) {
	const chunk = 500
	res := &repository.BulkResult{Errors: map[string]error{}}
	batch := make([]json.RawMessage, 0, chunk)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		payload, _ := json.Marshal(batch)
		_, err := r.pool.Exec(ctx, `SELECT pgstac.create_items($1::jsonb)`, payload)
		if err != nil {
			// Surface the failure on every item in the batch — pgstac's
			// bulk insert is all-or-nothing per call.
			err = mapPgErr(err, "create_items")
			res.Failed += len(batch)
			batch = batch[:0]
			return err
		}
		res.Succeeded += len(batch)
		batch = batch[:0]
		return nil
	}
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
		raw, err := json.Marshal(item)
		if err != nil {
			res.Errors[item.ID] = err
			res.Failed++
			continue
		}
		batch = append(batch, raw)
		if len(batch) >= chunk {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	if err := flush(); err != nil {
		return res, err
	}
	return res, nil
}

// ---------- search -------------------------------------------------------

// Search runs pgstac.search with the translated payload and decodes the
// returned ItemCollection.
func (r *Repo) Search(ctx context.Context, req repository.SearchRequest) (*repository.Page[*stac.Item], error) {
	payload, err := translateSearch(req)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	err = r.pool.QueryRow(ctx, `SELECT pgstac.search($1::jsonb)`, payload).Scan(&raw)
	if err != nil {
		return nil, mapPgErr(err, "search")
	}
	var fc struct {
		Features []*stac.Item `json:"features"`
		Links    []struct {
			Rel  string         `json:"rel"`
			Body map[string]any `json:"body"`
		} `json:"links"`
		NumberMatched *int64 `json:"numberMatched,omitempty"`
		Context       *struct {
			Matched *int64 `json:"matched,omitempty"`
		} `json:"context,omitempty"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("pgstac: decode search: %w", err)
	}
	page := &repository.Page[*stac.Item]{Items: fc.Features}
	for _, l := range fc.Links {
		if l.Rel == "next" {
			if t, ok := l.Body["token"].(string); ok {
				page.NextToken = t
			}
		}
		if l.Rel == "previous" || l.Rel == "prev" {
			if t, ok := l.Body["token"].(string); ok {
				page.PrevToken = t
			}
		}
	}
	switch {
	case fc.NumberMatched != nil:
		page.Matched = fc.NumberMatched
	case fc.Context != nil && fc.Context.Matched != nil:
		page.Matched = fc.Context.Matched
	}
	return page, nil
}

// ---------- queryables ---------------------------------------------------

// Queryables returns the collection's queryables document. pgstac's
// `get_queryables(collection_id)` returns a JSON Schema; we hand it
// through verbatim.
func (r *Repo) Queryables(ctx context.Context, collectionID string) (*repository.QueryablesDocument, error) {
	var raw json.RawMessage
	q := `SELECT pgstac.get_queryables($1)`
	args := []any{collectionID}
	if collectionID == "" {
		q = `SELECT pgstac.get_queryables(NULL)`
		args = nil
	}
	err := r.pool.QueryRow(ctx, q, args...).Scan(&raw)
	if err != nil {
		return nil, mapPgErr(err, "queryables")
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("pgstac: decode queryables: %w", err)
	}
	return &repository.QueryablesDocument{Schema: schema}, nil
}

// ---------- aggregation --------------------------------------------------

// Aggregate runs the pgstac aggregation flavor. Each agg is sent as a
// separate call so a single bad agg doesn't poison the rest. The set of
// supported AggTypes is what pgstac's `aggregate` function recognizes;
// unknown types are mapped to ErrInvalidInput.
func (r *Repo) Aggregate(ctx context.Context, req repository.AggregationRequest) (*repository.AggregationResponse, error) {
	out := &repository.AggregationResponse{Aggregations: map[string]any{}}
	search, err := translateSearch(req.Search)
	if err != nil {
		return nil, err
	}
	for _, a := range req.Aggs {
		body := map[string]any{
			"search":   json.RawMessage(search),
			"agg_type": a.AggType,
			"field":    a.Field,
			"params":   a.Params,
		}
		raw, _ := json.Marshal(body)
		var result json.RawMessage
		err := r.pool.QueryRow(ctx, `SELECT pgstac.aggregate($1::jsonb)`, raw).Scan(&result)
		if err != nil {
			return nil, mapPgErr(err, "aggregate "+a.Name)
		}
		var v any
		if err := json.Unmarshal(result, &v); err != nil {
			return nil, fmt.Errorf("pgstac: decode aggregation %q: %w", a.Name, err)
		}
		out.Aggregations[a.Name] = v
	}
	return out, nil
}

// ---------- helpers ------------------------------------------------------

func mapPgErr(err error, ctxStr string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrNotFound)
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "duplicate") || strings.Contains(msg, "conflict"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrConflict)
	case strings.Contains(msg, "does not exist") || strings.Contains(msg, "not found"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrNotFound)
	case strings.Contains(msg, "invalid") || strings.Contains(msg, "syntax"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrInvalidInput)
	}
	return fmt.Errorf("%s: %w", ctxStr, err)
}

// versionAtLeast returns true if got >= want under semver-style dotted
// comparison. Tags like "v0.8.1" or "0.8.1-rc1" are accepted.
func versionAtLeast(got, want string) bool {
	g := splitVersion(got)
	w := splitVersion(want)
	for i := 0; i < 3; i++ {
		if g[i] > w[i] {
			return true
		}
		if g[i] < w[i] {
			return false
		}
	}
	return true
}

func splitVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		var n int
		_, _ = fmt.Sscanf(parts[i], "%d", &n)
		out[i] = n
	}
	return out
}
