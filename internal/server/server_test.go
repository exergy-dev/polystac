package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/polystac/internal/backends/inmem"
	"github.com/example/polystac/internal/server"
	"github.com/example/polystac/pkg/stac"
)

func newTestServer(t *testing.T) (http.Handler, *inmem.Repo) {
	t.Helper()
	repo := inmem.New()
	if err := repo.UpsertCollection(context.Background(), &stac.Collection{
		ID:          "c1",
		Description: "test collection",
		License:     "x",
	}); err != nil {
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
			Properties: stac.ItemProperties{"datetime": dt, "eo:cloud_cover": float64(10 * (i + 1))},
		}
		if err := repo.UpsertItem(context.Background(), it); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := server.New(server.Options{
		Repo:         repo,
		LandingID:    "polystac",
		LandingTitle: "PolyStac",
		LandingDesc:  "test",
		DefaultLimit: 10,
		MaxLimit:     1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler(), repo
}

func get(t *testing.T, h http.Handler, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, body
}

func TestLanding(t *testing.T) {
	h, _ := newTestServer(t)
	code, body := get(t, h, "/")
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	var cat map[string]any
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatal(err)
	}
	if cat["type"] != "Catalog" {
		t.Errorf("type: %v", cat["type"])
	}
	if cat["id"] != "polystac" {
		t.Errorf("id: %v", cat["id"])
	}
	conf, _ := cat["conformsTo"].([]any)
	if len(conf) == 0 {
		t.Errorf("no conformance classes")
	}
}

func TestConformanceEndpoint(t *testing.T) {
	h, _ := newTestServer(t)
	code, body := get(t, h, "/conformance")
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	if !strings.Contains(string(body), "item-search") {
		t.Errorf("conformance missing item-search: %s", body)
	}
}

func TestListAndGetCollection(t *testing.T) {
	h, _ := newTestServer(t)
	code, body := get(t, h, "/collections")
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"c1"`) {
		t.Errorf("c1 missing: %s", body)
	}
	code, body = get(t, h, "/collections/c1")
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"id":"c1"`) {
		t.Errorf("collection body: %s", body)
	}
}

func TestSearchGETWithFilter(t *testing.T) {
	h, _ := newTestServer(t)
	code, body := get(t, h, `/search?filter=`+urlEncode(`"eo:cloud_cover" > 15`))
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	var ic struct {
		Type     string         `json:"type"`
		Features []stac.Item    `json:"features"`
		Matched  *int64         `json:"numberMatched"`
		Returned int            `json:"numberReturned"`
		Links    []stac.Link    `json:"links"`
	}
	if err := json.Unmarshal(body, &ic); err != nil {
		t.Fatal(err)
	}
	if ic.Type != "FeatureCollection" {
		t.Errorf("type: %q", ic.Type)
	}
	if ic.Returned != 2 {
		t.Errorf("returned %d (want 2): %s", ic.Returned, body)
	}
}

func TestSearchPOSTWithCQL2JSON(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{
		"limit": 10,
		"filter": {"op":">","args":[{"property":"eo:cloud_cover"},15]}
	}`)
	req := httptest.NewRequest("POST", "/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var ic struct {
		Returned int `json:"numberReturned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ic); err != nil {
		t.Fatal(err)
	}
	if ic.Returned != 2 {
		t.Errorf("returned %d, want 2", ic.Returned)
	}
}

func TestNotFound(t *testing.T) {
	h, _ := newTestServer(t)
	code, _ := get(t, h, "/collections/missing")
	if code != 404 {
		t.Errorf("want 404, got %d", code)
	}
	code, _ = get(t, h, "/collections/c1/items/missing")
	if code != 404 {
		t.Errorf("want 404, got %d", code)
	}
}

func TestQueryablesRoute(t *testing.T) {
	h, _ := newTestServer(t)
	code, body := get(t, h, "/collections/c1/queryables")
	if code != 200 {
		t.Fatalf("status %d body=%s", code, body)
	}
	if !strings.Contains(string(body), "datetime") {
		t.Errorf("queryables body missing datetime: %s", body)
	}
}

func TestHealthAndReady(t *testing.T) {
	h, _ := newTestServer(t)
	code, _ := get(t, h, "/_health")
	if code != 200 {
		t.Errorf("health: %d", code)
	}
	code, _ = get(t, h, "/_ready")
	if code != 200 {
		t.Errorf("ready: %d", code)
	}
}

// urlEncode does just enough escaping for these tests; we want a literal
// quote in the filter param.
func urlEncode(s string) string {
	rep := strings.NewReplacer(
		`"`, "%22",
		` `, "%20",
		`>`, "%3E",
		`<`, "%3C",
	)
	return rep.Replace(s)
}
