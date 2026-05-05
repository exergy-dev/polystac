package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/polystac/internal/app"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/stac"
)

// TestSmokeEndToEnd boots the full app.Build pipeline against the inmem
// backend, then exercises the canonical demo path: create collection,
// upsert items, list, search, get. This is the Gate 1 synchronization
// point reduced to a single Go test — when this is green, the binary
// works end-to-end.
func TestSmokeEndToEnd(t *testing.T) {
	cfg, err := config.Load(nil, map[string]string{"POLYSTAC_BACKEND": "inmem"})
	if err != nil {
		t.Fatal(err)
	}
	srv, _, cleanup, err := app.Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	h := srv.Handler()

	// 1. Landing page is reachable and lists conformance classes.
	rec := do(t, h, "GET", "/", nil)
	assertStatus(t, rec, 200)
	var landing struct {
		Type       string   `json:"type"`
		ID         string   `json:"id"`
		ConformsTo []string `json:"conformsTo"`
	}
	mustJSON(t, rec, &landing)
	if landing.Type != "Catalog" || landing.ID == "" {
		t.Fatalf("landing: %+v", landing)
	}
	if len(landing.ConformsTo) == 0 {
		t.Fatal("no conformance classes")
	}

	// 2. Create a collection.
	col := stac.Collection{ID: "c1", Description: "smoke", License: "x"}
	rec = doJSON(t, h, "POST", "/collections", col)
	assertStatus(t, rec, 201)

	// 3. Upsert two items.
	for _, dt := range []string{"2024-01-01T00:00:00Z", "2024-06-01T00:00:00Z"} {
		it := stac.Item{
			ID: "i-" + strings.ReplaceAll(dt[:10], "-", ""), Collection: "c1",
			Geometry:   &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{0, 0}},
			BBox:       []float64{0, 0, 0, 0},
			Properties: stac.ItemProperties{"datetime": dt, "eo:cloud_cover": float64(20)},
		}
		rec = doJSON(t, h, "POST", "/collections/c1/items", it)
		assertStatus(t, rec, 201)
	}

	// 4. /collections lists it.
	rec = do(t, h, "GET", "/collections", nil)
	assertStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"id":"c1"`) {
		t.Fatalf("collections list: %s", rec.Body.String())
	}

	// 5. /search returns the items.
	rec = do(t, h, "GET", "/search", nil)
	assertStatus(t, rec, 200)
	var fc struct {
		Type     string      `json:"type"`
		Returned int         `json:"numberReturned"`
		Features []stac.Item `json:"features"`
	}
	mustJSON(t, rec, &fc)
	if fc.Returned != 2 || fc.Type != "FeatureCollection" {
		t.Fatalf("search: %+v body=%s", fc, rec.Body.String())
	}

	// 6. /search with a CQL2-text filter narrows the result.
	rec = do(t, h, "GET", `/search?filter=`+enc(`"eo:cloud_cover" > 50`), nil)
	assertStatus(t, rec, 200)
	mustJSON(t, rec, &fc)
	if fc.Returned != 0 {
		t.Fatalf("filter > 50 should match nothing, got %d", fc.Returned)
	}
}

// helpers

func do(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var b io.Reader
	if body != nil {
		b = strings.NewReader(string(body))
	}
	req := httptest.NewRequest(method, path, b)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, method, path, b)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d (want %d): %s", rec.Code, want, rec.Body.String())
	}
}

func mustJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
}

func enc(s string) string {
	rep := strings.NewReplacer(`"`, "%22", ` `, "%20", `>`, "%3E", `<`, "%3C")
	return rep.Replace(s)
}
