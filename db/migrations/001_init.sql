CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE nodes (
  id   BIGINT PRIMARY KEY,                       -- OSM node id
  geom geometry(Point, 4326) NOT NULL
);
CREATE INDEX nodes_geom_idx ON nodes USING GIST (geom);

CREATE TABLE edges (                             -- undirected for MVP
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
CREATE INDEX edges_geom_idx ON edges USING GIST (geom);
CREATE INDEX edges_from_idx ON edges (from_node);
CREATE INDEX edges_to_idx   ON edges (to_node);
