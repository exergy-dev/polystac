//go:build cgo && spatialite

package spatialite

import (
	"fmt"
	"strings"

	"github.com/example/polystac/pkg/stac"
)

// geomToWKT renders a STAC GeoJSON geometry as WKT for binding into
// GeomFromText(?, 4326). Returns ("", false) when the shape isn't
// directly representable (GeometryCollection, unknown types, malformed
// coords); callers fall back to bbox-only filtering.
func geomToWKT(g *stac.Geometry) (string, bool) {
	if g == nil {
		return "", false
	}
	switch g.Type {
	case stac.GeometryPoint:
		coord, ok := asCoord(g.Coordinates)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("POINT(%s)", fmtCoord(coord)), true
	case stac.GeometryMultiPoint:
		coords, ok := asCoordSlice(g.Coordinates)
		if !ok {
			return "", false
		}
		return "MULTIPOINT(" + joinCoords(coords) + ")", true
	case stac.GeometryLineString:
		coords, ok := asCoordSlice(g.Coordinates)
		if !ok {
			return "", false
		}
		return "LINESTRING(" + joinCoords(coords) + ")", true
	case stac.GeometryPolygon:
		rings, ok := asRings(g.Coordinates)
		if !ok {
			return "", false
		}
		return "POLYGON(" + joinRings(rings) + ")", true
	case stac.GeometryMultiLineString:
		ls, ok := asRings(g.Coordinates)
		if !ok {
			return "", false
		}
		parts := make([]string, len(ls))
		for i, r := range ls {
			parts[i] = "(" + joinCoords(r) + ")"
		}
		return "MULTILINESTRING(" + strings.Join(parts, ",") + ")", true
	case stac.GeometryMultiPolygon:
		polys, ok := asMultiPolygon(g.Coordinates)
		if !ok {
			return "", false
		}
		parts := make([]string, len(polys))
		for i, rings := range polys {
			parts[i] = "(" + joinRings(rings) + ")"
		}
		return "MULTIPOLYGON(" + strings.Join(parts, ",") + ")", true
	}
	return "", false
}

// bboxToWKT renders [west, south, east, north] as a closed POLYGON WKT.
func bboxToWKT(bb []float64) (string, bool) {
	if len(bb) < 4 {
		return "", false
	}
	return bboxPolygonWKT(bb[0], bb[1], bb[2], bb[3]), true
}

func bboxPolygonWKT(w, s, e, n float64) string {
	return fmt.Sprintf(
		"POLYGON((%s %s,%s %s,%s %s,%s %s,%s %s))",
		fmtFloat(w), fmtFloat(s),
		fmtFloat(e), fmtFloat(s),
		fmtFloat(e), fmtFloat(n),
		fmtFloat(w), fmtFloat(n),
		fmtFloat(w), fmtFloat(s),
	)
}

func itemBBox(it *stac.Item) ([4]float64, bool) {
	if len(it.BBox) >= 4 {
		return [4]float64{it.BBox[0], it.BBox[1], it.BBox[2], it.BBox[3]}, true
	}
	if it.Geometry != nil {
		if bb, ok := it.Geometry.BBox(); ok {
			return bb, true
		}
	}
	return [4]float64{}, false
}

// GeoJSON allows coordinates as either []float64 or []any depending on
// how the JSON decoded; the asXxx helpers normalize both.

func asCoord(v any) ([2]float64, bool) {
	switch c := v.(type) {
	case []float64:
		if len(c) >= 2 {
			return [2]float64{c[0], c[1]}, true
		}
	case []any:
		if len(c) >= 2 {
			x, xok := toFloat(c[0])
			y, yok := toFloat(c[1])
			if xok && yok {
				return [2]float64{x, y}, true
			}
		}
	}
	return [2]float64{}, false
}

func asCoordSlice(v any) ([][2]float64, bool) {
	switch arr := v.(type) {
	case [][]float64:
		out := make([][2]float64, 0, len(arr))
		for _, c := range arr {
			if len(c) < 2 {
				return nil, false
			}
			out = append(out, [2]float64{c[0], c[1]})
		}
		return out, true
	case []any:
		out := make([][2]float64, 0, len(arr))
		for _, e := range arr {
			c, ok := asCoord(e)
			if !ok {
				return nil, false
			}
			out = append(out, c)
		}
		return out, true
	}
	return nil, false
}

func asRings(v any) ([][][2]float64, bool) {
	switch arr := v.(type) {
	case [][][]float64:
		out := make([][][2]float64, 0, len(arr))
		for _, ring := range arr {
			r := make([][2]float64, 0, len(ring))
			for _, c := range ring {
				if len(c) < 2 {
					return nil, false
				}
				r = append(r, [2]float64{c[0], c[1]})
			}
			out = append(out, r)
		}
		return out, true
	case []any:
		out := make([][][2]float64, 0, len(arr))
		for _, ring := range arr {
			r, ok := asCoordSlice(ring)
			if !ok {
				return nil, false
			}
			out = append(out, r)
		}
		return out, true
	}
	return nil, false
}

func asMultiPolygon(v any) ([][][][2]float64, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([][][][2]float64, 0, len(arr))
	for _, p := range arr {
		rings, ok := asRings(p)
		if !ok {
			return nil, false
		}
		out = append(out, rings)
	}
	return out, true
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func joinCoords(cs [][2]float64) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmtCoord(c)
	}
	return strings.Join(parts, ",")
}

func joinRings(rings [][][2]float64) string {
	parts := make([]string, len(rings))
	for i, r := range rings {
		parts[i] = "(" + joinCoords(r) + ")"
	}
	return strings.Join(parts, ",")
}

func fmtCoord(c [2]float64) string { return fmtFloat(c[0]) + " " + fmtFloat(c[1]) }

func fmtFloat(v float64) string { return fmt.Sprintf("%g", v) }
