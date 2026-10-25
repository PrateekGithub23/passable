-- +goose Up
-- Crowd-sourced hazard reports and everything around them.

-- Anonymous sessions: a random id the server hands out (POST /api/v1/session). There are no
-- accounts; the id only lets us count one vote per person per report and rate-limit later.
CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,                 -- 32 hex chars (128 random bits)
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per report. Lifecycle (see internal/hazard/state.go):
--   active -> confirmed -> resolved | expired     (resolved/expired are final)
CREATE TABLE hazard_reports (
  id                  BIGSERIAL PRIMARY KEY,
  edge_id             BIGINT NOT NULL REFERENCES edges(id) ON DELETE CASCADE,
  type                TEXT NOT NULL CHECK (type IN ('elevator_out', 'blocked_sidewalk', 'construction', 'ice_snow', 'missing_curb_ramp')),
  severity            TEXT NOT NULL CHECK (severity IN ('minor', 'major')),
  status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'confirmed', 'resolved', 'expired')),
  note                TEXT CHECK (length(note) <= 280),
  geom                geometry(Point, 4326) NOT NULL, -- where the reporter tapped (shown on the map)
  reporter_session_id TEXT NOT NULL REFERENCES sessions(id),
  confirm_count       INT NOT NULL DEFAULT 0,
  resolve_count       INT NOT NULL DEFAULT 0,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at          TIMESTAMPTZ NOT NULL
);
-- "Live" reports are the ones that affect routing; most queries only look at those, so a
-- partial index (only live rows) stays small as resolved/expired history grows.
CREATE INDEX hazard_reports_live_idx ON hazard_reports (expires_at) WHERE status IN ('active', 'confirmed');
CREATE INDEX hazard_reports_geom_idx ON hazard_reports USING GIST (geom);

-- One vote per session per report (the primary key enforces it). Re-voting updates `kind`,
-- so someone who confirmed in the morning can vote "resolved" once it's cleared.
CREATE TABLE report_votes (
  report_id  BIGINT NOT NULL REFERENCES hazard_reports(id) ON DELETE CASCADE,
  session_id TEXT   NOT NULL REFERENCES sessions(id),
  kind       TEXT   NOT NULL CHECK (kind IN ('confirm', 'resolve')),
  voted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (report_id, session_id)
);

-- Append-only event log. seq is the global "graph version": every change to the set of live
-- hazards gets the next seq, and every API instance applies events in seq order.
CREATE TABLE hazard_events (
  seq        BIGSERIAL PRIMARY KEY,
  report_id  BIGINT NOT NULL REFERENCES hazard_reports(id) ON DELETE CASCADE,
  kind       TEXT   NOT NULL CHECK (kind IN ('created', 'updated', 'resolved', 'expired')),
  edge_ids   BIGINT[] NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-request routing stats, for benchmarks (written later; schema from the plan).
CREATE TABLE route_requests (
  id             BIGSERIAL PRIMARY KEY,
  profile        TEXT NOT NULL,
  distance_m     DOUBLE PRECISION,
  duration_ms    DOUBLE PRECISION NOT NULL,
  nodes_expanded INT,
  graph_version  BIGINT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS route_requests;
DROP TABLE IF EXISTS hazard_events;
DROP TABLE IF EXISTS report_votes;
DROP TABLE IF EXISTS hazard_reports;
DROP TABLE IF EXISTS sessions;
