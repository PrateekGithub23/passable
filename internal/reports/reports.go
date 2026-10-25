// Package reports persists hazard reports in Postgres and keeps the in-memory overlay in sync.
//
// Postgres is the source of truth. Every change to a report happens in a transaction that
// also appends a row to hazard_events; after the commit, the same change is applied to the
// overlay as an Event with that row's seq.
//
// Ordering guarantee: every write transaction first takes one global advisory lock, so
// hazard writes run one at a time across ALL API instances. Then seq order equals commit
// order, which is what makes "apply events in seq order, resync on a gap" correct. Hazard
// writes are rare (people tapping "report"), so serializing them costs nothing noticeable.
package reports

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Arbitrary constant id for pg_advisory_xact_lock; any number works as long as every hazard
// writer uses the same one.
const writeLockKey = 0x50415353 // "PASS"

// Errors the API maps to HTTP status codes.
var (
	ErrNotFound       = errors.New("report not found")                      // 404
	ErrNotLive        = errors.New("report is already resolved or expired") // 409
	ErrInvalidSession = errors.New("unknown or missing session")            // 401
)

// Report is one hazard report as returned by the API.
type Report struct {
	ID           int64           `json:"id"`
	EdgeID       int64           `json:"edge_id"`
	Type         hazard.Type     `json:"type"`
	Severity     hazard.Severity `json:"severity"`
	Status       hazard.Status   `json:"status"`
	Note         string          `json:"note,omitempty"`
	Point        [2]float64      `json:"point"` // [lon, lat] where it was reported
	ConfirmCount int             `json:"confirm_count"`
	ResolveCount int             `json:"resolve_count"`
	CreatedAt    time.Time       `json:"created_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
	Demo         bool            `json:"demo"` // created by the demo simulator; the UI labels it
}

// reportCols is the SELECT/RETURNING column list that scanReport reads.
const reportCols = `id, edge_id, type, severity, status, coalesce(note, ''), ST_X(geom), ST_Y(geom),
	confirm_count, resolve_count, created_at, expires_at, is_demo`

func scanReport(row pgx.Row) (Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.EdgeID, &r.Type, &r.Severity, &r.Status, &r.Note, &r.Point[0], &r.Point[1],
		&r.ConfirmCount, &r.ResolveCount, &r.CreatedAt, &r.ExpiresAt, &r.Demo)
	return r, err
}

// Change is one committed hazard event: what is published to other instances (Redis) and
// pushed to browsers (WebSocket). It carries the report's full new state.
type Change struct {
	Seq    int64  `json:"seq"`
	Kind   string `json:"kind"` // created | updated | resolved | expired
	Report Report `json:"report"`
}

// Service owns report persistence and overlay updates.
type Service struct {
	db      *pgxpool.Pool
	g       *graph.Graph
	overlay *overlay.Holder
	now     func() time.Time // injectable clock

	// OnCommit, if set, is called once for every committed change, after it has been applied
	// to this instance's overlay. main wires it to "publish to Redis" (or, without Redis, to
	// "notify local WebSocket clients"). Set it before serving traffic.
	OnCommit func(Change)
}

// New creates the service. Call Resync before serving traffic.
func New(db *pgxpool.Pool, g *graph.Graph, h *overlay.Holder) *Service {
	return &Service{db: db, g: g, overlay: h, now: time.Now}
}

// NewSession issues a random anonymous session id (128 bits, hex).
func (s *Service) NewSession(ctx context.Context) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	_, err := s.db.Exec(ctx, `INSERT INTO sessions (id) VALUES ($1)`, id)
	return id, err
}

// checkSession verifies the id exists and refreshes last_seen_at, inside the caller's tx.
func checkSession(ctx context.Context, tx pgx.Tx, id string) error {
	if id == "" {
		return ErrInvalidSession
	}
	tag, err := tx.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidSession
	}
	return nil
}

// CreateInput is a validated new report. The API layer has already snapped the point to Edge.
type CreateInput struct {
	Edge     int32 // edge index
	Type     hazard.Type
	Severity hazard.Severity
	Note     string
	Lat, Lon float64
	Demo     bool
}

// Create stores a new report and applies it to the overlay.
func (s *Service) Create(ctx context.Context, sessionID string, in CreateInput) (Report, error) {
	return s.write(ctx, func(tx pgx.Tx) (Report, string, error) {
		if err := checkSession(ctx, tx, sessionID); err != nil {
			return Report{}, "", err
		}
		now := s.now()
		var note *string // empty note -> NULL
		if in.Note != "" {
			note = &in.Note
		}
		r, err := scanReport(tx.QueryRow(ctx, `
			INSERT INTO hazard_reports (edge_id, type, severity, note, geom, reporter_session_id, created_at, updated_at, expires_at, is_demo)
			VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint($5, $6), 4326), $7, $8, $8, $9, $10)
			RETURNING `+reportCols,
			s.g.Edges[in.Edge].ID, in.Type, in.Severity, note, in.Lon, in.Lat, sessionID, now, now.Add(hazard.TTL(in.Type)), in.Demo))
		return r, "created", err
	})
}

// VoteKind is "confirm" (still there) or "resolve" (gone).
type VoteKind string

const (
	Confirm VoteKind = "confirm"
	Resolve VoteKind = "resolve"
)

// Vote records (or changes) a session's vote on a report and moves the report through its
// lifecycle (hazard.Next).
func (s *Service) Vote(ctx context.Context, sessionID string, reportID int64, kind VoteKind) (Report, error) {
	return s.write(ctx, func(tx pgx.Tx) (Report, string, error) {
		if err := checkSession(ctx, tx, sessionID); err != nil {
			return Report{}, "", err
		}
		// FOR UPDATE locks the row until commit. (The advisory lock already serializes hazard
		// writes; this also protects against any other future writer of this table.)
		var reporter string
		var status hazard.Status
		var typ hazard.Type
		var expires time.Time
		err := tx.QueryRow(ctx, `
			SELECT reporter_session_id, status, type, expires_at FROM hazard_reports WHERE id = $1 FOR UPDATE`,
			reportID).Scan(&reporter, &status, &typ, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return Report{}, "", ErrNotFound
		}
		if err != nil {
			return Report{}, "", err
		}
		if !status.Live() {
			return Report{}, "", ErrNotLive
		}
		// A reporter "confirming" their own report adds nothing; return it unchanged.
		if kind == Confirm && sessionID == reporter {
			r, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportCols+` FROM hazard_reports WHERE id = $1`, reportID))
			return r, "", err // "" = no event: nothing changed
		}

		// One vote per (report, session); voting again with a different kind replaces the
		// earlier vote. Repeating the same vote matches the WHERE false, affects 0 rows, and is
		// a no-op: no event, so the graph version (and route cache) are left alone.
		tag, err := tx.Exec(ctx, `
			INSERT INTO report_votes (report_id, session_id, kind) VALUES ($1, $2, $3)
			ON CONFLICT (report_id, session_id) DO UPDATE SET kind = EXCLUDED.kind, voted_at = now()
			WHERE report_votes.kind <> EXCLUDED.kind`,
			reportID, sessionID, kind)
		if err != nil {
			return Report{}, "", err
		}
		if tag.RowsAffected() == 0 {
			r, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportCols+` FROM hazard_reports WHERE id = $1`, reportID))
			return r, "", err
		}

		// Recount from the votes table (the source of truth for counts).
		var t hazard.Tally
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE kind = 'confirm'),
			       count(*) FILTER (WHERE kind = 'resolve'),
			       coalesce(bool_or(session_id = $2 AND kind = 'resolve'), false)
			FROM report_votes WHERE report_id = $1`, reportID, reporter).Scan(&t.Confirms, &t.Resolves, &t.ReporterResolved); err != nil {
			return Report{}, "", err
		}

		now := s.now()
		if kind == Confirm {
			expires = hazard.ExtendExpiry(typ, expires, now) // still there -> keep it alive longer
		}
		next := hazard.Next(status, t, now, expires)
		r, err := scanReport(tx.QueryRow(ctx, `
			UPDATE hazard_reports
			SET status = $2, confirm_count = $3, resolve_count = $4, expires_at = $5, updated_at = $6
			WHERE id = $1 RETURNING `+reportCols,
			reportID, next, t.Confirms, t.Resolves, expires, now))
		event := "updated"
		if next == hazard.Resolved {
			event = "resolved"
		}
		return r, event, err
	})
}

// write runs fn in a transaction holding the global hazard write lock, appends a
// hazard_events row (unless fn returns event ""), commits, then applies the event to the overlay.
func (s *Service) write(ctx context.Context, fn func(tx pgx.Tx) (Report, string, error)) (Report, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx) // no-op after Commit
	// Global write lock, released automatically at commit/rollback ("xact" = transaction-scoped).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writeLockKey); err != nil {
		return Report{}, err
	}
	r, event, err := fn(tx)
	if err != nil {
		return Report{}, err
	}
	if event == "" {
		return r, tx.Commit(ctx)
	}
	var seq int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO hazard_events (report_id, kind, edge_ids) VALUES ($1, $2, ARRAY[$3::bigint]) RETURNING seq`,
		r.ID, event, r.EdgeID).Scan(&seq); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	s.committed(ctx, Change{Seq: seq, Kind: event, Report: r})
	return r, nil
}

// committed handles a change this instance just committed: apply it locally first (so the
// writer's very next route request already sees it: read-your-writes), then hand it to
// OnCommit for fan-out.
func (s *Service) committed(ctx context.Context, ch Change) {
	s.ApplyChange(ctx, ch)
	if s.OnCommit != nil {
		s.OnCommit(ch)
	}
}

// ApplyChange applies a change (local, or received from another instance) to the overlay.
// Safe to call more than once per change: already-applied events are ignored.
func (s *Service) ApplyChange(ctx context.Context, ch Change) {
	s.apply(ctx, ch.Seq, ch.Report)
}

// apply pushes one committed change into the overlay, resyncing from Postgres on a gap.
func (s *Service) apply(ctx context.Context, seq int64, r Report) {
	ei, ok := s.g.EdgeByID(r.EdgeID)
	if !ok {
		log.Printf("reports: edge %d of report %d not in graph; resyncing", r.EdgeID, r.ID)
		s.resyncOrLog(ctx)
		return
	}
	ev := overlay.Event{Seq: seq, Hazard: overlay.Hazard{ID: r.ID, Edge: ei, Type: r.Type, Severity: r.Severity, Status: r.Status}}
	if err := s.overlay.Apply(ev); err != nil {
		// ErrGap: an event between our snapshot and seq hasn't been applied here (e.g. a
		// concurrent write whose apply hasn't run yet). Reloading from the DB is always correct.
		s.resyncOrLog(ctx)
	}
}

func (s *Service) resyncOrLog(ctx context.Context) {
	if err := s.Resync(ctx); err != nil {
		log.Printf("reports: resync failed: %v", err)
	}
}

// Resync rebuilds the overlay from Postgres: every live report, plus the latest event seq.
// Both are read in one REPEATABLE READ transaction, so they describe the same moment.
func (s *Service) Resync(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM hazard_events`).Scan(&seq); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id, edge_id, type, severity, status FROM hazard_reports WHERE status IN ('active', 'confirmed')`)
	if err != nil {
		return err
	}
	var live []overlay.Hazard
	for rows.Next() {
		var h overlay.Hazard
		var edgeID int64
		if err := rows.Scan(&h.ID, &edgeID, &h.Type, &h.Severity, &h.Status); err != nil {
			return err
		}
		ei, ok := s.g.EdgeByID(edgeID)
		if !ok {
			continue // edge no longer in the graph (re-import); the report can't affect routing
		}
		h.Edge = ei
		live = append(live, h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.overlay.Replace(overlay.Build(seq, live))
	return nil
}

// ExpireDue marks every live report past its deadline as expired, logging one event each,
// and applies them to the overlay. Returns how many expired.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, writeLockKey); err != nil {
		return 0, err
	}
	// One statement: expire the rows and log an event per row. ORDER BY id makes the seq
	// assignment deterministic.
	rows, err := tx.Query(ctx, `
		WITH expired AS (
			UPDATE hazard_reports SET status = 'expired', updated_at = $1
			WHERE status IN ('active', 'confirmed') AND expires_at <= $1
			RETURNING id, edge_id, type, severity
		)
		INSERT INTO hazard_events (report_id, kind, edge_ids)
		SELECT id, 'expired', ARRAY[edge_id] FROM expired ORDER BY id
		RETURNING seq, report_id, edge_ids[1]`, s.now())
	if err != nil {
		return 0, err
	}
	type expiredEvent struct{ seq, id, edgeID int64 }
	var evs []expiredEvent
	for rows.Next() {
		var e expiredEvent
		if err := rows.Scan(&e.seq, &e.id, &e.edgeID); err != nil {
			return 0, err
		}
		evs = append(evs, e)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	// RETURNING order isn't guaranteed to follow seq; the overlay needs seq order.
	slices.SortFunc(evs, func(a, b expiredEvent) int { return cmp.Compare(a.seq, b.seq) })
	for _, e := range evs {
		// Read the full row back so browsers get a complete report (point, type, ...).
		// Falls back to the minimal state if the read fails: the overlay only needs
		// id + edge + "not live" to remove the hazard.
		r, err := scanReport(s.db.QueryRow(ctx, `SELECT `+reportCols+` FROM hazard_reports WHERE id = $1`, e.id))
		if err != nil {
			r = Report{ID: e.id, EdgeID: e.edgeID, Status: hazard.Expired}
		}
		s.committed(ctx, Change{Seq: e.seq, Kind: "expired", Report: r})
	}
	return len(evs), nil
}

// RunExpiry calls ExpireDue (and a catch-up check) every interval until ctx is cancelled. Several instances can run
// it at once: the advisory lock plus "WHERE status IN (live)" means each report expires once.
func (s *Service) RunExpiry(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.ExpireDue(ctx); err != nil {
			if ctx.Err() == nil {
				log.Printf("reports: expiry: %v", err)
			}
		} else if n > 0 {
			log.Printf("reports: expired %d report(s)", n)
		}
		s.catchUp(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// catchUp resyncs if Postgres has events this instance never received. Redis pub/sub drops
// messages while a subscriber is disconnected, and a dropped *last* event would otherwise go
// unnoticed (gap detection only fires when a later event arrives).
func (s *Service) catchUp(ctx context.Context) {
	var latest int64
	if err := s.db.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM hazard_events`).Scan(&latest); err != nil {
		return
	}
	if latest > s.overlay.Load().Seq {
		log.Printf("reports: overlay at %d but log at %d; resyncing", s.overlay.Load().Seq, latest)
		s.resyncOrLog(ctx)
	}
}

// List returns live reports inside a bounding box (west, south, east, north).
func (s *Service) List(ctx context.Context, w, south, e, n float64) ([]Report, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+reportCols+` FROM hazard_reports
		WHERE status IN ('active', 'confirmed') AND geom && ST_MakeEnvelope($1, $2, $3, $4, 4326)
		ORDER BY created_at DESC LIMIT 500`, w, south, e, n)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Report, error) { return scanReport(row) })
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	if out == nil {
		out = []Report{}
	}
	return out, nil
}
