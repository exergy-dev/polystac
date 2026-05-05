package eval

import (
	"testing"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/stac"
)

func mustParse(t *testing.T, s string) cql2.Expression {
	t.Helper()
	e, err := cql2.Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return e
}

func sampleItem() *stac.Item {
	return &stac.Item{
		ID:         "S2A_001",
		Collection: "sentinel-2-l2a",
		Geometry:   &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{12.5, 41.9}},
		BBox:       []float64{12.0, 41.0, 13.0, 42.0},
		Properties: stac.ItemProperties{
			"datetime":       "2024-05-01T00:00:00Z",
			"eo:cloud_cover": 12.0,
			"platform":       "S2A",
		},
	}
}

func TestComparisons(t *testing.T) {
	it := sampleItem()
	cases := []struct {
		expr string
		want bool
	}{
		{`"eo:cloud_cover" < 50`, true},
		{`"eo:cloud_cover" > 50`, false},
		{`"eo:cloud_cover" = 12`, true},
		{`"eo:cloud_cover" <> 12`, false},
		{`platform = 'S2A'`, true},
		{`platform = 'S2B'`, false},
		{`id = 'S2A_001'`, true},
		{`collection = 'sentinel-2-l2a'`, true},
	}
	for _, tc := range cases {
		got, err := Match(mustParse(t, tc.expr), it)
		if err != nil {
			t.Errorf("%q: err %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v want %v", tc.expr, got, tc.want)
		}
	}
}

func TestLogical(t *testing.T) {
	it := sampleItem()
	cases := []struct {
		expr string
		want bool
	}{
		{`"eo:cloud_cover" < 50 and platform = 'S2A'`, true},
		{`"eo:cloud_cover" > 50 or platform = 'S2A'`, true},
		{`"eo:cloud_cover" > 50 and platform = 'S2A'`, false},
		{`not ("eo:cloud_cover" > 50)`, true},
	}
	for _, tc := range cases {
		got, err := Match(mustParse(t, tc.expr), it)
		if err != nil {
			t.Errorf("%q: err %v", tc.expr, err)
		}
		if got != tc.want {
			t.Errorf("%q: got %v want %v", tc.expr, got, tc.want)
		}
	}
}

func TestBetweenInLikeIsNull(t *testing.T) {
	it := sampleItem()
	cases := []struct {
		expr string
		want bool
	}{
		{`"eo:cloud_cover" between 0 and 100`, true},
		{`"eo:cloud_cover" between 50 and 100`, false},
		{`platform in ('S2A', 'S2B')`, true},
		{`platform in ('S2C', 'S2D')`, false},
		{`platform like 'S2%'`, true},
		{`platform like 'X%'`, false},
		{`missing_prop is null`, true},
		{`platform is null`, false},
	}
	for _, tc := range cases {
		got, err := Match(mustParse(t, tc.expr), it)
		if err != nil {
			t.Errorf("%q: err %v", tc.expr, err)
		}
		if got != tc.want {
			t.Errorf("%q: got %v want %v", tc.expr, got, tc.want)
		}
	}
}

func TestNullSemantics(t *testing.T) {
	it := sampleItem()
	got, err := Match(mustParse(t, `nonexistent < 5`), it)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Error("comparison with NULL should yield false")
	}
}
