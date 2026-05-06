// Package spatial converts STAC and CQL2 geometry values into the
// go-topology-suite Geometry type, plus a few commonly-used
// constructors. The functions are pure and allocate fresh values; they
// don't retain references to inputs.
package spatial

import (
	"encoding/json"

	gtsgeojson "github.com/exergy-dev/go-topology-suite/geojson"
	gtsgeom "github.com/exergy-dev/go-topology-suite/geom"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/stac"
)

// FromSTAC parses a STAC GeoJSON geometry. Returns (nil, false) on nil
// input or any parse failure (callers fall back to a coarser check).
func FromSTAC(g *stac.Geometry) (gtsgeom.Geometry, bool) {
	if g == nil {
		return nil, false
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, false
	}
	parsed, err := gtsgeojson.Unmarshal(raw)
	if err != nil {
		return nil, false
	}
	return parsed, true
}

// FromCQL2 converts a CQL2 geometry literal into a gts geometry.
// Returns (nil, false) for unsupported / empty literals.
func FromCQL2(g cql2.Geometry) (gtsgeom.Geometry, bool) {
	switch x := g.(type) {
	case *cql2.Point:
		if x.Empty {
			return nil, false
		}
		return gtsgeom.NewPoint(nil, gtsgeom.XY{X: x.Coord.X, Y: x.Coord.Y}), true
	case *cql2.LineString:
		return gtsgeom.NewLineString(nil, cql2CoordsToXY(x.Coords)), true
	case *cql2.Polygon:
		rings := make([][]gtsgeom.XY, len(x.Rings))
		for i, r := range x.Rings {
			rings[i] = cql2CoordsToXY(r)
		}
		return gtsgeom.NewPolygon(nil, rings...), true
	case *cql2.MultiPoint:
		pts := make([]gtsgeom.XY, 0, len(x.Points))
		for _, p := range x.Points {
			if p.Empty {
				continue
			}
			pts = append(pts, gtsgeom.XY{X: p.Coord.X, Y: p.Coord.Y})
		}
		return gtsgeom.NewMultiPoint(nil, pts), true
	case *cql2.MultiLineString:
		parts := make([]*gtsgeom.LineString, 0, len(x.Lines))
		for _, ls := range x.Lines {
			parts = append(parts, gtsgeom.NewLineString(nil, cql2CoordsToXY(ls.Coords)))
		}
		return gtsgeom.NewMultiLineString(nil, parts...), true
	case *cql2.MultiPolygon:
		parts := make([]*gtsgeom.Polygon, 0, len(x.Polys))
		for _, p := range x.Polys {
			rings := make([][]gtsgeom.XY, len(p.Rings))
			for i, r := range p.Rings {
				rings[i] = cql2CoordsToXY(r)
			}
			parts = append(parts, gtsgeom.NewPolygon(nil, rings...))
		}
		return gtsgeom.NewMultiPolygon(nil, parts...), true
	case *cql2.GeometryCollection:
		members := make([]gtsgeom.Geometry, 0, len(x.Geoms))
		for _, sub := range x.Geoms {
			if m, ok := FromCQL2(sub); ok {
				members = append(members, m)
			}
		}
		return gtsgeom.NewGeometryCollection(nil, members...), true
	}
	return nil, false
}

// BBoxPolygon builds a closed polygon for [w,s] → [e,n]. Vertices wind
// CCW which matches the GeoJSON exterior-ring convention.
func BBoxPolygon(w, s, e, n float64) gtsgeom.Geometry {
	return gtsgeom.NewPolygon(nil, []gtsgeom.XY{
		{X: w, Y: s}, {X: e, Y: s}, {X: e, Y: n}, {X: w, Y: n}, {X: w, Y: s},
	})
}

func cql2CoordsToXY(in []cql2.Coord) []gtsgeom.XY {
	out := make([]gtsgeom.XY, len(in))
	for i, c := range in {
		out[i] = gtsgeom.XY{X: c.X, Y: c.Y}
	}
	return out
}
