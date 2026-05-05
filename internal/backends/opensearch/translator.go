package opensearch

import (
	"fmt"
	"strings"
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// translateSearch turns a SearchRequest into the OpenSearch DSL body.
// The result is the full _search request (query + sort + size +
// _source). Pagination uses search_after; the token is decoded into
// the resume cursor in opensearch.go.
func translateSearch(req repository.SearchRequest, after []any) (map[string]any, error) {
	must := []any{}
	if len(req.IDs) > 0 {
		must = append(must, map[string]any{"terms": map[string]any{"id": req.IDs}})
	}
	if len(req.BBox) > 0 {
		geo, err := bboxGeoShape(req.BBox)
		if err != nil {
			return nil, err
		}
		must = append(must, map[string]any{
			"geo_shape": map[string]any{
				"geometry": map[string]any{"shape": geo, "relation": "intersects"},
			},
		})
	}
	if req.Intersects != nil {
		must = append(must, map[string]any{
			"geo_shape": map[string]any{
				"geometry": map[string]any{"shape": geometryShape(req.Intersects), "relation": "intersects"},
			},
		})
	}
	if req.Datetime != nil {
		must = append(must, datetimeRange(*req.Datetime))
	}
	if len(req.Query) > 0 {
		for field, pred := range req.Query {
			clauses := queryToClauses(field, pred)
			must = append(must, clauses...)
		}
	}
	if req.Filter != nil {
		clause, err := translateFilter(req.Filter)
		if err != nil {
			return nil, err
		}
		must = append(must, clause)
	}

	body := map[string]any{
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"size":  pickSize(req.Limit),
	}

	if len(req.SortBy) > 0 {
		body["track_total_hits"] = true
	} else {
		body["track_total_hits"] = 10000
	}

	// Sort: if user supplied none, use a deterministic default
	// (properties.datetime DESC, id ASC). Always tiebreak on id so
	// search_after is reliable.
	body["sort"] = sortClauses(req.SortBy)

	if len(after) > 0 {
		body["search_after"] = after
	}

	if req.Fields != nil {
		src := map[string]any{}
		if len(req.Fields.Include) > 0 {
			src["includes"] = req.Fields.Include
		}
		if len(req.Fields.Exclude) > 0 {
			src["excludes"] = req.Fields.Exclude
		}
		if len(src) > 0 {
			body["_source"] = src
		}
	}

	return body, nil
}

func pickSize(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 10000 {
		return 10000
	}
	return limit
}

func sortClauses(in []repository.SortClause) []any {
	if len(in) == 0 {
		return []any{
			map[string]any{"properties.datetime": map[string]any{"order": "desc"}},
			map[string]any{"id": map[string]any{"order": "asc"}},
		}
	}
	out := make([]any, 0, len(in)+1)
	idTiebreak := false
	for _, c := range in {
		dir := "asc"
		if c.Direction == repository.SortDesc {
			dir = "desc"
		}
		field := mapField(c.Field)
		out = append(out, map[string]any{field: map[string]any{"order": dir}})
		if field == "id" {
			idTiebreak = true
		}
	}
	if !idTiebreak {
		out = append(out, map[string]any{"id": map[string]any{"order": "asc"}})
	}
	return out
}

// mapField translates a STAC property name into the OpenSearch field
// path. id and collection are top-level keywords; everything else lives
// under properties.
func mapField(name string) string {
	switch name {
	case "id", "collection":
		return name
	case "geometry":
		return "geometry"
	}
	return "properties." + name
}

func datetimeRange(ti repository.TemporalInterval) map[string]any {
	r := map[string]any{}
	if ti.Start != nil {
		r["gte"] = ti.Start.UTC().Format(time.RFC3339)
	}
	if ti.End != nil {
		r["lte"] = ti.End.UTC().Format(time.RFC3339)
	}
	if len(r) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	r["format"] = "strict_date_optional_time"
	return map[string]any{"range": map[string]any{"properties.datetime": r}}
}

func queryToClauses(field string, p repository.Predicate) []any {
	osField := mapField(field)
	out := []any{}
	addRange := func(op string, v any) {
		out = append(out, map[string]any{"range": map[string]any{osField: map[string]any{op: v}}})
	}
	if p.Eq != nil {
		out = append(out, map[string]any{"term": map[string]any{osField: p.Eq}})
	}
	if p.Neq != nil {
		out = append(out, map[string]any{"bool": map[string]any{"must_not": []any{
			map[string]any{"term": map[string]any{osField: p.Neq}},
		}}})
	}
	if p.Lt != nil {
		addRange("lt", p.Lt)
	}
	if p.Lte != nil {
		addRange("lte", p.Lte)
	}
	if p.Gt != nil {
		addRange("gt", p.Gt)
	}
	if p.Gte != nil {
		addRange("gte", p.Gte)
	}
	if len(p.In) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{osField: p.In}})
	}
	if p.StartsWith != "" {
		out = append(out, map[string]any{"prefix": map[string]any{osField: p.StartsWith}})
	}
	if p.EndsWith != "" {
		out = append(out, map[string]any{"wildcard": map[string]any{osField: "*" + p.EndsWith}})
	}
	if p.Contains != "" {
		out = append(out, map[string]any{"wildcard": map[string]any{osField: "*" + p.Contains + "*"}})
	}
	return out
}

// translateFilter walks a CQL2 AST and produces an OpenSearch query
// fragment. Returns *cql2.TranslationError for nodes that have no
// reasonable mapping into the DSL.
func translateFilter(e cql2.Expression) (map[string]any, error) {
	switch n := e.(type) {
	case *cql2.Op:
		return translateOp(n)
	}
	return nil, &cql2.TranslationError{Backend: "opensearch", Reason: fmt.Sprintf("top-level node type %T not allowed", e)}
}

func translateOp(op *cql2.Op) (map[string]any, error) {
	switch op.Op {
	case cql2.OpAnd, cql2.OpOr:
		clauses := make([]any, 0, len(op.Args))
		for _, a := range op.Args {
			sub, err := translateFilter(a)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, sub)
		}
		key := "must"
		extra := map[string]any{}
		if op.Op == cql2.OpOr {
			key = "should"
			extra["minimum_should_match"] = 1
		}
		body := map[string]any{key: clauses}
		for k, v := range extra {
			body[k] = v
		}
		return map[string]any{"bool": body}, nil

	case cql2.OpNot:
		if len(op.Args) != 1 {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
		}
		sub, err := translateFilter(op.Args[0])
		if err != nil {
			return nil, err
		}
		return map[string]any{"bool": map[string]any{"must_not": []any{sub}}}, nil

	case cql2.OpEq:
		field, value, err := propAndLiteral(op.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"term": map[string]any{field: value}}, nil

	case cql2.OpNeq:
		field, value, err := propAndLiteral(op.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"bool": map[string]any{"must_not": []any{
			map[string]any{"term": map[string]any{field: value}},
		}}}, nil

	case cql2.OpLt, cql2.OpLte, cql2.OpGt, cql2.OpGte:
		field, value, err := propAndLiteral(op.Args)
		if err != nil {
			return nil, err
		}
		osOp := map[cql2.Operator]string{
			cql2.OpLt:  "lt",
			cql2.OpLte: "lte",
			cql2.OpGt:  "gt",
			cql2.OpGte: "gte",
		}[op.Op]
		return map[string]any{"range": map[string]any{field: map[string]any{osOp: value}}}, nil

	case cql2.OpBetween:
		if len(op.Args) != 3 {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
		}
		field, ok := propRef(op.Args[0])
		if !ok {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "first arg must be a property"}
		}
		lo, err := literalValue(op.Args[1])
		if err != nil {
			return nil, err
		}
		hi, err := literalValue(op.Args[2])
		if err != nil {
			return nil, err
		}
		return map[string]any{"range": map[string]any{mapField(field): map[string]any{"gte": lo, "lte": hi}}}, nil

	case cql2.OpIn:
		if len(op.Args) < 2 {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
		}
		field, ok := propRef(op.Args[0])
		if !ok {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "first arg must be a property"}
		}
		var values []any
		for _, a := range op.Args[1:] {
			if arr, ok := a.(*cql2.ArrayLit); ok {
				for _, e := range arr.Elements {
					v, err := literalValue(e)
					if err != nil {
						return nil, err
					}
					values = append(values, v)
				}
				continue
			}
			v, err := literalValue(a)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return map[string]any{"terms": map[string]any{mapField(field): values}}, nil

	case cql2.OpLike:
		if len(op.Args) != 2 {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
		}
		field, ok := propRef(op.Args[0])
		if !ok {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "first arg must be a property"}
		}
		pat, ok := stringLit(op.Args[1])
		if !ok {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "second arg must be a string literal"}
		}
		// CQL2 wildcards: % → *, _ → ?  (escape OS specials first)
		escaped := strings.NewReplacer(`*`, `\*`, `?`, `\?`).Replace(pat)
		dsl := strings.NewReplacer(`%`, "*", `_`, "?").Replace(escaped)
		return map[string]any{"wildcard": map[string]any{mapField(field): dsl}}, nil

	case cql2.OpIsNull:
		if len(op.Args) != 1 {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
		}
		field, ok := propRef(op.Args[0])
		if !ok {
			return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arg must be a property"}
		}
		return map[string]any{"bool": map[string]any{"must_not": []any{
			map[string]any{"exists": map[string]any{"field": mapField(field)}},
		}}}, nil

	case cql2.OpSIntersects, cql2.OpSWithin, cql2.OpSContains, cql2.OpSDisjoint:
		return translateSpatial(op)

	case cql2.OpTAfter, cql2.OpTBefore, cql2.OpTDuring, cql2.OpTIntersects, cql2.OpTEquals:
		return translateTemporal(op)
	}

	return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "operator not supported"}
}

func translateSpatial(op *cql2.Op) (map[string]any, error) {
	if len(op.Args) != 2 {
		return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
	}
	field, ok := propRef(op.Args[0])
	if !ok {
		return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "first arg must be a property (e.g. geometry)"}
	}
	shape, err := spatialShape(op.Args[1])
	if err != nil {
		return nil, err
	}
	relation := map[cql2.Operator]string{
		cql2.OpSIntersects: "intersects",
		cql2.OpSWithin:     "within",
		cql2.OpSContains:   "contains",
		cql2.OpSDisjoint:   "disjoint",
	}[op.Op]
	return map[string]any{
		"geo_shape": map[string]any{
			mapField(field): map[string]any{"shape": shape, "relation": relation},
		},
	}, nil
}

func translateTemporal(op *cql2.Op) (map[string]any, error) {
	if len(op.Args) != 2 {
		return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "arity"}
	}
	// Translate against properties.datetime (canonical STAC temporal field).
	field := "properties.datetime"
	if name, ok := propRef(op.Args[0]); ok {
		field = mapField(name)
	}
	v, err := literalValue(op.Args[1])
	if err != nil {
		return nil, err
	}
	switch op.Op {
	case cql2.OpTAfter:
		return rangeOn(field, "gt", v), nil
	case cql2.OpTBefore:
		return rangeOn(field, "lt", v), nil
	case cql2.OpTDuring, cql2.OpTIntersects:
		// If literal is an interval, translate to a [gte,lte] range.
		if iv, ok := v.([2]any); ok {
			r := map[string]any{}
			if iv[0] != nil {
				r["gte"] = iv[0]
			}
			if iv[1] != nil {
				r["lte"] = iv[1]
			}
			r["format"] = "strict_date_optional_time"
			return map[string]any{"range": map[string]any{field: r}}, nil
		}
		return rangeOn(field, "gte", v), nil
	case cql2.OpTEquals:
		return map[string]any{"term": map[string]any{field: v}}, nil
	}
	return nil, &cql2.TranslationError{Backend: "opensearch", Op: op.Op, Reason: "unsupported temporal op"}
}

func rangeOn(field, op string, v any) map[string]any {
	return map[string]any{"range": map[string]any{field: map[string]any{op: v, "format": "strict_date_optional_time"}}}
}

// propAndLiteral interprets the common (property, literal) shape used
// by =/<>/</<=/>/>=. Either order is accepted (`x = 1` or `1 = x`) but
// both arguments must include exactly one property reference.
func propAndLiteral(args []cql2.Expression) (string, any, error) {
	if len(args) != 2 {
		return "", nil, &cql2.TranslationError{Backend: "opensearch", Reason: "arity"}
	}
	if name, ok := propRef(args[0]); ok {
		v, err := literalValue(args[1])
		if err != nil {
			return "", nil, err
		}
		return mapField(name), v, nil
	}
	if name, ok := propRef(args[1]); ok {
		v, err := literalValue(args[0])
		if err != nil {
			return "", nil, err
		}
		return mapField(name), v, nil
	}
	return "", nil, &cql2.TranslationError{Backend: "opensearch", Reason: "expected property + literal"}
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
		return n.Value, nil
	case *cql2.NumLit:
		f, err := n.Value.Float64()
		if err != nil {
			return nil, &cql2.TranslationError{Backend: "opensearch", Reason: fmt.Sprintf("number %q: %v", n.Value.String(), err)}
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
	return nil, &cql2.TranslationError{Backend: "opensearch", Reason: fmt.Sprintf("unsupported literal type %T", e)}
}

func spatialShape(e cql2.Expression) (any, error) {
	switch n := e.(type) {
	case *cql2.BBoxLit:
		return bboxGeoShape(n.Coords)
	case *cql2.GeomLit:
		return geometryFromCQL2(n.Geom), nil
	}
	return nil, &cql2.TranslationError{Backend: "opensearch", Reason: fmt.Sprintf("spatial literal type %T", e)}
}

func bboxGeoShape(coords []float64) (map[string]any, error) {
	if len(coords) < 4 {
		return nil, &cql2.TranslationError{Backend: "opensearch", Reason: "bbox needs 4 elements"}
	}
	w, s, e, n := coords[0], coords[1], coords[2], coords[3]
	return map[string]any{
		"type":        "envelope",
		"coordinates": [][]float64{{w, n}, {e, s}},
	}, nil
}

func geometryShape(g *stac.Geometry) any {
	if g == nil {
		return nil
	}
	out := map[string]any{"type": string(g.Type)}
	if len(g.Geometries) > 0 {
		subs := make([]any, 0, len(g.Geometries))
		for _, sg := range g.Geometries {
			sg := sg
			subs = append(subs, geometryShape(&sg))
		}
		out["geometries"] = subs
	} else if g.Coordinates != nil {
		out["coordinates"] = g.Coordinates
	}
	return out
}

func geometryFromCQL2(g cql2.Geometry) any {
	switch x := g.(type) {
	case *cql2.Point:
		c := []float64{x.Coord.X, x.Coord.Y}
		return map[string]any{"type": "Point", "coordinates": c}
	case *cql2.Polygon:
		rings := make([][][]float64, 0, len(x.Rings))
		for _, r := range x.Rings {
			ring := make([][]float64, 0, len(r))
			for _, c := range r {
				ring = append(ring, []float64{c.X, c.Y})
			}
			rings = append(rings, ring)
		}
		return map[string]any{"type": "Polygon", "coordinates": rings}
	}
	return nil
}
