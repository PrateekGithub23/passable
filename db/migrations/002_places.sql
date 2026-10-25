-- +goose Up
-- Named places for search (buildings, amenities, shops, ...), with UW building codes in ref.
-- Idempotent for the same reason as 001 (adopting a database created by init scripts).
CREATE TABLE IF NOT EXISTS places (
  id       BIGSERIAL PRIMARY KEY,
  osm_type TEXT   NOT NULL,                      -- node | way | relation
  osm_id   BIGINT NOT NULL,
  name     TEXT   NOT NULL,
  ref      TEXT,                                 -- e.g. "DC", "SLC"
  aliases  TEXT,                                 -- short/alt/official/old/local names and old_ref (e.g. "E7"), space-separated
  kind     TEXT   NOT NULL,                      -- e.g. "university", "cafe"
  geom     geometry(Point, 4326) NOT NULL,       -- node location or way/relation center
  -- One lowercased text column to search, kept up to date by Postgres itself.
  search   TEXT GENERATED ALWAYS AS (lower(name || ' ' || coalesce(ref, '') || ' ' || coalesce(aliases, ''))) STORED,
  UNIQUE (osm_type, osm_id)
);
-- Trigram index: makes similarity (<%) and regex (~) searches on `search` fast.
CREATE INDEX IF NOT EXISTS places_search_trgm_idx ON places USING GIN (search gin_trgm_ops);
CREATE INDEX IF NOT EXISTS places_ref_idx ON places (lower(ref));

-- +goose Down
DROP TABLE IF EXISTS places;
