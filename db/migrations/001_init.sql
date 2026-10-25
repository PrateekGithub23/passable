-- +goose Up
-- Base graph: OSM nodes and the edges between them (filled by cmd/importer).
-- IF NOT EXISTS everywhere: this schema was first applied by Docker init scripts, before
-- goose managed migrations. Being idempotent lets goose adopt an existing database without
-- dropping it.
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS nodes (
  id   BIGINT PRIMARY KEY,                       -- OSM node id
  geom geometry(Point, 4326) NOT NULL
);
CREATE INDEX IF NOT EXISTS nodes_geom_idx ON nodes USING GIST (geom);

CREATE TABLE IF NOT EXISTS edges (               -- undirected for MVP
  id          BIGSERIAL PRIMARY KEY,
  osm_way_id  BIGINT NOT NULL,
  from_node   BIGINT NOT NULL REFERENCES nodes(id),
  to_node     BIGINT NOT NULL REFERENCES nodes(id),
  length_m    DOUBLE PRECISION NOT NULL,
  geom        geometry(LineString, 4326) NOT NULL,
  highway     TEXT NOT NULL,
  footway     TEXT, surface TEXT, smoothness TEXT, incline TEXT,
  width_m     DOUBLE PRECISION,
  wheelchair  TEXT, kerb_start TEXT, kerb_end TEXT,
  has_steps   BOOLEAN NOT NULL DEFAULT false,
  is_crossing BOOLEAN NOT NULL DEFAULT false,
  tags        JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS edges_geom_idx ON edges USING GIST (geom);   -- KNN snapping (<->)
CREATE INDEX IF NOT EXISTS edges_from_idx ON edges (from_node);
CREATE INDEX IF NOT EXISTS edges_to_idx   ON edges (to_node);

-- +goose Down
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS nodes;
