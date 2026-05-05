//go:build cgo && spatialite

package spatialite

import (
	"strings"
	"testing"

	"github.com/example/polystac/pkg/cql2"
)

func mustParseFilter(t *testing.T, src string) cql2.Expression {
	t.Helper()
	e, err := cql2.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return e
}

func TestTranslatorOperators(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantFrag  string // substring that must appear
		wantArgsN int
	}{
		{
			name:      "eq-property-num",
			src:       `"eo:cloud_cover" = 30`,
			wantFrag:  `json_extract(items.properties, '$."eo:cloud_cover"') = ?`,
			wantArgsN: 1,
		},
		{
			name:      "eq-id",
			src:       `id = 'a-1'`,
			wantFrag:  `items.id = ?`,
			wantArgsN: 1,
		},
		{
			name:      "lt",
			src:       `"eo:cloud_cover" < 30`,
			wantFrag:  `json_extract(items.properties, '$."eo:cloud_cover"') < ?`,
			wantArgsN: 1,
		},
		{
			name:      "between",
			src:       `"eo:cloud_cover" between 30 and 70`,
			wantFrag:  `json_extract(items.properties, '$."eo:cloud_cover"') BETWEEN ? AND ?`,
			wantArgsN: 2,
		},
		{
			name:      "in-list",
			src:       `platform in ('S2A', 'S2B')`,
			wantFrag:  `IN (?,?)`,
			wantArgsN: 2,
		},
		{
			name:      "is-null",
			src:       `missing_prop is null`,
			wantFrag:  `IS NULL`,
			wantArgsN: 0,
		},
		{
			name:      "and",
			src:       `platform = 'S2A' and "eo:cloud_cover" < 30`,
			wantFrag:  ` AND `,
			wantArgsN: 2,
		},
		{
			name:      "or",
			src:       `platform = 'S2A' or platform = 'L8'`,
			wantFrag:  ` OR `,
			wantArgsN: 2,
		},
		{
			name:      "not",
			src:       `not (platform = 'S2A')`,
			wantFrag:  `(NOT `,
			wantArgsN: 1,
		},
		{
			name:      "like",
			src:       `platform like 'S2%'`,
			wantFrag:  `LIKE ?`,
			wantArgsN: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expr := mustParseFilter(t, tc.src)
			frag, args, err := translateFilter(expr)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if !strings.Contains(frag, tc.wantFrag) {
				t.Errorf("fragment missing %q:\n  got: %s", tc.wantFrag, frag)
			}
			if len(args) != tc.wantArgsN {
				t.Errorf("args: got %d want %d (%v)", len(args), tc.wantArgsN, args)
			}
		})
	}
}

func TestTranslatorSpatial(t *testing.T) {
	expr := mustParseFilter(t, `S_INTERSECTS(geometry, BBOX(-1, -1, 1, 1))`)
	frag, args, err := translateFilter(expr)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.Contains(frag, "ST_Intersects(items.geom, GeomFromText(?, 4326))") {
		t.Errorf("expected ST_Intersects fragment, got: %s", frag)
	}
	if len(args) != 1 {
		t.Errorf("args: got %d want 1", len(args))
	}
	wkt, _ := args[0].(string)
	if !strings.HasPrefix(wkt, "POLYGON((-1 -1") {
		t.Errorf("expected POLYGON WKT, got %q", wkt)
	}
}

func TestTranslatorTemporal(t *testing.T) {
	expr := mustParseFilter(t, `T_AFTER(datetime, TIMESTAMP('2024-01-01T00:00:00Z'))`)
	frag, args, err := translateFilter(expr)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.Contains(frag, "items.datetime > ?") {
		t.Errorf("expected datetime > ? fragment, got: %s", frag)
	}
	if len(args) != 1 || args[0].(string) != "2024-01-01T00:00:00Z" {
		t.Errorf("args: got %v want [2024-01-01T00:00:00Z]", args)
	}
}

func TestTranslatorUnsupportedReturnsTranslationError(t *testing.T) {
	// Function calls are not translated by this backend.
	expr := mustParseFilter(t, `casei(platform) = 's2a'`)
	_, _, err := translateFilter(expr)
	if err == nil {
		t.Fatal("expected TranslationError for casei()")
	}
	if !cql2.IsTranslationError(err) {
		t.Errorf("expected *cql2.TranslationError, got %T: %v", err, err)
	}
}

func TestMapColumn(t *testing.T) {
	cases := map[string]string{
		"id":              "items.id",
		"collection":      "items.collection_id",
		"datetime":        "items.datetime",
		"geometry":        "items.geom",
		"eo:cloud_cover":  `json_extract(items.properties, '$."eo:cloud_cover"')`,
		"properties.foo":  `json_extract(items.properties, '$."foo"')`,
	}
	for in, want := range cases {
		if got := mapColumn(in); got != want {
			t.Errorf("mapColumn(%q): got %q want %q", in, got, want)
		}
	}
}
