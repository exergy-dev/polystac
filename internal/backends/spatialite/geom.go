//go:build cgo && spatialite

package spatialite

import (
	"encoding/json"

	"github.com/exergy-dev/go-topology-suite/geojson"
	"github.com/exergy-dev/go-topology-suite/geom"
	"github.com/exergy-dev/go-topology-suite/wkt"

	"github.com/example/polystac/pkg/stac"
)

// geomToWKT renders a STAC GeoJSON geometry as WKT for binding into
// GeomFromText(?, 4326). Returns ("", false) when the input can't be
// parsed; callers fall back to bbox-only filtering.
func geomToWKT(g *stac.Geometry) (string, bool) {
	if g == nil {
		return "", false
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return "", false
	}
	parsed, err := geojson.Unmarshal(raw)
	if err != nil {
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
	return bboxPolygonWKT(bb[0], bb[1], bb[2], bb[3]), true
}

func bboxPolygonWKT(w, s, e, n float64) string {
	poly := geom.NewPolygon(nil, []geom.XY{
		{X: w, Y: s}, {X: e, Y: s}, {X: e, Y: n}, {X: w, Y: n}, {X: w, Y: s},
	})
	out, _ := wkt.Marshal(poly)
	return out
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
