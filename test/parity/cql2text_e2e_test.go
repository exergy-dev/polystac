//go:build integration

package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/opensearch"
	_ "github.com/example/polystac/internal/backends/pgstac"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/internal/server"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
	"github.com/example/polystac/test/parity"
)

// TestCQL2TextHTTPIntoPgstac proves the full client → server → pgstac
// pipeline for CQL2-text: the client sends `filter=eo:cloud_cover < 30`
// on the wire (the text form), the server parses to AST, the pgstac
// translator re-encodes as CQL2-JSON, and pgstac returns the matching
// items. Same body shape via POST is also exercised.
//
// This is the regression test for the dialect bug fixed in
// internal/backends/pgstac/translator.go (filter-lang must be
// "cql2-json" on the wire and `between` must be rewritten to
// `>= AND <=` because go-cql2's emitted JSON form for between is not
// what pgstac's cql2_query accepts).
func TestCQL2TextHTTPIntoPgstac(t *testing.T) {
	dsn := pgstacDSN(t)
	cfg := config.Defaults()
	cfg.Backend = "pgstac"
	cfg.BackendConfig = map[string]string{"dsn": dsn}
	repo, err := backends.Open(context.Background(), "pgstac", cfg)
	if err != nil {
		t.Fatalf("open pgstac: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	if err := parity.Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := buildHandler(t, repo)
	runCQL2TextSuite(t, srv, repo.Capabilities().Backend)
}

// TestCQL2TextHTTPIntoOpenSearch proves the same pipeline against
// OpenSearch. The OS backend does not re-encode the AST as JSON; it
// walks the AST directly to ES DSL. The same client-side wire form
// (CQL2-text) must still produce identical results.
func TestCQL2TextHTTPIntoOpenSearch(t *testing.T) {
	hosts, user, pass := openSearchHosts(t)
	cfg := config.Defaults()
	cfg.Backend = "opensearch"
	cfg.BackendConfig = map[string]string{
		"hosts":             hosts,
		"username":          user,
		"password":          pass,
		"verify_certs":      "false",
		"index_prefix":      "test_e2e_items_",
		"collections_index": "test_e2e_collections",
	}
	repo, err := backends.Open(context.Background(), "opensearch", cfg)
	if err != nil {
		t.Fatalf("open opensearch: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	if err := parity.Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// OpenSearch eventually-consistent index refresh is forced via
	// `?refresh=wait_for` in the client; no extra sleep needed.

	srv := buildHandler(t, repo)
	runCQL2TextSuite(t, srv, repo.Capabilities().Backend)
}

// runCQL2TextSuite issues a small set of GET / POST searches whose
// only filter is a CQL2-text expression on the wire. Asserts the
// right items come back.
func runCQL2TextSuite(t *testing.T, srv http.Handler, backendName string) {
	t.Helper()

	cases := []struct {
		name string
		text string
		want []string
	}{
		{"comparison", `"eo:cloud_cover" < 30`, []string{"a-1", "a-2", "b-1"}},
		{"and-combination", `platform = 'S2A' and "eo:cloud_cover" < 30`, []string{"a-1", "b-1"}},
		{"between", `"eo:cloud_cover" between 30 and 70`, []string{"a-3", "b-2"}},
		{"in-list", `platform in ('S2B', 'L8')`, []string{"a-2", "b-2"}},
		{"is-null", `missing_prop is null`, []string{"a-1", "a-2", "a-3", "b-1", "b-2"}},
	}

	for _, tc := range cases {
		t.Run("GET/"+tc.name, func(t *testing.T) {
			q := url.Values{
				"filter":      []string{tc.text},
				"filter-lang": []string{"cql2-text"},
				"limit":       []string{"100"},
			}
			ids := getSearchIDs(t, srv, "/search?"+q.Encode())
			assertIDsEqual(t, ids, tc.want, backendName, tc.name)
		})

		t.Run("POST/"+tc.name, func(t *testing.T) {
			body := map[string]any{
				"filter":      tc.text,
				"filter-lang": "cql2-text",
				"limit":       100,
			}
			ids := postSearchIDs(t, srv, body)
			assertIDsEqual(t, ids, tc.want, backendName, tc.name)
		})
	}
}

// buildHandler wraps the configured Repository in the standard server
// stack — same code path as `polystac serve`.
func buildHandler(t *testing.T, repo repository.Repository) http.Handler {
	t.Helper()
	srv, err := server.New(server.Options{
		Repo:         repo,
		LandingID:    "test",
		LandingTitle: "test",
		LandingDesc:  "test",
		DefaultLimit: 10,
		MaxLimit:     1000,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv.Handler()
}

func getSearchIDs(t *testing.T, h http.Handler, path string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET %s status %d body=%s", path, rec.Code, rec.Body.String())
	}
	return decodeFeatureIDs(t, rec.Body)
}

func postSearchIDs(t *testing.T, h http.Handler, body map[string]any) []string {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/search", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /search status %d body=%s", rec.Code, rec.Body.String())
	}
	return decodeFeatureIDs(t, rec.Body)
}

func decodeFeatureIDs(t *testing.T, r io.Reader) []string {
	t.Helper()
	var fc struct {
		Features []stac.Item `json:"features"`
	}
	if err := json.NewDecoder(r).Decode(&fc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make([]string, 0, len(fc.Features))
	for _, f := range fc.Features {
		out = append(out, f.ID)
	}
	return out
}

func assertIDsEqual(t *testing.T, got, want []string, backend, name string) {
	t.Helper()
	gs := append([]string(nil), got...)
	ws := append([]string(nil), want...)
	sortStrings(gs)
	sortStrings(ws)
	if strings.Join(gs, ",") != strings.Join(ws, ",") {
		t.Errorf("[%s/%s] ids:\n got:  %v\n want: %v", backend, name, gs, ws)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
