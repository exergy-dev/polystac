// Package stac defines STAC object types (Item, Collection, Catalog, Asset,
// Link) and the GeoJSON geometry types they use.
//
// JSON marshalling is deterministic. See marshal.go for the canonical key
// ordering rules — this is load-bearing for byte-equivalence with
// stac-fastapi reference implementations (SDD §9.3, risk #2).
package stac

import (
	"encoding/json"
	"fmt"
	"math"
)

// GeometryType enumerates the GeoJSON geometry types STAC items use.
type GeometryType string

const (
	GeometryPoint              GeometryType = "Point"
	GeometryMultiPoint         GeometryType = "MultiPoint"
	GeometryLineString         GeometryType = "LineString"
	GeometryMultiLineString    GeometryType = "MultiLineString"
	GeometryPolygon            GeometryType = "Polygon"
	GeometryMultiPolygon       GeometryType = "MultiPolygon"
	GeometryGeometryCollection GeometryType = "GeometryCollection"
)

// Geometry is a GeoJSON geometry. Coordinates are stored as the raw nested
// numeric arrays GeoJSON specifies; backends are responsible for any
// translation to native spatial representations.
//
// For GeometryCollection, Geometries holds the member geometries and
// Coordinates is nil.
type Geometry struct {
	Type        GeometryType `json:"type"`
	Coordinates any          `json:"coordinates,omitempty"`
	Geometries  []Geometry   `json:"geometries,omitempty"`
}

// MarshalJSON enforces the GeoJSON ordering: type before coordinates/
// geometries. Empty Coordinates is omitted only for GeometryCollection;
// other geometry types must always carry coordinates.
func (g Geometry) MarshalJSON() ([]byte, error) {
	if g.Type == GeometryGeometryCollection {
		return json.Marshal(struct {
			Type       GeometryType `json:"type"`
			Geometries []Geometry   `json:"geometries"`
		}{g.Type, g.Geometries})
	}
	return json.Marshal(struct {
		Type        GeometryType `json:"type"`
		Coordinates any          `json:"coordinates"`
	}{g.Type, g.Coordinates})
}

// BBox computes the axis-aligned bounding box of the geometry's
// coordinates. Returns ([west, south, east, north], true) when the
// geometry has at least one numeric coordinate. Walks GeometryCollection
// children recursively.
func (g *Geometry) BBox() ([4]float64, bool) {
	if g == nil {
		return [4]float64{}, false
	}
	mn := [2]float64{math.Inf(1), math.Inf(1)}
	mx := [2]float64{math.Inf(-1), math.Inf(-1)}
	saw := false
	visit := func(x, y float64) {
		if x < mn[0] {
			mn[0] = x
		}
		if y < mn[1] {
			mn[1] = y
		}
		if x > mx[0] {
			mx[0] = x
		}
		if y > mx[1] {
			mx[1] = y
		}
		saw = true
	}
	var walk func(any)
	walk = func(v any) {
		switch coords := v.(type) {
		case []float64:
			if len(coords) >= 2 {
				visit(coords[0], coords[1])
			}
		case []any:
			if len(coords) >= 2 {
				if x, ok := coords[0].(float64); ok {
					if y, ok := coords[1].(float64); ok {
						visit(x, y)
						return
					}
				}
			}
			for _, e := range coords {
				walk(e)
			}
		}
	}
	walk(g.Coordinates)
	for _, sg := range g.Geometries {
		if bb, ok := sg.BBox(); ok {
			visit(bb[0], bb[1])
			visit(bb[2], bb[3])
		}
	}
	if !saw {
		return [4]float64{}, false
	}
	return [4]float64{mn[0], mn[1], mx[0], mx[1]}, true
}

// UnmarshalJSON validates the discriminator and dispatches.
func (g *Geometry) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type        GeometryType    `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
		Geometries  []Geometry      `json:"geometries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Type == "" {
		return fmt.Errorf("stac: geometry missing \"type\"")
	}
	g.Type = raw.Type
	g.Geometries = raw.Geometries
	if len(raw.Coordinates) > 0 {
		var coords any
		if err := json.Unmarshal(raw.Coordinates, &coords); err != nil {
			return fmt.Errorf("stac: geometry coordinates: %w", err)
		}
		g.Coordinates = coords
	}
	return nil
}
