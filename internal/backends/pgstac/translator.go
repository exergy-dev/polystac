// Package pgstac implements the Repository interface against a pgstac
// (PostgreSQL/PostGIS + pgstac extension) datastore.
//
// Read path: invokes pgstac.search($1::jsonb) and decodes the returned
// FeatureCollection. The translator's job is to turn a backend-agnostic
// SearchRequest into the JSON payload pgstac expects, which is very
// close to the STAC API search spec — making the translator largely a
// faithful re-emit rather than a transformation.
//
// Write path: pgstac.create_item / update_item / delete_item /
// create_items.
//
// Schema version: validated at startup via SELECT pgstac.get_version();
// PolyStac refuses to start if the schema is incompatible (SDD §8.1).
package pgstac

import (
	"encoding/json"
	"fmt"

	upstreamcql "github.com/exergy-dev/go-cql2"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
)

// translateSearch converts a backend-agnostic SearchRequest into the
// JSON shape pgstac.search() expects. The output mirrors the STAC API
// item-search spec: collections, ids, bbox, intersects, datetime,
// limit, token, query, filter, filter-lang, sortby, fields.
func translateSearch(req repository.SearchRequest) ([]byte, error) {
	out := map[string]any{}
	if len(req.Collections) > 0 {
		out["collections"] = req.Collections
	}
	if len(req.IDs) > 0 {
		out["ids"] = req.IDs
	}
	if len(req.BBox) > 0 {
		out["bbox"] = req.BBox
	}
	if req.Intersects != nil {
		out["intersects"] = req.Intersects
	}
	if req.Datetime != nil {
		out["datetime"] = formatDatetime(req.Datetime)
	}
	if req.Limit > 0 {
		out["limit"] = req.Limit
	}
	if req.Token != "" {
		out["token"] = req.Token
	}
	if len(req.Query) > 0 {
		out["query"] = req.Query
	}
	if req.Filter != nil {
		filterJSON, err := encodeFilter(req.Filter)
		if err != nil {
			return nil, err
		}
		out["filter"] = json.RawMessage(filterJSON)
		// We always re-encode the parsed AST as CQL2-JSON before sending
		// (encodeFilter, above) — pgstac only accepts cql2-json or
		// cql-json on this field. The original surface form the user
		// typed is recorded in req.FilterLang for conformance reporting
		// but is not propagated here.
		out["filter-lang"] = "cql2-json"
	}
	if len(req.SortBy) > 0 {
		sort := make([]map[string]string, 0, len(req.SortBy))
		for _, s := range req.SortBy {
			dir := "asc"
			if s.Direction == repository.SortDesc {
				dir = "desc"
			}
			sort = append(sort, map[string]string{"field": s.Field, "direction": dir})
		}
		out["sortby"] = sort
	}
	if req.Fields != nil {
		fields := map[string]any{}
		if len(req.Fields.Include) > 0 {
			fields["include"] = req.Fields.Include
		}
		if len(req.Fields.Exclude) > 0 {
			fields["exclude"] = req.Fields.Exclude
		}
		out["fields"] = fields
	}
	return json.Marshal(out)
}

// encodeFilter serializes the parsed AST back to CQL2-JSON. Before
// encoding it normalizes a known dialect divergence between go-cql2 and
// pgstac: go-cql2 emits `between(prop, lo, hi)` with three flat args,
// while pgstac's cql2_query expects two args with the bounds nested as
// an array. Rewriting to `(prop >= lo) AND (prop <= hi)` is equivalent
// and accepted unchanged by both sides.
func encodeFilter(e cql2.Expression) ([]byte, error) {
	rewritten := upstreamcql.Transform(e, rewriteBetween)
	b, err := upstreamcql.Encode(rewritten, upstreamcql.EncodingJSON)
	if err != nil {
		return nil, &cql2.TranslationError{
			Backend: "pgstac",
			Reason:  fmt.Sprintf("encode CQL2 to JSON: %v", err),
		}
	}
	return b, nil
}

// rewriteBetween folds `between(t, lo, hi)` into `(t >= lo) AND (t <= hi)`.
// Returns the input unchanged for any other node.
func rewriteBetween(n cql2.Expression) cql2.Expression {
	op, ok := n.(*cql2.Op)
	if !ok || op.Op != cql2.OpBetween || len(op.Args) != 3 {
		return n
	}
	target, lo, hi := op.Args[0], op.Args[1], op.Args[2]
	return &cql2.Op{Op: cql2.OpAnd, Args: []cql2.Expression{
		&cql2.Op{Op: cql2.OpGte, Args: []cql2.Expression{target, lo}},
		&cql2.Op{Op: cql2.OpLte, Args: []cql2.Expression{target, hi}},
	}}
}

// formatDatetime serializes the temporal interval per STAC API rules
// ("start/end" with ".." for open endpoints). pgstac accepts both a
// single instant and an interval in this same field.
func formatDatetime(ti *repository.TemporalInterval) string {
	const open = ".."
	if ti.IsPoint() {
		return ti.Start.UTC().Format("2006-01-02T15:04:05Z")
	}
	start := open
	end := open
	if ti.Start != nil {
		start = ti.Start.UTC().Format("2006-01-02T15:04:05Z")
	}
	if ti.End != nil {
		end = ti.End.UTC().Format("2006-01-02T15:04:05Z")
	}
	return start + "/" + end
}
