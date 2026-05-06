//go:build cgo && spatialite

package spatialite

import (
	"github.com/exergy-dev/go-topology-suite/wkt"

	"github.com/example/polystac/pkg/spatial"
	"github.com/example/polystac/pkg/stac"
)

// geomToWKT renders a STAC GeoJSON geometry as WKT for binding into
// GeomFromText(?, 4326). Returns ("", false) when the input can't be
// parsed; callers fall back to bbox-only filtering.
func geomToWKT(g *stac.Geometry) (string, bool) {
	parsed, ok := spatial.FromSTAC(g)
	if !ok {
		return "", false
	}
	out, err := wkt.Marshal(parsed)
	if err != nil {
		return "", false
	}
	return out, true
}

// bboxToWKT renders [west, south, east, north] as a closed POLYGON.
func bboxToWKT(bb []float64) (string, bool) {
	if len(bb) < 4 {
		return "", false
	}
	out, err := wkt.Marshal(spatial.BBoxPolygon(bb[0], bb[1], bb[2], bb[3]))
	if err != nil {
		return "", false
	}
	return out, true
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
