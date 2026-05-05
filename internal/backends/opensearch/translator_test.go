package opensearch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
)

func dump(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTranslatesBasicSearch(t *testing.T) {
	body, err := translateSearch(repository.SearchRequest{
		IDs:   []string{"a", "b"},
		Limit: 25,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := dump(t, body)
	if !strings.Contains(s, `"size":25`) {
		t.Errorf("size: %s", s)
	}
	if !strings.Contains(s, `"terms":{"id":["a","b"]}`) {
		t.Errorf("ids term: %s", s)
	}
}

func TestTranslatesBBoxToGeoShape(t *testing.T) {
	body, err := translateSearch(repository.SearchRequest{BBox: []float64{-10, -20, 10, 20}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := dump(t, body)
	if !strings.Contains(s, `"type":"envelope"`) {
		t.Errorf("envelope missing: %s", s)
	}
}

func TestTranslatesDatetimeRange(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	body, _ := translateSearch(repository.SearchRequest{
		Datetime: &repository.TemporalInterval{Start: &start, End: &end},
	}, nil)
	s := dump(t, body)
	if !strings.Contains(s, `"properties.datetime"`) {
		t.Errorf("datetime field: %s", s)
	}
	if !strings.Contains(s, `"gte":"2024-01-01T00:00:00Z"`) || !strings.Contains(s, `"lte":"2024-06-01T00:00:00Z"`) {
		t.Errorf("datetime range: %s", s)
	}
}

func TestTranslatesCQL2Comparison(t *testing.T) {
	expr, _ := cql2.Parse([]byte(`"eo:cloud_cover" < 10`))
	body, err := translateSearch(repository.SearchRequest{Filter: expr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := dump(t, body)
	if !strings.Contains(s, `"range":{"properties.eo:cloud_cover":{"lt":10}}`) {
		t.Errorf("range clause: %s", s)
	}
}

func TestTranslatesCQL2LogicalAndIn(t *testing.T) {
	expr, _ := cql2.Parse([]byte(`platform = 'S2A' and "eo:cloud_cover" between 0 and 50`))
	body, err := translateSearch(repository.SearchRequest{Filter: expr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := dump(t, body)
	if !strings.Contains(s, `"bool":{"must":[`) {
		t.Errorf("bool must: %s", s)
	}
	if !strings.Contains(s, `"properties.platform"`) {
		t.Errorf("property mapped to properties.*: %s", s)
	}
	if !strings.Contains(s, `"gte":0`) || !strings.Contains(s, `"lte":50`) {
		t.Errorf("between as range: %s", s)
	}
}

func TestTranslatesCQL2Like(t *testing.T) {
	expr, _ := cql2.Parse([]byte(`platform like 'S2%'`))
	body, _ := translateSearch(repository.SearchRequest{Filter: expr}, nil)
	s := dump(t, body)
	if !strings.Contains(s, `"wildcard":{"properties.platform":"S2*"}`) {
		t.Errorf("wildcard translation: %s", s)
	}
}

func TestTranslatesCQL2IsNull(t *testing.T) {
	expr, _ := cql2.Parse([]byte(`platform is null`))
	body, _ := translateSearch(repository.SearchRequest{Filter: expr}, nil)
	s := dump(t, body)
	if !strings.Contains(s, `"must_not":[{"exists":{"field":"properties.platform"}}]`) {
		t.Errorf("isNull translation: %s", s)
	}
}

func TestSortClausesIncludeIDTiebreak(t *testing.T) {
	out := sortClauses([]repository.SortClause{{Field: "datetime", Direction: repository.SortDesc}})
	s := dump(t, out)
	if !strings.Contains(s, `"properties.datetime":{"order":"desc"}`) {
		t.Errorf("datetime sort: %s", s)
	}
	if !strings.Contains(s, `"id":{"order":"asc"}`) {
		t.Errorf("id tiebreak missing: %s", s)
	}
}

func TestSanitizeIndexName(t *testing.T) {
	cases := map[string]string{
		"sentinel-2-l2a": "sentinel-2-l2a",
		"Foo Bar":        "foo-bar",
		"!!!":            "c", // hashed
	}
	for in, prefix := range cases {
		got := sanitizeIndexName(in)
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("sanitize(%q) = %q, want prefix %q", in, got, prefix)
		}
	}
}
