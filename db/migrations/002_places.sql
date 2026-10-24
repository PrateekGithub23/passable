-- Named places for search (buildings, amenities, shops, ...), with UW building codes in ref.
CREATE TABLE places (
  id       BIGSERIAL PRIMARY KEY,
  osm_type TEXT   NOT NULL,                      -- node | way | relation
  osm_id   BIGINT NOT NULL,
  name     TEXT   NOT NULL,
  ref      TEXT,                                 -- e.g. "DC", "SLC"
  aliases  TEXT,                                 -- short/alt/official/old/local names and old_ref (e.g. "E7"), space-separated
  kind     TEXT   NOT NULL,                      -- e.g. "university", "cafe"
  geom     geometry(Point, 4326) NOT NULL,       -- node location or way/relation center
  search   TEXT GENERATED ALWAYS AS (lower(name || ' ' || coalesce(ref, '') || ' ' || coalesce(aliases, ''))) STORED,
  UNIQUE (osm_type, osm_id)
);
CREATE INDEX places_search_trgm_idx ON places USING GIN (search gin_trgm_ops);
CREATE INDEX places_ref_idx ON places (lower(ref));
