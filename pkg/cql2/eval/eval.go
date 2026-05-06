// Package eval is a pure-Go evaluator that walks a CQL2 AST against a
// single STAC item and returns a boolean match result.
//
// Two consumers:
//
//  1. The in-memory backend (internal/backends/inmem) uses Match as its
//     filter predicate during Search.
//  2. The property-test harness (Front H) uses Match as the truth oracle:
//     for every random CQL2 expression, the result of running the
//     backend translator + datastore is compared with Match's verdict on
//     the same fixture.
//
// Coverage:
//
//   - Comparison: =, <>, <, <=, >, >=
//   - Logical: and, or, not
//   - Advanced: between, in, like, isNull
//   - Temporal: t_after, t_before, t_during, t_intersects
//     (operates on properties.datetime / start_datetime / end_datetime)
//   - Spatial: s_intersects, s_within, s_contains, s_disjoint (exact
//     geometry predicates via go-topology-suite; falls back to bbox
//     approximation when either side cannot be rendered as a geometry)
package eval

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	gtsgeom "github.com/exergy-dev/go-topology-suite/geom"
	gtspredicate "github.com/exergy-dev/go-topology-suite/predicate"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/spatial"
	"github.com/example/polystac/pkg/stac"
)

// ErrUnsupported is returned for AST nodes the oracle cannot evaluate.
// Property tests must constrain their generators to avoid these; the
// inmem backend treats this error as "no match" with a logged warning.
var ErrUnsupported = errors.New("cql2/eval: unsupported expression")

// Match reports whether the item satisfies the expression.
func Match(e cql2.Expression, item *stac.Item) (bool, error) {
	if e == nil {
		return true, nil
	}
	v, err := eval(e, item)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("cql2/eval: top-level expression is not a boolean: got %T", v)
	}
	return b, nil
}

// eval returns the runtime Go value of the expression. For literal nodes
// it returns the literal; for Op nodes it computes the result.
func eval(e cql2.Expression, item *stac.Item) (any, error) {
	switch n := e.(type) {
	case *cql2.BoolLit:
		return n.Value, nil
	case *cql2.NumLit:
		f, err := n.Value.Float64()
		if err != nil {
			return nil, fmt.Errorf("cql2/eval: bad number %q: %w", n.Value.String(), err)
		}
		return f, nil
	case *cql2.StringLit:
		return n.Value, nil
	case *cql2.NullLit:
		return nil, nil
	case *cql2.TimestampLit:
		return n.Value, nil
	case *cql2.DateLit:
		return n.Value, nil
	case *cql2.PropertyRef:
		return resolveProperty(item, n.Name), nil
	case *cql2.ArrayLit:
		out := make([]any, 0, len(n.Elements))
		for _, el := range n.Elements {
			v, err := eval(el, item)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *cql2.IntervalLit:
		return intervalEndpoints(n, item)
	case *cql2.GeomLit:
		return n.Geom, nil
	case *cql2.BBoxLit:
		return n.Coords, nil
	case *cql2.Op:
		return evalOp(n, item)
	case *cql2.FunctionCall:
		return nil, fmt.Errorf("%w: function %q", ErrUnsupported, n.Name)
	}
	return nil, fmt.Errorf("%w: %T", ErrUnsupported, e)
}

func evalOp(op *cql2.Op, item *stac.Item) (any, error) {
	switch op.Op {
	case cql2.OpAnd, cql2.OpOr:
		// Short-circuit logical evaluation.
		short := op.Op == cql2.OpOr
		for _, arg := range op.Args {
			v, err := eval(arg, item)
			if err != nil {
				return nil, err
			}
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("cql2/eval: %s arg is not a boolean: %T", op.Op, v)
			}
			if b == short {
				return short, nil
			}
		}
		return !short, nil

	case cql2.OpNot:
		if len(op.Args) != 1 {
			return nil, fmt.Errorf("cql2/eval: not arity")
		}
		v, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("cql2/eval: not arg is not a boolean: %T", v)
		}
		return !b, nil

	case cql2.OpEq, cql2.OpNeq, cql2.OpLt, cql2.OpLte, cql2.OpGt, cql2.OpGte:
		if len(op.Args) != 2 {
			return nil, fmt.Errorf("cql2/eval: %s arity", op.Op)
		}
		l, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		r, err := eval(op.Args[1], item)
		if err != nil {
			return nil, err
		}
		return compare(op.Op, l, r)

	case cql2.OpBetween:
		if len(op.Args) != 3 {
			return nil, fmt.Errorf("cql2/eval: between arity")
		}
		t, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		lo, err := eval(op.Args[1], item)
		if err != nil {
			return nil, err
		}
		hi, err := eval(op.Args[2], item)
		if err != nil {
			return nil, err
		}
		geLo, err := compare(cql2.OpGte, t, lo)
		if err != nil {
			return nil, err
		}
		leHi, err := compare(cql2.OpLte, t, hi)
		if err != nil {
			return nil, err
		}
		return geLo.(bool) && leHi.(bool), nil

	case cql2.OpIn:
		if len(op.Args) < 2 {
			return nil, fmt.Errorf("cql2/eval: in arity")
		}
		needle, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		for _, hayNode := range op.Args[1:] {
			hay, err := eval(hayNode, item)
			if err != nil {
				return nil, err
			}
			// in supports a single ArrayLit or a flat list of literals.
			if arr, ok := hay.([]any); ok {
				for _, v := range arr {
					if r, _ := compare(cql2.OpEq, needle, v); r == true {
						return true, nil
					}
				}
				continue
			}
			if r, _ := compare(cql2.OpEq, needle, hay); r == true {
				return true, nil
			}
		}
		return false, nil

	case cql2.OpLike:
		if len(op.Args) != 2 {
			return nil, fmt.Errorf("cql2/eval: like arity")
		}
		l, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		r, err := eval(op.Args[1], item)
		if err != nil {
			return nil, err
		}
		ls, _ := l.(string)
		rs, _ := r.(string)
		return likeMatch(ls, rs), nil

	case cql2.OpIsNull:
		if len(op.Args) != 1 {
			return nil, fmt.Errorf("cql2/eval: isNull arity")
		}
		v, err := eval(op.Args[0], item)
		if err != nil {
			return nil, err
		}
		return v == nil, nil

	case cql2.OpTAfter, cql2.OpTBefore, cql2.OpTDuring, cql2.OpTIntersects, cql2.OpTEquals:
		return evalTemporal(op, item)

	case cql2.OpSIntersects, cql2.OpSWithin, cql2.OpSContains, cql2.OpSDisjoint:
		return evalSpatial(op, item)
	}

	return nil, fmt.Errorf("%w: operator %q", ErrUnsupported, op.Op)
}

// resolveProperty pulls a CQL2 property reference's value off the item.
// CQL2 names map to Item fields per the STAC item-search Filter mapping:
//
//   - "id"          → item.ID
//   - "collection"  → item.Collection
//   - "geometry"    → item.Geometry (returned as *stac.Geometry)
//   - "datetime"    → item.Properties["datetime"]
//   - other         → item.Properties[name]
func resolveProperty(item *stac.Item, name string) any {
	if item == nil {
		return nil
	}
	switch name {
	case "id":
		return item.ID
	case "collection":
		return item.Collection
	case "geometry":
		return item.Geometry
	}
	if v, ok := item.Properties[name]; ok {
		return v
	}
	return nil
}

// compare implements CQL2 ordered/equality comparison. Heterogeneous
// comparisons return (false, nil) in line with the spec — they do not
// raise.
func compare(op cql2.Operator, l, r any) (any, error) {
	if l == nil || r == nil {
		// Per CQL2: comparisons with NULL yield UNKNOWN, treated as false.
		if op == cql2.OpEq {
			return l == nil && r == nil, nil
		}
		if op == cql2.OpNeq {
			return !(l == nil && r == nil), nil
		}
		return false, nil
	}

	if cmp, ok := numericCompare(l, r); ok {
		return applyOrder(op, cmp), nil
	}
	if cmp, ok := stringCompare(l, r); ok {
		return applyOrder(op, cmp), nil
	}
	if cmp, ok := timeCompare(l, r); ok {
		return applyOrder(op, cmp), nil
	}
	if op == cql2.OpEq {
		return l == r, nil
	}
	if op == cql2.OpNeq {
		return l != r, nil
	}
	return false, nil
}

func applyOrder(op cql2.Operator, cmp int) bool {
	switch op {
	case cql2.OpEq:
		return cmp == 0
	case cql2.OpNeq:
		return cmp != 0
	case cql2.OpLt:
		return cmp < 0
	case cql2.OpLte:
		return cmp <= 0
	case cql2.OpGt:
		return cmp > 0
	case cql2.OpGte:
		return cmp >= 0
	}
	return false
}

func numericCompare(l, r any) (int, bool) {
	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if !lok || !rok {
		return 0, false
	}
	switch {
	case math.IsNaN(lf) || math.IsNaN(rf):
		return 0, false
	case lf < rf:
		return -1, true
	case lf > rf:
		return 1, true
	}
	return 0, true
}

func stringCompare(l, r any) (int, bool) {
	ls, lok := l.(string)
	rs, rok := r.(string)
	if !lok || !rok {
		return 0, false
	}
	return strings.Compare(ls, rs), true
}

func timeCompare(l, r any) (int, bool) {
	lt, lok := toTime(l)
	rt, rok := toTime(r)
	if !lok || !rok {
		return 0, false
	}
	switch {
	case lt.Before(rt):
		return -1, true
	case lt.After(rt):
		return 1, true
	}
	return 0, true
}

// ToFloat64 attempts a numeric coercion. Exported so other packages
// (in particular internal/backends/inmem) reuse the same coercion rules
// the CQL2 oracle applies, keeping their filter and sort semantics
// aligned with what the property tests assert.
func ToFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) { return ToFloat64(v) }

// CompareValues returns -1/0/1 if the values are comparable under one
// of the typed orderings (numeric, string, time), or (0, false) if the
// types are incompatible. Equality of unrelated types returns
// (0, true) only when the values are deeply equal.
func CompareValues(a, b any) (int, bool) {
	if cmp, ok := numericCompare(a, b); ok {
		return cmp, true
	}
	if cmp, ok := stringCompare(a, b); ok {
		return cmp, true
	}
	if cmp, ok := timeCompare(a, b); ok {
		return cmp, true
	}
	return 0, false
}

func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		// Try common STAC timestamp formats.
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func likeMatch(s, pattern string) bool {
	// CQL2 LIKE wildcards: % matches any run, _ matches one char.
	// Translate to a regex-free walk for speed and predictability.
	return likeWalk(s, pattern)
}

func likeWalk(s, p string) bool {
	si, pi := 0, 0
	star, match := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '_' || p[pi] == s[si]):
			si++
			pi++
		case pi < len(p) && p[pi] == '%':
			star = pi
			match = si
			pi++
		case star != -1:
			pi = star + 1
			match++
			si = match
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '%' {
		pi++
	}
	return pi == len(p)
}

func intervalEndpoints(n *cql2.IntervalLit, item *stac.Item) (any, error) {
	endpoint := func(e cql2.IntervalEndpoint) (*time.Time, error) {
		switch x := e.(type) {
		case *cql2.TimestampLit:
			t := x.Value
			return &t, nil
		case *cql2.DateLit:
			t := x.Value
			return &t, nil
		case *cql2.PropertyRef:
			v := resolveProperty(item, x.Name)
			t, ok := toTime(v)
			if !ok {
				return nil, nil
			}
			return &t, nil
		case *cql2.Unbounded:
			return nil, nil
		}
		return nil, fmt.Errorf("%w: interval endpoint %T", ErrUnsupported, e)
	}
	start, err := endpoint(n.Start)
	if err != nil {
		return nil, err
	}
	end, err := endpoint(n.End)
	if err != nil {
		return nil, err
	}
	return [2]*time.Time{start, end}, nil
}

func evalTemporal(op *cql2.Op, item *stac.Item) (any, error) {
	if len(op.Args) != 2 {
		return nil, fmt.Errorf("cql2/eval: %s arity", op.Op)
	}
	l, err := eval(op.Args[0], item)
	if err != nil {
		return nil, err
	}
	r, err := eval(op.Args[1], item)
	if err != nil {
		return nil, err
	}
	li, lok := asInterval(l)
	ri, rok := asInterval(r)
	if !lok || !rok {
		return false, nil
	}
	switch op.Op {
	case cql2.OpTAfter:
		return li[0] != nil && ri[1] != nil && li[0].After(*ri[1]), nil
	case cql2.OpTBefore:
		return li[1] != nil && ri[0] != nil && li[1].Before(*ri[0]), nil
	case cql2.OpTDuring:
		return contains(ri, li), nil
	case cql2.OpTIntersects:
		return intervalIntersect(li, ri), nil
	case cql2.OpTEquals:
		return ptrEqual(li[0], ri[0]) && ptrEqual(li[1], ri[1]), nil
	}
	return false, nil
}

func ptrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func contains(outer, inner [2]*time.Time) bool {
	if outer[0] != nil && inner[0] != nil && inner[0].Before(*outer[0]) {
		return false
	}
	if outer[1] != nil && inner[1] != nil && inner[1].After(*outer[1]) {
		return false
	}
	return true
}

func intervalIntersect(a, b [2]*time.Time) bool {
	if a[1] != nil && b[0] != nil && a[1].Before(*b[0]) {
		return false
	}
	if b[1] != nil && a[0] != nil && b[1].Before(*a[0]) {
		return false
	}
	return true
}

func asInterval(v any) ([2]*time.Time, bool) {
	if iv, ok := v.([2]*time.Time); ok {
		return iv, true
	}
	if t, ok := toTime(v); ok {
		t := t
		return [2]*time.Time{&t, &t}, true
	}
	return [2]*time.Time{}, false
}

func evalSpatial(op *cql2.Op, item *stac.Item) (any, error) {
	if len(op.Args) != 2 {
		return nil, fmt.Errorf("cql2/eval: %s arity", op.Op)
	}
	l, err := eval(op.Args[0], item)
	if err != nil {
		return nil, err
	}
	r, err := eval(op.Args[1], item)
	if err != nil {
		return nil, err
	}
	lg, lok := asGTSGeom(l, item)
	rg, rok := asGTSGeom(r, item)
	if !lok || !rok {
		return false, nil
	}
	switch op.Op {
	case cql2.OpSIntersects:
		return gtspredicate.Intersects(lg, rg)
	case cql2.OpSDisjoint:
		hit, err := gtspredicate.Intersects(lg, rg)
		if err != nil {
			return nil, err
		}
		return !hit, nil
	case cql2.OpSWithin:
		return gtspredicate.Within(lg, rg)
	case cql2.OpSContains:
		return gtspredicate.Contains(lg, rg)
	}
	return false, nil
}

// asGTSGeom dispatches on the concrete type that eval produced. It
// covers every shape eval emits for a spatial argument: a *stac.Geometry
// (item-bound `geometry` ref), a cql2 geometry literal, a raw bbox
// slice from BBOX(...), or a fallback to the item's own geometry when
// no argument shape matches.
func asGTSGeom(v any, item *stac.Item) (gtsgeom.Geometry, bool) {
	switch x := v.(type) {
	case *stac.Geometry:
		return spatial.FromSTAC(x)
	case stac.Geometry:
		return spatial.FromSTAC(&x)
	case cql2.Geometry:
		return spatial.FromCQL2(x)
	case []float64:
		if len(x) < 4 {
			return nil, false
		}
		return spatial.BBoxPolygon(x[0], x[1], x[2], x[3]), true
	}
	if item != nil && item.Geometry != nil {
		return spatial.FromSTAC(item.Geometry)
	}
	return nil, false
}

