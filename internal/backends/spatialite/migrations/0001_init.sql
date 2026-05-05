-- Schema version tracking. Bumped together with the migration runner.
CREATE TABLE IF NOT EXISTS polystac_schema (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Collections: one row per STAC Collection. body is the canonical JSON
-- emitted by pkg/stac.Collection.MarshalJSON.
CREATE TABLE IF NOT EXISTS collections (
    id         TEXT PRIMARY KEY NOT NULL,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Items: top-level columns mirror indexable STAC fields; full payload
-- in `body`. The spatial GEOMETRY column on `items.geom` is added by
-- ensureGeometryAndIndex in migrate.go after mod_spatialite loads
-- (AddGeometryColumn / CreateSpatialIndex require the extension).
CREATE TABLE IF NOT EXISTS items (
    id            TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    datetime      TEXT,
    start_dt      TEXT,
    end_dt        TEXT,
    properties    TEXT NOT NULL DEFAULT '{}',
    body          TEXT NOT NULL,
    bbox_xmin     REAL,
    bbox_ymin     REAL,
    bbox_xmax     REAL,
    bbox_ymax     REAL,
    PRIMARY KEY (collection_id, id),
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_items_datetime         ON items(datetime);
CREATE INDEX IF NOT EXISTS idx_items_collection_dt_id ON items(collection_id, datetime DESC, id);
CREATE INDEX IF NOT EXISTS idx_items_id               ON items(id);
