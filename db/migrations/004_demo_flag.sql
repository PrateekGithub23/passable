-- +goose Up
-- Reports created by the demo simulator (cmd/simulator) are flagged so the UI can label them
-- "Demo" and nobody mistakes simulated hazards for real ones.
ALTER TABLE hazard_reports ADD COLUMN is_demo BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE hazard_reports DROP COLUMN is_demo;
