//go:build cgo && spatialite

package spatialite

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/example/polystac/pkg/cql2"
)

const backendID = "spatialite"

// translateFilter walks a CQL2 AST and produces a parameterized SQL
// fragment plus its argument list. Unsupported nodes return a
// *cql2.TranslationError so the service layer can map to HTTP 400.
func translateFilter(e cql2.Expression) (string, []any, error) {
	switch n := e.(type) {
	case *cql2.Op:
		return translateOp(n)
	}
	return "", nil, &cql2.TranslationError{
		Backend: backendID,
		Reason:  fmt.Sprintf("top-level node type %T not allowed", e),
	}
}

func translateOp(op *cql2.Op) (string, []any, error) {
	switch op.Op {
	case cql2.OpAnd, cql2.OpOr:
		joiner := " AND "
		if op.Op == cql2.OpOr {
			joiner = " OR "
		}
		parts := make([]string, 0, len(op.Args))
		args := []any{}
		for _, a := range op.Args {
			frag, fargs, err := translateFilter(a)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, frag)
			args = append(args, fargs...)
		}
		return "(" + strings.Join(parts, joiner) + ")", args, nil

	case cql2.OpNot:
		if len(op.Args) != 1 {
			return "", nil, arityErr(op.Op)
		}
		frag, args, err := translateFilter(op.Args[0])
		if err != nil {
			return "", nil, err
		}
		return "(NOT " + frag + ")", args, nil

	case cql2.OpEq:
		col, val, err := propAndLiteral(op.Args)
		if err != nil {
			return "", nil, err
		}
		if val == nil {
			return col + " IS NULL", nil, nil
		}
		return col + " = ?", []any{val}, nil

	case cql2.OpNeq:
		col, val, err := propAndLiteral(op.Args)
		if err != nil {
			return "", nil, err
		}
		if val == nil {
			return col + " IS NOT NULL", nil, nil
		}
		// `IS NOT ?` treats NULL as not-equal-to-non-null, which is
		// the parity-safe choice for STAC searches.
		return col + " IS NOT ?", []any{val}, nil

	case cql2.OpLt, cql2.OpLte, cql2.OpGt, cql2.OpGte:
		col, val, err := propAndLiteral(op.Args)
		if err != nil {
			return "", nil, err
		}
		sym := map[cql2.Operator]string{
			cql2.OpLt: "<", cql2.OpLte: "<=", cql2.OpGt: ">", cql2.OpGte: ">=",
		}[op.Op]
		return col + " " + sym + " ?", []any{val}, nil

	case cql2.OpBetween:
		if len(op.Args) != 3 {
			return "", nil, arityErr(op.Op)
		}
		col, ok := propRef(op.Args[0])
		if !ok {
			return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "first arg must be a property"}
		}
		lo, err := literalValue(op.Args[1])
		if err != nil {
			return "", nil, err
		}
		hi, err := literalValue(op.Args[2])
		if err != nil {
			return "", nil, err
		}
		return mapColumn(col) + " BETWEEN ? AND ?", []any{lo, hi}, nil

	case cql2.OpIn:
		if len(op.Args) < 2 {
			return "", nil, arityErr(op.Op)
		}
		col, ok := propRef(op.Args[0])
		if !ok {
			return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "first arg must be a property"}
		}
		var values []any
		for _, a := range op.Args[1:] {
			if arr, ok := a.(*cql2.ArrayLit); ok {
				for _, e := range arr.Elements {
					v, err := literalValue(e)
					if err != nil {
						return "", nil, err
					}
					values = append(values, v)
				}
				continue
			}
			v, err := literalValue(a)
			if err != nil {
				return "", nil, err
			}
			values = append(values, v)
		}
		if len(values) == 0 {
			return "0", nil, nil
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		return mapColumn(col) + " IN (" + ph + ")", values, nil

	case cql2.OpLike:
		if len(op.Args) != 2 {
			return "", nil, arityErr(op.Op)
		}
		col, ok := propRef(op.Args[0])
		if !ok {
			return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "first arg must be a property"}
		}
		pat, ok := stringLit(op.Args[1])
		if !ok {
			return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "second arg must be a string literal"}
		}
		// CQL2 wildcards %/_ map directly to SQL LIKE wildcards.
		return mapColumn(col) + " LIKE ?", []any{pat}, nil

	case cql2.OpIsNull:
		if len(op.Args) != 1 {
			return "", nil, arityErr(op.Op)
		}
		col, ok := propRef(op.Args[0])
		if !ok {
			return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "arg must be a property"}
		}
		return mapColumn(col) + " IS NULL", nil, nil

	case cql2.OpSIntersects, cql2.OpSWithin, cql2.OpSContains, cql2.OpSDisjoint:
		return translateSpatial(op)

	case cql2.OpTAfter, cql2.OpTBefore, cql2.OpTDuring, cql2.OpTIntersects, cql2.OpTEquals:
		return translateTemporal(op)
	}

	return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "operator not supported"}
}

func translateSpatial(op *cql2.Op) (string, []any, error) {
	if len(op.Args) != 2 {
		return "", nil, arityErr(op.Op)
	}
	col, ok := propRef(op.Args[0])
	if !ok {
		return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "first arg must be a property (e.g. geometry)"}
	}
	wkt, err := spatialWKT(op.Args[1])
	if err != nil {
		return "", nil, err
	}
	fn := map[cql2.Operator]string{
		cql2.OpSIntersects: "ST_Intersects",
		cql2.OpSWithin:     "ST_Within",
		cql2.OpSContains:   "ST_Contains",
		cql2.OpSDisjoint:   "ST_Disjoint",
	}[op.Op]
	return fmt.Sprintf("%s(%s, GeomFromText(?, 4326)) = 1", fn, mapColumn(col)), []any{wkt}, nil
}

func translateTemporal(op *cql2.Op) (string, []any, error) {
	if len(op.Args) != 2 {
		return "", nil, arityErr(op.Op)
	}
	col := "items.datetime"
	if name, ok := propRef(op.Args[0]); ok {
		col = mapColumn(name)
	}
	v, err := literalValue(op.Args[1])
	if err != nil {
		return "", nil, err
	}
	switch op.Op {
	case cql2.OpTAfter:
		return col + " > ?", []any{v}, nil
	case cql2.OpTBefore:
		return col + " < ?", []any{v}, nil
	case cql2.OpTEquals:
		return col + " = ?", []any{v}, nil
	case cql2.OpTDuring, cql2.OpTIntersects:
		// IntervalLit comes through as [2]any{lo, hi} from literalValue.
		if iv, ok := v.([2]any); ok {
			lo, hi := iv[0], iv[1]
			parts := []string{}
			args := []any{}
			if lo != nil {
				parts = append(parts, col+" >= ?")
				args = append(args, lo)
			}
			if hi != nil {
				parts = append(parts, col+" <= ?")
				args = append(args, hi)
			}
			if len(parts) == 0 {
				return "1", nil, nil
			}
			return "(" + strings.Join(parts, " AND ") + ")", args, nil
		}
		return col + " = ?", []any{v}, nil
	}
	return "", nil, &cql2.TranslationError{Backend: backendID, Op: op.Op, Reason: "unsupported temporal op"}
}

// mapColumn translates a STAC property name into the SQL column
// reference used by the items table. Top-level fields map to dedicated
// columns; everything else is extracted from the JSON `properties` blob.
//
// Already-qualified `properties.<x>` paths pass through to a JSON path
// expression (preserving stac-server's convention) so callers that send
// the prefix don't get double-prefixed.
func mapColumn(name string) string {
	switch name {
	case "id":
		return "items.id"
	case "collection":
		return "items.collection_id"
	case "datetime":
		return "items.datetime"
	case "geometry":
		return "items.geom"
	}
	if strings.HasPrefix(name, "properties.") {
		return jsonPath(strings.TrimPrefix(name, "properties."))
	}
	return jsonPath(name)
}

func jsonPath(name string) string {
	// Quote the property name in the JSON path so colon-bearing keys
	// (e.g. "eo:cloud_cover") survive — `$."eo:cloud_cover"`.
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `json_extract(items.properties, '$."` + escaped + `"')`
}

// ---- helpers (sibling of opensearch translator's helpers) ----------------

func propAndLiteral(args []cql2.Expression) (string, any, error) {
	if len(args) != 2 {
		return "", nil, &cql2.TranslationError{Backend: backendID, Reason: "arity"}
	}
	if name, ok := propRef(args[0]); ok {
		v, err := literalValue(args[1])
		if err != nil {
			return "", nil, err
		}
		return mapColumn(name), v, nil
	}
	if name, ok := propRef(args[1]); ok {
		v, err := literalValue(args[0])
		if err != nil {
			return "", nil, err
		}
		return mapColumn(name), v, nil
	}
	return "", nil, &cql2.TranslationError{Backend: backendID, Reason: "expected property + literal"}
}

func propRef(e cql2.Expression) (string, bool) {
	if p, ok := e.(*cql2.PropertyRef); ok {
		return p.Name, true
	}
	return "", false
}

func stringLit(e cql2.Expression) (string, bool) {
	if s, ok := e.(*cql2.StringLit); ok {
		return s.Value, true
	}
	return "", false
}

func literalValue(e cql2.Expression) (any, error) {
	switch n := e.(type) {
	case *cql2.StringLit:
		return n.Value, nil
	case *cql2.BoolLit:
		// SQLite has no boolean — store as 0/1 to match json_extract.
		if n.Value {
			return int64(1), nil
		}
		return int64(0), nil
	case *cql2.NumLit:
		f, err := n.Value.Float64()
		if err != nil {
			return nil, &cql2.TranslationError{Backend: backendID, Reason: fmt.Sprintf("number %q: %v", n.Value.String(), err)}
		}
		return f, nil
	case *cql2.NullLit:
		return nil, nil
	case *cql2.TimestampLit:
		return n.Value.UTC().Format(time.RFC3339), nil
	case *cql2.DateLit:
		return n.Value.UTC().Format("2006-01-02"), nil
	case *cql2.IntervalLit:
		var lo, hi any
		if t, ok := n.Start.(*cql2.TimestampLit); ok {
			lo = t.Value.UTC().Format(time.RFC3339)
		}
		if t, ok := n.End.(*cql2.TimestampLit); ok {
			hi = t.Value.UTC().Format(time.RFC3339)
		}
		return [2]any{lo, hi}, nil
	}
	return nil, &cql2.TranslationError{Backend: backendID, Reason: fmt.Sprintf("unsupported literal type %T", e)}
}

// spatialWKT renders a CQL2 spatial literal (GeomLit or BBoxLit) into
// the WKT form GeomFromText accepts.
func spatialWKT(e cql2.Expression) (string, error) {
	switch n := e.(type) {
	case *cql2.BBoxLit:
		if len(n.Coords) < 4 {
			return "", &cql2.TranslationError{Backend: backendID, Reason: "bbox needs 4 elements"}
		}
		w, s, e, north := n.Coords[0], n.Coords[1], n.Coords[2], n.Coords[3]
		return fmt.Sprintf(
			"POLYGON((%s %s,%s %s,%s %s,%s %s,%s %s))",
			f(w), f(s), f(e), f(s), f(e), f(north), f(w), f(north), f(w), f(s),
		), nil
	case *cql2.GeomLit:
		// Render a Point or Polygon directly; bail to GeoJSON-as-WKT
		// fallback for richer shapes.
		switch g := n.Geom.(type) {
		case *cql2.Point:
			return fmt.Sprintf("POINT(%s %s)", f(g.Coord.X), f(g.Coord.Y)), nil
		case *cql2.Polygon:
			parts := make([]string, 0, len(g.Rings))
			for _, ring := range g.Rings {
				ring := ring
				cs := make([]string, 0, len(ring))
				for _, c := range ring {
					cs = append(cs, fmt.Sprintf("%s %s", f(c.X), f(c.Y)))
				}
				parts = append(parts, "("+strings.Join(cs, ",")+")")
			}
			return "POLYGON(" + strings.Join(parts, ",") + ")", nil
		}
		// Fallback: serialize whatever shape upstream gave us as JSON
		// and let SpatiaLite parse it via GeomFromGeoJSON, which is
		// available in modern builds. If absent the query errors with
		// a clear message; spatial filters of arbitrary geometry on
		// SpatiaLite are best-effort.
		b, err := json.Marshal(n.Geom)
		if err != nil {
			return "", &cql2.TranslationError{Backend: backendID, Reason: fmt.Sprintf("encode geom: %v", err)}
		}
		return string(b), nil
	}
	return "", &cql2.TranslationError{Backend: backendID, Reason: fmt.Sprintf("spatial literal type %T", e)}
}

func arityErr(op cql2.Operator) error {
	return &cql2.TranslationError{Backend: backendID, Op: op, Reason: "arity"}
}
