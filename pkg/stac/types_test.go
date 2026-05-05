package stac

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestItemMarshalCanonicalKeyOrder(t *testing.T) {
	dt := "2024-05-01T00:00:00Z"
	item := Item{
		ID:         "S2A_2024",
		Collection: "sentinel-2-l2a",
		Geometry: &Geometry{
			Type:        GeometryPoint,
			Coordinates: []float64{12.5, 41.9},
		},
		BBox:       []float64{12.5, 41.9, 12.5, 41.9},
		Properties: ItemProperties{"datetime": dt, "eo:cloud_cover": 12.3, "platform": "S2A"},
		Assets: map[string]Asset{
			"thumbnail": {Href: "https://x/y.png", Type: "image/png", Roles: []string{"thumbnail"}},
		},
	}
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)

	wantPrefix := `{"type":"Feature","stac_version":"1.0.0","id":"S2A_2024","geometry":{"type":"Point","coordinates":[12.5,41.9]},"bbox":[12.5,41.9,12.5,41.9],"properties":{"datetime":"2024-05-01T00:00:00Z"`
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("Item key order mismatch.\n got: %s\nwant prefix: %s", got, wantPrefix)
	}

	if !strings.Contains(got, `"properties":{"datetime":"2024-05-01T00:00:00Z","eo:cloud_cover":12.3,"platform":"S2A"}`) {
		t.Errorf("properties not in datetime-first then alphabetical order: %s", got)
	}

	if !strings.Contains(got, `"collection":"sentinel-2-l2a"`) {
		t.Errorf("collection field missing: %s", got)
	}
}

func TestItemRoundTrip(t *testing.T) {
	src := []byte(`{"type":"Feature","stac_version":"1.0.0","id":"x","geometry":{"type":"Point","coordinates":[0,0]},"properties":{"datetime":"2024-01-01T00:00:00Z"},"links":[],"assets":{}}`)
	var it Item
	if err := json.Unmarshal(src, &it); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if it.ID != "x" || it.Properties["datetime"] != "2024-01-01T00:00:00Z" {
		t.Fatalf("decode lost fields: %+v", it)
	}
	out, err := json.Marshal(it)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if string(out) != string(src) {
		t.Errorf("round-trip not byte-identical:\n got: %s\nwant: %s", out, src)
	}
}

func TestCollectionMarshalCanonicalKeyOrder(t *testing.T) {
	c := Collection{
		ID:          "sentinel-2-l2a",
		Title:       "Sentinel-2 L2A",
		Description: "...",
		License:     "proprietary",
		Extent: Extent{
			Spatial:  SpatialExtent{BBox: [][]float64{{-180, -90, 180, 90}}},
			Temporal: TemporalExtent{Interval: [][]*string{{nil, nil}}},
		},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	wantPrefix := `{"type":"Collection","stac_version":"1.0.0","id":"sentinel-2-l2a","title":"Sentinel-2 L2A","description":"...","license":"proprietary","extent":`
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("Collection key order mismatch.\n got: %s\nwant prefix: %s", got, wantPrefix)
	}
}

func TestGeometryCollectionShape(t *testing.T) {
	g := Geometry{
		Type: GeometryGeometryCollection,
		Geometries: []Geometry{
			{Type: GeometryPoint, Coordinates: []float64{0, 0}},
		},
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[0,0]}]}`
	if string(b) != want {
		t.Errorf("got %s want %s", b, want)
	}
}
