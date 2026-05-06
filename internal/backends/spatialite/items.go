//go:build cgo && spatialite

package spatialite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

func (r *Repo) GetItem(ctx context.Context, collectionID, itemID string) (*stac.Item, error) {
	var body []byte
	err := r.db.QueryRowContext(ctx,
		`SELECT body FROM items WHERE collection_id = ? AND id = ?`,
		collectionID, itemID,
	).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		// Distinguish "collection missing" from "item missing" so the log
		// reason is precise; service layer renders the same 404 either way.
		var cn int
		_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections WHERE id = ?`, collectionID).Scan(&cn)
		if cn == 0 {
			return nil, fmt.Errorf("collection %q: %w", collectionID, repository.ErrNotFound)
		}
		return nil, fmt.Errorf("item %q in %q: %w", itemID, collectionID, repository.ErrNotFound)
	}
	if err != nil {
		return nil, mapSQLiteErr(err, "spatialite: get_item")
	}
	var it stac.Item
	if err := json.Unmarshal(body, &it); err != nil {
		return nil, fmt.Errorf("spatialite: decode item: %w", err)
	}
	return &it, nil
}

func (r *Repo) UpsertItem(ctx context.Context, item *stac.Item) error {
	if item == nil || item.ID == "" {
		return fmt.Errorf("spatialite: item.id required: %w", repository.ErrInvalidInput)
	}
	if item.Collection == "" {
		return fmt.Errorf("spatialite: item.collection required: %w", repository.ErrInvalidInput)
	}
	row, err := buildItemRow(item)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, upsertSQLFor(row), row.execArgs()...)
	return mapSQLiteErr(err, "spatialite: upsert_item "+item.ID)
}

func (r *Repo) DeleteItem(ctx context.Context, collectionID, itemID string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM items WHERE collection_id = ? AND id = ?`,
		collectionID, itemID,
	)
	if err != nil {
		return mapSQLiteErr(err, "spatialite: delete_item")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return mapSQLiteErr(err, "spatialite: delete_item")
	}
	if n == 0 {
		return fmt.Errorf("item %q in %q: %w", itemID, collectionID, repository.ErrNotFound)
	}
	return nil
}

// BulkUpsertItems chunks rows into per-tx batches so writer-fsync cost
// is amortized across many items.
func (r *Repo) BulkUpsertItems(ctx context.Context, items iter.Seq2[*stac.Item, error]) (*repository.BulkResult, error) {
	const chunk = 500
	res := &repository.BulkResult{Errors: map[string]error{}}

	flush := func(rows []itemRow) error {
		if len(rows) == 0 {
			return nil
		}
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for _, row := range rows {
			_, err := tx.ExecContext(ctx, upsertSQLFor(row), row.execArgs()...)
			if err != nil {
				res.Errors[row.id] = mapSQLiteErr(err, "upsert_item "+row.id)
				res.Failed++
				continue
			}
			res.Succeeded++
		}
		return tx.Commit()
	}

	batch := make([]itemRow, 0, chunk)
	for item, err := range items {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if err != nil {
			id := "<unknown>"
			if item != nil {
				id = item.ID
			}
			res.Errors[id] = err
			res.Failed++
			continue
		}
		row, berr := buildItemRow(item)
		if berr != nil {
			res.Errors[item.ID] = berr
			res.Failed++
			continue
		}
		batch = append(batch, row)
		if len(batch) >= chunk {
			if err := flush(batch); err != nil {
				return res, err
			}
			batch = batch[:0]
		}
	}
	if err := flush(batch); err != nil {
		return res, err
	}
	return res, nil
}

type itemRow struct {
	id, collection           string
	datetime, startDt, endDt sql.NullString
	properties               string
	body                     string
	bboxMin, bboxMax         [2]float64
	hasBBox                  bool
	geomWKT                  string
}

// Two pre-baked upsert statements: one with the GEOMETRY column, one
// without. SpatiaLite's GeomFromText errors on a NULL WKT, so callers
// that lack a renderable geometry omit the column entirely.
const (
	upsertItemSQLNoGeom = `INSERT INTO items (
            id, collection_id, datetime, start_dt, end_dt, properties, body,
            bbox_xmin, bbox_ymin, bbox_xmax, bbox_ymax
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(collection_id, id) DO UPDATE SET
            datetime = excluded.datetime,
            start_dt = excluded.start_dt,
            end_dt = excluded.end_dt,
            properties = excluded.properties,
            body = excluded.body,
            bbox_xmin = excluded.bbox_xmin,
            bbox_ymin = excluded.bbox_ymin,
            bbox_xmax = excluded.bbox_xmax,
            bbox_ymax = excluded.bbox_ymax`

	upsertItemSQLWithGeom = `INSERT INTO items (
            id, collection_id, datetime, start_dt, end_dt, properties, body,
            bbox_xmin, bbox_ymin, bbox_xmax, bbox_ymax, geom
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, GeomFromText(?, 4326))
        ON CONFLICT(collection_id, id) DO UPDATE SET
            datetime = excluded.datetime,
            start_dt = excluded.start_dt,
            end_dt = excluded.end_dt,
            properties = excluded.properties,
            body = excluded.body,
            bbox_xmin = excluded.bbox_xmin,
            bbox_ymin = excluded.bbox_ymin,
            bbox_xmax = excluded.bbox_xmax,
            bbox_ymax = excluded.bbox_ymax,
            geom = excluded.geom`
)

func upsertSQLFor(row itemRow) string {
	if row.geomWKT != "" {
		return upsertItemSQLWithGeom
	}
	return upsertItemSQLNoGeom
}

func (row itemRow) execArgs() []any {
	args := []any{
		row.id, row.collection,
		row.datetime, row.startDt, row.endDt,
		row.properties, row.body,
		nullableFloat(row.bboxMin[0], row.hasBBox),
		nullableFloat(row.bboxMin[1], row.hasBBox),
		nullableFloat(row.bboxMax[0], row.hasBBox),
		nullableFloat(row.bboxMax[1], row.hasBBox),
	}
	if row.geomWKT != "" {
		args = append(args, row.geomWKT)
	}
	return args
}

func nullableFloat(v float64, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

func buildItemRow(it *stac.Item) (itemRow, error) {
	body, err := json.Marshal(it)
	if err != nil {
		return itemRow{}, fmt.Errorf("spatialite: encode item: %w", err)
	}
	props := it.Properties
	if props == nil {
		props = stac.ItemProperties{}
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return itemRow{}, fmt.Errorf("spatialite: encode properties: %w", err)
	}
	row := itemRow{
		id:         it.ID,
		collection: it.Collection,
		properties: string(propsJSON),
		body:       string(body),
	}
	row.datetime = pickDatetime(props, "datetime")
	row.startDt = pickDatetime(props, "start_datetime")
	row.endDt = pickDatetime(props, "end_datetime")

	if bb, ok := itemBBox(it); ok {
		row.hasBBox = true
		row.bboxMin = [2]float64{bb[0], bb[1]}
		row.bboxMax = [2]float64{bb[2], bb[3]}
	}
	if wkt, ok := geomToWKT(it.Geometry); ok {
		row.geomWKT = wkt
	}
	return row, nil
}

// pickDatetime returns (RFC3339-UTC, valid) for a datetime-like property.
// Unparseable strings are preserved verbatim so they still sort lexically.
func pickDatetime(props stac.ItemProperties, key string) sql.NullString {
	v, ok := props[key]
	if !ok {
		return sql.NullString{}
	}
	s, ok := v.(string)
	if !ok {
		return sql.NullString{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return sql.NullString{String: s, Valid: true}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}
