//go:build cgo && spatialite

package spatialite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

func (r *Repo) Search(ctx context.Context, req repository.SearchRequest) (*repository.Page[*stac.Item], error) {
	where := []string{}
	args := []any{}

	if len(req.Collections) > 0 {
		where = append(where, "items.collection_id IN ("+placeholders(len(req.Collections))+")")
		for _, c := range req.Collections {
			args = append(args, c)
		}
	}
	if len(req.IDs) > 0 {
		where = append(where, "items.id IN ("+placeholders(len(req.IDs))+")")
		for _, id := range req.IDs {
			args = append(args, id)
		}
	}
	if len(req.BBox) >= 4 {
		// Cheap bbox-overlap pre-filter on cached corner columns; the
		// ST_Intersects refinement below uses the R-Tree.
		w, s, e, n := req.BBox[0], req.BBox[1], req.BBox[2], req.BBox[3]
		where = append(where,
			"items.bbox_xmin <= ? AND items.bbox_xmax >= ? AND items.bbox_ymin <= ? AND items.bbox_ymax >= ?")
		args = append(args, e, w, n, s)
		if wkt, ok := bboxToWKT(req.BBox); ok {
			where = append(where, "(items.geom IS NULL OR ST_Intersects(items.geom, GeomFromText(?, 4326)) = 1)")
			args = append(args, wkt)
		}
	}
	if req.Intersects != nil {
		if wkt, ok := geomToWKT(req.Intersects); ok {
			where = append(where, "ST_Intersects(items.geom, GeomFromText(?, 4326)) = 1")
			args = append(args, wkt)
		} else if bb, ok := req.Intersects.BBox(); ok {
			// WKT fallback for shapes geomToWKT doesn't render: bbox
			// approximation, matching the inmem oracle's behavior.
			where = append(where,
				"items.bbox_xmin <= ? AND items.bbox_xmax >= ? AND items.bbox_ymin <= ? AND items.bbox_ymax >= ?")
			args = append(args, bb[2], bb[0], bb[3], bb[1])
		}
	}
	if req.Datetime != nil {
		if req.Datetime.Start != nil {
			s := req.Datetime.Start.UTC().Format(time.RFC3339)
			where = append(where, "(items.datetime >= ? OR items.end_dt >= ?)")
			args = append(args, s, s)
		}
		if req.Datetime.End != nil {
			e := req.Datetime.End.UTC().Format(time.RFC3339)
			where = append(where, "(items.datetime <= ? OR items.start_dt <= ?)")
			args = append(args, e, e)
		}
	}
	if len(req.Query) > 0 {
		for field, pred := range req.Query {
			frag, qargs := queryToSQL(field, pred)
			if frag != "" {
				where = append(where, frag)
				args = append(args, qargs...)
			}
		}
	}
	if req.Filter != nil {
		frag, fargs, err := translateFilter(req.Filter)
		if err != nil {
			return nil, err
		}
		where = append(where, "("+frag+")")
		args = append(args, fargs...)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 10000 {
		limit = 10000
	}

	orderClause, sortBys, err := buildOrder(req.SortBy)
	if err != nil {
		return nil, err
	}

	cur, err := decodeSearchToken(req.Token)
	if err != nil {
		return nil, fmt.Errorf("spatialite: bad token: %w", repository.ErrInvalidInput)
	}
	if cur != nil {
		ksFrag, ksArgs := keysetWhere(sortBys, *cur)
		if ksFrag != "" {
			where = append(where, ksFrag)
			args = append(args, ksArgs...)
		}
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	// Fetch limit+1 to detect the next page without a separate count.
	rowsSQL := "SELECT items.id, items.collection_id, items.datetime, items.body FROM items" +
		whereSQL + " " + orderClause + " LIMIT ?"
	rowArgs := append(append([]any{}, args...), limit+1)

	rows, err := r.db.QueryContext(ctx, rowsSQL, rowArgs...)
	if err != nil {
		return nil, mapSQLiteErr(err, "spatialite: search")
	}
	defer rows.Close()

	type scanned struct {
		id       string
		datetime string
		item     *stac.Item
	}
	scannedRows := make([]scanned, 0, limit+1)
	for rows.Next() {
		var (
			id, collection string
			dt             *string
			body           []byte
		)
		if err := rows.Scan(&id, &collection, &dt, &body); err != nil {
			return nil, mapSQLiteErr(err, "spatialite: scan")
		}
		var it stac.Item
		if err := json.Unmarshal(body, &it); err != nil {
			return nil, fmt.Errorf("spatialite: decode item: %w", err)
		}
		dtStr := ""
		if dt != nil {
			dtStr = *dt
		}
		scannedRows = append(scannedRows, scanned{id: id, datetime: dtStr, item: &it})
	}
	if err := rows.Err(); err != nil {
		return nil, mapSQLiteErr(err, "spatialite: iter")
	}

	var matched int64
	countSQL := "SELECT COUNT(*) FROM items" + whereSQL
	if err := r.db.QueryRowContext(ctx, countSQL, args...).Scan(&matched); err != nil {
		return nil, mapSQLiteErr(err, "spatialite: count")
	}

	page := &repository.Page[*stac.Item]{Matched: &matched}
	if len(scannedRows) > limit {
		page.Items = make([]*stac.Item, limit)
		for i := 0; i < limit; i++ {
			page.Items[i] = scannedRows[i].item
		}
		last := scannedRows[limit-1]
		page.NextToken = encodeSearchToken(searchCursor{Datetime: last.datetime, ID: last.id})
	} else {
		page.Items = make([]*stac.Item, len(scannedRows))
		for i, sr := range scannedRows {
			page.Items[i] = sr.item
		}
	}
	return page, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// queryToSQL emits SQL for one Query-extension predicate. Returns
// ("", nil) when the predicate is empty.
func queryToSQL(field string, p repository.Predicate) (string, []any) {
	col := mapColumn(field)
	parts := []string{}
	args := []any{}
	if p.Eq != nil {
		parts = append(parts, col+" = ?")
		args = append(args, p.Eq)
	}
	if p.Neq != nil {
		parts = append(parts, col+" IS NOT ?")
		args = append(args, p.Neq)
	}
	if p.Lt != nil {
		parts = append(parts, col+" < ?")
		args = append(args, p.Lt)
	}
	if p.Lte != nil {
		parts = append(parts, col+" <= ?")
		args = append(args, p.Lte)
	}
	if p.Gt != nil {
		parts = append(parts, col+" > ?")
		args = append(args, p.Gt)
	}
	if p.Gte != nil {
		parts = append(parts, col+" >= ?")
		args = append(args, p.Gte)
	}
	if len(p.In) > 0 {
		parts = append(parts, col+" IN ("+placeholders(len(p.In))+")")
		args = append(args, p.In...)
	}
	if p.StartsWith != "" {
		parts = append(parts, col+" LIKE ?")
		args = append(args, escapeLike(p.StartsWith)+"%")
	}
	if p.EndsWith != "" {
		parts = append(parts, col+" LIKE ?")
		args = append(args, "%"+escapeLike(p.EndsWith))
	}
	if p.Contains != "" {
		parts = append(parts, col+" LIKE ?")
		args = append(args, "%"+escapeLike(p.Contains)+"%")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

func escapeLike(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return r.Replace(s)
}

// buildOrder returns ("ORDER BY ...", normalizedSortBys). The returned
// sort list always ends in `id ASC` so the keyset cursor is unique.
// Default sort matches the opensearch backend: datetime DESC, id ASC.
func buildOrder(in []repository.SortClause) (string, []repository.SortClause, error) {
	if len(in) == 0 {
		return "ORDER BY items.datetime DESC, items.id ASC",
			[]repository.SortClause{
				{Field: "datetime", Direction: repository.SortDesc},
				{Field: "id", Direction: repository.SortAsc},
			}, nil
	}
	parts := make([]string, 0, len(in)+1)
	idTiebreak := false
	out := make([]repository.SortClause, 0, len(in)+1)
	for _, c := range in {
		dir := "ASC"
		if c.Direction == repository.SortDesc {
			dir = "DESC"
		}
		parts = append(parts, mapColumn(c.Field)+" "+dir)
		out = append(out, c)
		if c.Field == "id" {
			idTiebreak = true
		}
	}
	if !idTiebreak {
		parts = append(parts, "items.id ASC")
		out = append(out, repository.SortClause{Field: "id", Direction: repository.SortAsc})
	}
	return "ORDER BY " + strings.Join(parts, ", "), out, nil
}

// keysetWhere emits the keyset predicate that resumes after `cur`. We
// hand-code the leading-key + id-tiebreak case (the only one we
// actually emit) and fall back to `id > cur.id` for unusual sorts.
func keysetWhere(sortBys []repository.SortClause, cur searchCursor) (string, []any) {
	if len(sortBys) == 0 {
		return "", nil
	}
	first := sortBys[0]
	switch first.Field {
	case "datetime":
		if first.Direction == repository.SortDesc {
			return "(items.datetime < ? OR (items.datetime IS ? AND items.id > ?))",
				[]any{cur.Datetime, cur.Datetime, cur.ID}
		}
		return "(items.datetime > ? OR (items.datetime IS ? AND items.id > ?))",
			[]any{cur.Datetime, cur.Datetime, cur.ID}
	case "id":
		if first.Direction == repository.SortDesc {
			return "items.id < ?", []any{cur.ID}
		}
		return "items.id > ?", []any{cur.ID}
	}
	return "items.id > ?", []any{cur.ID}
}
