package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// fakeClient is a tiny in-memory SearchClient used by unit tests. It
// captures the last Search body and lets tests preload responses.
type fakeClient struct {
	docs              map[string]map[string]json.RawMessage // index → id → doc
	lastSearchBody    []byte
	lastSearchIndex   string
	searchResponse    []byte
	pingErr           error
	indexTemplateBody []byte
}

func newFake() *fakeClient {
	return &fakeClient{docs: map[string]map[string]json.RawMessage{}}
}

func (f *fakeClient) Search(_ context.Context, index string, body []byte) ([]byte, error) {
	f.lastSearchIndex = index
	f.lastSearchBody = body
	if f.searchResponse != nil {
		return f.searchResponse, nil
	}
	// Echo all matching docs as a fake hits envelope.
	matched := []map[string]any{}
	for ix, ids := range f.docs {
		if !indexMatches(ix, index) {
			continue
		}
		for id, doc := range ids {
			matched = append(matched, map[string]any{
				"_index": ix, "_id": id, "_source": json.RawMessage(doc),
				"sort": []any{id},
			})
		}
	}
	resp := map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": len(matched), "relation": "eq"},
			"hits":  matched,
		},
	}
	out, _ := json.Marshal(resp)
	return out, nil
}

func indexMatches(idx, query string) bool {
	if query == "_all" {
		return true
	}
	for _, q := range strings.Split(query, ",") {
		if strings.HasSuffix(q, "*") {
			if strings.HasPrefix(idx, strings.TrimSuffix(q, "*")) {
				return true
			}
		} else if idx == q {
			return true
		}
	}
	return false
}

func (f *fakeClient) Index(_ context.Context, index, id string, body []byte) error {
	if _, ok := f.docs[index]; !ok {
		f.docs[index] = map[string]json.RawMessage{}
	}
	f.docs[index][id] = append(json.RawMessage(nil), body...)
	return nil
}

func (f *fakeClient) Get(_ context.Context, index, id string) ([]byte, error) {
	doc, ok := f.docs[index][id]
	if !ok {
		return nil, ErrNotFound
	}
	out, _ := json.Marshal(map[string]any{"_index": index, "_id": id, "_source": json.RawMessage(doc), "found": true})
	return out, nil
}

func (f *fakeClient) Delete(_ context.Context, index, id string) error {
	if _, ok := f.docs[index][id]; !ok {
		return ErrNotFound
	}
	delete(f.docs[index], id)
	return nil
}

func (f *fakeClient) Bulk(_ context.Context, body []byte) (BulkResponse, error) {
	// Parse the NDJSON bulk body and apply each index op.
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	items := []map[string]any{}
	for i := 0; i < len(lines); i += 2 {
		var hdr map[string]map[string]string
		if err := json.Unmarshal([]byte(lines[i]), &hdr); err != nil {
			return BulkResponse{}, err
		}
		op := hdr["index"]
		if _, ok := f.docs[op["_index"]]; !ok {
			f.docs[op["_index"]] = map[string]json.RawMessage{}
		}
		f.docs[op["_index"]][op["_id"]] = json.RawMessage(lines[i+1])
		items = append(items, map[string]any{"index": map[string]any{"_id": op["_id"], "status": float64(201)}})
	}
	return BulkResponse{Items: items}, nil
}

func (f *fakeClient) DeleteIndex(_ context.Context, index string) error {
	delete(f.docs, index)
	return nil
}

func (f *fakeClient) IndexTemplateExists(_ context.Context, _ string) (bool, error) {
	return f.indexTemplateBody != nil, nil
}

func (f *fakeClient) PutIndexTemplate(_ context.Context, _ string, body []byte) error {
	f.indexTemplateBody = body
	return nil
}

func (f *fakeClient) Ping(_ context.Context) error { return f.pingErr }

func newTestRepo() (*Repo, *fakeClient) {
	fc := newFake()
	return &Repo{client: fc, indexPrefix: "items_", collectionsIndex: "collections"}, fc
}

func TestRepoCRUDPath(t *testing.T) {
	r, _ := newTestRepo()
	ctx := context.Background()

	// Upsert a collection — also bootstraps the per-collection index.
	col := &stac.Collection{ID: "c1", Description: "x", License: "y"}
	if err := r.UpsertCollection(ctx, col); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetCollection(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "c1" {
		t.Errorf("id: %q", got.ID)
	}

	// Upsert two items.
	for _, id := range []string{"a", "b"} {
		it := &stac.Item{
			ID: id, Collection: "c1",
			Properties: stac.ItemProperties{"datetime": "2024-01-01T00:00:00Z"},
		}
		if err := r.UpsertItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}

	// Search returns both.
	page, err := r.Search(ctx, repository.SearchRequest{Collections: []string{"c1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Errorf("search returned %d items: %+v", len(page.Items), page.Items)
	}

	// Delete an item — get returns ErrNotFound.
	if err := r.DeleteItem(ctx, "c1", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetItem(ctx, "c1", "a"); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBulkUpsert(t *testing.T) {
	r, fc := newTestRepo()
	ctx := context.Background()
	if err := r.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "x", License: "y"}); err != nil {
		t.Fatal(err)
	}
	stream := iter.Seq2[*stac.Item, error](func(yield func(*stac.Item, error) bool) {
		for i := 0; i < 3; i++ {
			yield(&stac.Item{
				ID: "id-" + string(rune('0'+i)), Collection: "c1",
				Properties: stac.ItemProperties{"datetime": "2024-01-01T00:00:00Z"},
			}, nil)
		}
	})
	res, err := r.BulkUpsertItems(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 3 || res.Failed != 0 {
		t.Errorf("bulk: %+v", res)
	}
	if len(fc.docs["items_c1"]) != 3 {
		t.Errorf("indexed %d, want 3", len(fc.docs["items_c1"]))
	}
}

func TestSearchPassesTranslatedBody(t *testing.T) {
	r, fc := newTestRepo()
	_, _ = r.Search(context.Background(), repository.SearchRequest{Collections: []string{"c1"}, Limit: 5})
	if !strings.Contains(string(fc.lastSearchBody), `"size":5`) {
		t.Errorf("size not propagated: %s", fc.lastSearchBody)
	}
	if fc.lastSearchIndex != "items_c1" {
		t.Errorf("index: %q", fc.lastSearchIndex)
	}
}

func TestRoundTripToken(t *testing.T) {
	tok, err := encodeToken([]any{"x", float64(42)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "x" || got[1] != float64(42) {
		t.Errorf("round trip: %v", got)
	}
}
