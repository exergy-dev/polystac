package opensearch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"iter"
	"strings"

	"github.com/example/polystac/internal/backends"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

const backendName = "opensearch"

func init() {
	backends.Register("opensearch", Open)
	backends.Register("elasticsearch", Open)
}

// Open is the registry constructor.
func Open(ctx context.Context, anyCfg any) (repository.Repository, error) {
	cfg, ok := anyCfg.(*config.Config)
	if !ok {
		return nil, fmt.Errorf("opensearch: expected *config.Config, got %T", anyCfg)
	}
	env := cfg.BackendConfig
	hosts := env["hosts"]
	if hosts == "" {
		return nil, errors.New("opensearch: POLYSTAC_ES_HOSTS is required")
	}
	verify := !strings.EqualFold(env["verify_certs"], "false")
	client, err := NewHTTPClient(hosts, env["username"], env["password"], verify)
	if err != nil {
		return nil, err
	}
	if r := env["refresh"]; r != "" {
		client.Refresh = r
	}
	indexPrefix := env["index_prefix"]
	if indexPrefix == "" {
		indexPrefix = "items_"
	}
	collectionsIndex := env["collections_index"]
	if collectionsIndex == "" {
		collectionsIndex = "collections"
	}
	r := &Repo{
		client:           client,
		indexPrefix:      indexPrefix,
		collectionsIndex: collectionsIndex,
	}
	if err := r.ensureTemplates(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// Repo implements Repository against OpenSearch / Elasticsearch.
type Repo struct {
	client           SearchClient
	indexPrefix      string
	collectionsIndex string
}

// Capabilities reports the feature set.
func (r *Repo) Capabilities() repository.Capabilities {
	return repository.Capabilities{
		Backend:                  backendName,
		SupportsTransactions:     true,
		SupportsBulkTransactions: true,
		SupportsFreeTextSearch:   true,
		SupportsFilterCQL2Text:   true,
		SupportsFilterCQL2JSON:   true,
		SupportedSortFields:      repository.SortFieldsIndexedOnly,
		CountSemantics:           repository.CountApproximate,
		MaxItemLimit:             10000,
		Notes: []string{
			"numberMatched is approximate above 10,000 (track_total_hits cap)",
			"sort on text fields requires a keyword sub-field",
		},
	}
}

// Health calls the cluster root.
func (r *Repo) Health(ctx context.Context) error { return r.client.Ping(ctx) }

// Close is a no-op.
func (r *Repo) Close() error { return nil }

// ensureTemplates installs both index templates (items and
// collections). Idempotent across restarts. Templates apply to indices
// created AFTER the template — a pre-existing collections index with
// the default dynamic mapping must be deleted manually for the new
// mapping to take effect.
func (r *Repo) ensureTemplates(ctx context.Context) error {
	if err := r.client.PutIndexTemplate(ctx, itemTemplateName, itemIndexTemplate(r.indexPrefix)); err != nil {
		return fmt.Errorf("opensearch: install items template: %w", err)
	}
	if err := r.client.PutIndexTemplate(ctx, collectionsTemplateName, collectionsIndexTemplate(r.collectionsIndex)); err != nil {
		return fmt.Errorf("opensearch: install collections template: %w", err)
	}
	return nil
}

// ---------- collections --------------------------------------------------

func (r *Repo) GetCollection(ctx context.Context, id string) (*stac.Collection, error) {
	body, err := r.client.Get(ctx, r.collectionsIndex, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
		}
		return nil, err
	}
	var doc struct {
		Source stac.Collection `json:"_source"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("opensearch: decode collection: %w", err)
	}
	return &doc.Source, nil
}

func (r *Repo) ListCollections(ctx context.Context, opts repository.ListCollectionsOptions) (*repository.Page[*stac.Collection], error) {
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	body, _ := json.Marshal(map[string]any{
		"size": limit,
		"sort": []any{map[string]any{"id": map[string]any{"order": "asc"}}},
	})
	raw, err := r.client.Search(ctx, r.collectionsIndex, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source stac.Collection `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]*stac.Collection, 0, len(resp.Hits.Hits))
	for _, h := range resp.Hits.Hits {
		c := h.Source
		out = append(out, &c)
	}
	page := &repository.Page[*stac.Collection]{Items: out}
	page.Matched = &resp.Hits.Total.Value
	return page, nil
}

func (r *Repo) UpsertCollection(ctx context.Context, c *stac.Collection) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := r.client.Index(ctx, r.collectionsIndex, c.ID, body); err != nil {
		return err
	}
	// Also ensure the per-collection items index exists with the
	// templated mapping.
	emptyItem := []byte(`{"id":"__init__"}`)
	if err := r.client.Index(ctx, r.itemsIndex(c.ID), "__init__", emptyItem); err == nil {
		_ = r.client.Delete(ctx, r.itemsIndex(c.ID), "__init__")
	}
	return nil
}

func (r *Repo) DeleteCollection(ctx context.Context, id string) error {
	if err := r.client.Delete(ctx, r.collectionsIndex, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
		}
		return err
	}
	return r.client.DeleteIndex(ctx, r.itemsIndex(id))
}

// ---------- items --------------------------------------------------------

func (r *Repo) GetItem(ctx context.Context, collectionID, itemID string) (*stac.Item, error) {
	body, err := r.client.Get(ctx, r.itemsIndex(collectionID), itemID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("item %q: %w", itemID, repository.ErrNotFound)
		}
		return nil, err
	}
	var doc struct {
		Source stac.Item `json:"_source"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("opensearch: decode item: %w", err)
	}
	return &doc.Source, nil
}

func (r *Repo) UpsertItem(ctx context.Context, item *stac.Item) error {
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return r.client.Index(ctx, r.itemsIndex(item.Collection), item.ID, body)
}

func (r *Repo) DeleteItem(ctx context.Context, collectionID, itemID string) error {
	if err := r.client.Delete(ctx, r.itemsIndex(collectionID), itemID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("item %q: %w", itemID, repository.ErrNotFound)
		}
		return err
	}
	return nil
}

// BulkUpsertItems batches items into a _bulk request. Items in the same
// batch may target different per-collection indices.
func (r *Repo) BulkUpsertItems(ctx context.Context, items iter.Seq2[*stac.Item, error]) (*repository.BulkResult, error) {
	const chunk = 500
	res := &repository.BulkResult{Errors: map[string]error{}}
	var buf bytes.Buffer
	count := 0
	flush := func() error {
		if count == 0 {
			return nil
		}
		resp, err := r.client.Bulk(ctx, buf.Bytes())
		if err != nil {
			res.Failed += count
			buf.Reset()
			count = 0
			return err
		}
		for _, item := range resp.Items {
			for _, op := range item {
				m, _ := op.(map[string]any)
				id, _ := m["_id"].(string)
				status, _ := m["status"].(float64)
				if status >= 400 {
					res.Failed++
					res.Errors[id] = fmt.Errorf("opensearch bulk: status %v", status)
				} else {
					res.Succeeded++
				}
			}
		}
		buf.Reset()
		count = 0
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
		header := map[string]any{
			"index": map[string]any{"_index": r.itemsIndex(item.Collection), "_id": item.ID},
		}
		hb, _ := json.Marshal(header)
		buf.Write(hb)
		buf.WriteByte('\n')
		body, _ := json.Marshal(item)
		buf.Write(body)
		buf.WriteByte('\n')
		count++
		if count >= chunk {
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

func (r *Repo) Search(ctx context.Context, req repository.SearchRequest) (*repository.Page[*stac.Item], error) {
	after, err := decodeToken(req.Token)
	if err != nil {
		return nil, fmt.Errorf("opensearch: bad token: %w", repository.ErrInvalidInput)
	}
	body, err := translateSearch(req, after)
	if err != nil {
		return nil, err
	}
	bodyJSON, _ := json.Marshal(body)

	indices := strings.Join(prefixed(req.Collections, r.indexPrefix), ",")
	if indices == "" {
		indices = r.indexPrefix + "*"
	}
	raw, err := r.client.Search(ctx, indices, bodyJSON)
	if err != nil {
		// "no such index" → empty result rather than error so a search
		// against an empty cluster doesn't 5xx.
		if strings.Contains(err.Error(), "index_not_found") {
			zero := int64(0)
			return &repository.Page[*stac.Item]{Items: nil, Matched: &zero}, nil
		}
		return nil, err
	}
	var resp struct {
		Hits struct {
			Total struct {
				Value    int64  `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Source stac.Item `json:"_source"`
				Sort   []any     `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("opensearch: decode search: %w", err)
	}
	items := make([]*stac.Item, 0, len(resp.Hits.Hits))
	var lastSort []any
	for _, h := range resp.Hits.Hits {
		it := h.Source
		items = append(items, &it)
		lastSort = h.Sort
	}
	page := &repository.Page[*stac.Item]{Items: items}
	page.Matched = &resp.Hits.Total.Value
	page.Approximate = resp.Hits.Total.Relation != "eq"
	if len(lastSort) > 0 {
		tok, err := encodeToken(lastSort)
		if err == nil {
			page.NextToken = tok
		}
	}
	return page, nil
}

// ---------- queryables ---------------------------------------------------

// Queryables returns a placeholder schema document. A richer impl can
// inspect the index mapping and return per-field types; the placeholder
// satisfies the Filter extension's discovery endpoint.
func (r *Repo) Queryables(_ context.Context, collectionID string) (*repository.QueryablesDocument, error) {
	id := "/queryables"
	if collectionID != "" {
		id = "/collections/" + collectionID + "/queryables"
	}
	return &repository.QueryablesDocument{Schema: map[string]any{
		"$schema":     "https://json-schema.org/draft/2019-09/schema",
		"$id":         id,
		"type":        "object",
		"title":       "Queryables for " + collectionID,
		"properties": map[string]any{
			"id":         map[string]any{"type": "string"},
			"collection": map[string]any{"type": "string"},
			"datetime":   map[string]any{"type": "string", "format": "date-time"},
			"geometry":   map[string]any{"type": "object"},
		},
		"description": "queryable properties (opensearch backend)",
	}}, nil
}

// ---------- helpers ------------------------------------------------------

func (r *Repo) itemsIndex(collectionID string) string {
	return r.indexPrefix + sanitizeIndexName(collectionID)
}

// sanitizeIndexName replaces characters that aren't legal in OS index
// names. Keeps the user-visible collection ID round-trippable via FNV.
func sanitizeIndexName(s string) string {
	clean := strings.Builder{}
	clean.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			clean.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			clean.WriteRune(r + 32)
		default:
			clean.WriteByte('-')
		}
	}
	out := clean.String()
	out = strings.Trim(out, "-_")
	if out == "" {
		h := fnv.New64a()
		_, _ = h.Write([]byte(s))
		return fmt.Sprintf("c%016x", h.Sum64())
	}
	return out
}

func prefixed(collections []string, prefix string) []string {
	out := make([]string, 0, len(collections))
	for _, c := range collections {
		out = append(out, prefix+sanitizeIndexName(c))
	}
	return out
}

func encodeToken(sort []any) (string, error) {
	b, err := json.Marshal(sort)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeToken(tok string) ([]any, error) {
	if tok == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, err
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
