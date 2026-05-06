//go:build cgo && spatialite

package spatialite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

func (r *Repo) GetCollection(ctx context.Context, id string) (*stac.Collection, error) {
	var body []byte
	err := r.db.QueryRowContext(ctx, `SELECT body FROM collections WHERE id = ?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return nil, mapSQLiteErr(err, "spatialite: get_collection")
	}
	var c stac.Collection
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("spatialite: decode collection: %w", err)
	}
	return &c, nil
}

func (r *Repo) ListCollections(ctx context.Context, opts repository.ListCollectionsOptions) (*repository.Page[*stac.Collection], error) {
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	start, err := decodeListToken(opts.Token)
	if err != nil {
		return nil, fmt.Errorf("spatialite: bad token: %w", repository.ErrInvalidInput)
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections`).Scan(&total); err != nil {
		return nil, mapSQLiteErr(err, "spatialite: count_collections")
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT body FROM collections ORDER BY id ASC LIMIT ? OFFSET ?`, limit, start)
	if err != nil {
		return nil, mapSQLiteErr(err, "spatialite: list_collections")
	}
	defer rows.Close()
	out := make([]*stac.Collection, 0, limit)
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, mapSQLiteErr(err, "spatialite: scan_collection")
		}
		var c stac.Collection
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("spatialite: decode collection: %w", err)
		}
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapSQLiteErr(err, "spatialite: iter_collections")
	}

	page := &repository.Page[*stac.Collection]{Items: out, Matched: &total}
	end := start + len(out)
	if int64(end) < total {
		page.NextToken = encodeListToken(end)
	}
	if start > 0 {
		prev := start - limit
		if prev < 0 {
			prev = 0
		}
		page.PrevToken = encodeListToken(prev)
	}
	return page, nil
}

func (r *Repo) UpsertCollection(ctx context.Context, c *stac.Collection) error {
	if c == nil || c.ID == "" {
		return fmt.Errorf("spatialite: collection.id required: %w", repository.ErrInvalidInput)
	}
	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("spatialite: encode collection: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO collections (id, body) VALUES (?, ?)
         ON CONFLICT(id) DO UPDATE SET
            body = excluded.body,
            updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		c.ID, string(body),
	)
	return mapSQLiteErr(err, "spatialite: upsert_collection "+c.ID)
}

// DeleteCollection cascades to items via the FK ON DELETE CASCADE.
func (r *Repo) DeleteCollection(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	if err != nil {
		return mapSQLiteErr(err, "spatialite: delete_collection "+id)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return mapSQLiteErr(err, "spatialite: delete_collection "+id)
	}
	if n == 0 {
		return fmt.Errorf("collection %q: %w", id, repository.ErrNotFound)
	}
	return nil
}
