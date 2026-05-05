package cql2

import (
	upstream "github.com/exergy-dev/go-cql2"
)

// Visitor is the upstream visitor interface re-exported. Backend
// translators implement Visitor and call Walk to traverse the AST.
//
// The choice to keep the upstream visitor surface (rather than wrap it in
// a "semantic" interface) is deliberate: backend translators need access
// to the full set of node types, and a higher-level wrapper would either
// hide nodes the translator must handle or duplicate the upstream
// taxonomy. Keeping the AST as the boundary means adding a new backend is
// a matter of writing one Visitor implementation against types this
// package re-exports.
type Visitor = upstream.Visitor

// Walk traverses an Expression with a Visitor. Translators typically use
// this together with a type switch on the concrete node types below.
func Walk(v Visitor, e Expression) {
	upstream.Walk(v, e)
}

// Inspect walks the AST, calling f at each node. Returning false from f
// stops descent into that node's children.
func Inspect(e Expression, f func(Expression) bool) {
	upstream.Inspect(e, f)
}

// Operator is a typed string identifying a CQL2 operator.
type Operator = upstream.Operator

// Operator constants. These cover the comparison, logical, advanced
// comparison (between/in/like/isNull), arithmetic, spatial, temporal,
// array, and case/accent-insensitive operator families. Backend
// translators typically dispatch on Op.Op against these constants.
const (
	OpAnd Operator = upstream.OpAnd
	OpOr  Operator = upstream.OpOr
	OpNot Operator = upstream.OpNot

	OpEq  Operator = upstream.OpEq
	OpNeq Operator = upstream.OpNeq
	OpLt  Operator = upstream.OpLt
	OpLte Operator = upstream.OpLte
	OpGt  Operator = upstream.OpGt
	OpGte Operator = upstream.OpGte

	OpLike    Operator = upstream.OpLike
	OpBetween Operator = upstream.OpBetween
	OpIn      Operator = upstream.OpIn
	OpIsNull  Operator = upstream.OpIsNull

	OpSIntersects Operator = upstream.OpSIntersects
	OpSEquals     Operator = upstream.OpSEquals
	OpSDisjoint   Operator = upstream.OpSDisjoint
	OpSTouches    Operator = upstream.OpSTouches
	OpSWithin     Operator = upstream.OpSWithin
	OpSOverlaps   Operator = upstream.OpSOverlaps
	OpSCrosses    Operator = upstream.OpSCrosses
	OpSContains   Operator = upstream.OpSContains

	OpTAfter      Operator = upstream.OpTAfter
	OpTBefore     Operator = upstream.OpTBefore
	OpTContains   Operator = upstream.OpTContains
	OpTDisjoint   Operator = upstream.OpTDisjoint
	OpTDuring     Operator = upstream.OpTDuring
	OpTEquals     Operator = upstream.OpTEquals
	OpTIntersects Operator = upstream.OpTIntersects
	OpTOverlaps   Operator = upstream.OpTOverlaps

	OpAContains    Operator = upstream.OpAContains
	OpAContainedBy Operator = upstream.OpAContainedBy
	OpAEquals      Operator = upstream.OpAEquals
	OpAOverlaps    Operator = upstream.OpAOverlaps
)

// AST node type aliases — re-exported so callers do not need to import
// the upstream package directly. Backend translators type-switch on these.
type (
	BoolLit      = upstream.BoolLit
	NumLit       = upstream.NumLit
	StringLit    = upstream.StringLit
	NullLit      = upstream.NullLit
	TimestampLit = upstream.TimestampLit
	DateLit      = upstream.DateLit
	IntervalLit  = upstream.IntervalLit
	GeomLit      = upstream.GeomLit
	BBoxLit      = upstream.BBoxLit
	ArrayLit     = upstream.ArrayLit
	PropertyRef  = upstream.PropertyRef
	Op           = upstream.Op
	FunctionCall = upstream.FunctionCall
	Geometry     = upstream.Geometry

	Coord              = upstream.Coord
	Point              = upstream.Point
	LineString         = upstream.LineString
	Polygon            = upstream.Polygon
	MultiPoint         = upstream.MultiPoint
	MultiLineString    = upstream.MultiLineString
	MultiPolygon       = upstream.MultiPolygon
	GeometryCollection = upstream.GeometryCollection

	IntervalEndpoint = upstream.IntervalEndpoint
	Unbounded        = upstream.Unbounded
)
