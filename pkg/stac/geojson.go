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

	gtsgeojson "github.com/exergy-dev/go-topology-suite/geojson"
	gtsgeom "github.com/exergy-dev/go-topology-suite/geom"
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

// MarshalJSON delegates to go-topology-suite's GeoJSON encoder, which
// matches stac-fastapi's Python json.dumps output (notably zero-padded
// scientific-notation exponents that Go's encoding/json doesn't emit).
// We round-trip via the canonical struct shape first so the typed
// []float64 / [][]float64 / ... and the decoded-from-JSON []any paths
// produce the same wire bytes.
func (g Geometry) MarshalJSON() ([]byte, error) {
	canonical, err := g.canonicalBytes()
	if err != nil {
		return nil, err
	}
	parsed, err := gtsgeojson.Unmarshal(canonical)
	if err != nil {
		// Degenerate or empty inputs — fall through to the canonical
		// form rather than dropping the response. Preserves the
		// pre-gts behavior for those edge cases.
		return canonical, nil
	}
	return gtsgeojson.Marshal(parsed)
}

// BBox computes the axis-aligned bounding box of the geometry's
// coordinates. Returns ([west, south, east, north], true) when the
// geometry has at least one numeric coordinate.
func (g *Geometry) BBox() ([4]float64, bool) {
	if g == nil {
		return [4]float64{}, false
	}
	parsed, ok := g.toGTS()
	if !ok {
		return [4]float64{}, false
	}
	env := parsed.Envelope()
	if env.IsEmpty() {
		return [4]float64{}, false
	}
	return [4]float64{env.MinX, env.MinY, env.MaxX, env.MaxY}, true
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

// canonicalBytes emits the type+coordinates (or type+geometries)
// struct shape that both Go's stdlib and gts can parse. Used as the
// hand-off format for round-tripping into gts.
func (g Geometry) canonicalBytes() ([]byte, error) {
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

// toGTS is the internal converter to a go-topology-suite geometry.
// `pkg/spatial.FromSTAC` does the same thing externally; duplicated
// here to keep `pkg/stac` from depending on `pkg/spatial` (which would
// be a cycle, since spatial depends on stac).
func (g Geometry) toGTS() (gtsgeom.Geometry, bool) {
	canonical, err := g.canonicalBytes()
	if err != nil {
		return nil, false
	}
	parsed, err := gtsgeojson.Unmarshal(canonical)
	if err != nil {
		return nil, false
	}
	return parsed, true
}
