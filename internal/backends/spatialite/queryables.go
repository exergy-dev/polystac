//go:build cgo && spatialite

package spatialite

import (
	"context"
	"fmt"

	"github.com/example/polystac/pkg/repository"
)

func (r *Repo) Queryables(ctx context.Context, collectionID string) (*repository.QueryablesDocument, error) {
	props := map[string]any{
		"id":         map[string]any{"type": "string"},
		"collection": map[string]any{"type": "string"},
		"datetime":   map[string]any{"type": "string", "format": "date-time"},
	}

	var (
		query string
		args  []any
	)
	if collectionID != "" {
		var n int
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections WHERE id = ?`, collectionID).Scan(&n); err != nil {
			return nil, mapSQLiteErr(err, "spatialite: queryables_probe")
		}
		if n == 0 {
			return nil, fmt.Errorf("collection %q: %w", collectionID, repository.ErrNotFound)
		}
		query = `SELECT DISTINCT je.key FROM items, json_each(items.properties) je WHERE items.collection_id = ?`
		args = []any{collectionID}
	} else {
		query = `SELECT DISTINCT je.key FROM items, json_each(items.properties) je`
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapSQLiteErr(err, "spatialite: queryables")
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, mapSQLiteErr(err, "spatialite: queryables_scan")
		}
		if _, exists := props[k]; exists {
			continue
		}
		props[k] = map[string]any{}
	}
	if err := rows.Err(); err != nil {
		return nil, mapSQLiteErr(err, "spatialite: queryables_iter")
	}

	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2019-09/schema",
		"$id":         fmt.Sprintf("/collections/%s/queryables", collectionID),
		"type":        "object",
		"title":       "Queryables for " + collectionID,
		"properties":  props,
		"description": "queryable properties (spatialite backend)",
	}
	return &repository.QueryablesDocument{Schema: schema}, nil
}
