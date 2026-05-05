package pgstac

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
)

func TestTranslateBasicSearch(t *testing.T) {
	req := repository.SearchRequest{
		Collections: []string{"a", "b"},
		IDs:         []string{"x"},
		BBox:        []float64{-10, -20, 10, 20},
		Limit:       25,
		Token:       "tok123",
	}
	b, err := translateSearch(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if cs := got["collections"].([]any); cs[0] != "a" || cs[1] != "b" {
		t.Errorf("collections: %v", cs)
	}
	if got["limit"].(float64) != 25 {
		t.Errorf("limit: %v", got["limit"])
	}
	if got["token"] != "tok123" {
		t.Errorf("token: %v", got["token"])
	}
}

func TestTranslateDatetimeInterval(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	req := repository.SearchRequest{
		Datetime: &repository.TemporalInterval{Start: &start},
	}
	b, err := translateSearch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"datetime":"2024-01-01T00:00:00Z/.."`) {
		t.Errorf("open-ended datetime: %s", b)
	}
}

func TestTranslateFilterEncodesToJSON(t *testing.T) {
	expr, err := cql2.Parse([]byte(`"eo:cloud_cover" < 10`))
	if err != nil {
		t.Fatal(err)
	}
	req := repository.SearchRequest{Filter: expr, FilterLang: repository.FilterLangText}
	b, err := translateSearch(req)
	if err != nil {
		t.Fatal(err)
	}
	// Even when the user's input was CQL2-text, the translator
	// re-encodes the AST as CQL2-JSON before sending — pgstac does
	// not accept the text form on this field.
	if !strings.Contains(string(b), `"filter-lang":"cql2-json"`) {
		t.Errorf("filter-lang: %s", b)
	}
	// json.Marshal escapes "<" to its < form by default; both
	// renderings are equivalent to pgstac. Normalize the escape so the
	// assertion works regardless of which path the encoder took.
	escapedLT := "\\u003c"
	normalized := strings.ReplaceAll(string(b), escapedLT, "<")
	if !strings.Contains(normalized, `"op":"<"`) {
		t.Errorf("filter op not encoded: %s", b)
	}
	if !strings.Contains(normalized, `"property":"eo:cloud_cover"`) {
		t.Errorf("filter property missing: %s", b)
	}
}

func TestTranslateSortBy(t *testing.T) {
	req := repository.SearchRequest{
		SortBy: []repository.SortClause{
			{Field: "datetime", Direction: repository.SortDesc},
			{Field: "id", Direction: repository.SortAsc},
		},
	}
	b, err := translateSearch(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `"sortby":[{"direction":"desc","field":"datetime"},{"direction":"asc","field":"id"}]`
	if !strings.Contains(string(b), want) {
		t.Errorf("sortby:\n got: %s\nwant: %s", b, want)
	}
}
