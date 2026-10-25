-- +goose Up
-- Record whether each route request was served from the cache, so the cache hit rate can be
-- computed from real traffic (SELECT avg(cache_hit::int) FROM route_requests ...).
ALTER TABLE route_requests ADD COLUMN cache_hit BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE route_requests ALTER COLUMN nodes_expanded DROP NOT NULL; -- unknown for cache hits
CREATE INDEX route_requests_created_idx ON route_requests (created_at);

-- +goose Down
DROP INDEX IF EXISTS route_requests_created_idx;
ALTER TABLE route_requests DROP COLUMN cache_hit;
