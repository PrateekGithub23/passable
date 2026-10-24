package api

import (
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type placeResult struct {
	Name  string     `json:"name"`
	Ref   string     `json:"ref,omitempty"`
	Kind  string     `json:"kind"`
	Point [2]float64 `json:"point"` // [lon, lat]
	Score float64    `json:"score"`
}

// handlePlaces searches named places. An exact building code (ref, e.g. "DC") ranks first,
// then word-prefix matches ("dav" -> "Davis Centre"), then trigram word similarity for typos
// ("dana portr"). Duplicate OSM objects for one place (same name and ref within ~100 m) collapse.
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		writeError(w, http.StatusBadRequest, "q must be 2-100 characters")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT name, ref, kind, lon, lat, score FROM (
			SELECT DISTINCT ON (name, coalesce(ref, ''), ST_SnapToGrid(geom, 0.001))
			       name, coalesce(ref, '') AS ref, kind, ST_X(geom) AS lon, ST_Y(geom) AS lat,
			       CASE
			         WHEN lower(ref) = $1 THEN 1.0
			         WHEN search ~ ('\m' || $2) THEN 0.9
			         ELSE word_similarity($1, search)
			       END AS score
			FROM places
			WHERE lower(ref) = $1 OR search ~ ('\m' || $2) OR $1 <% search
			ORDER BY name, coalesce(ref, ''), ST_SnapToGrid(geom, 0.001), id
		) m
		ORDER BY score DESC, length(name), name
		LIMIT 10`, q, regexp.QuoteMeta(q))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	results, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (placeResult, error) {
		var p placeResult
		err := row.Scan(&p.Name, &p.Ref, &p.Kind, &p.Point[0], &p.Point[1], &p.Score)
		return p, err
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	if results == nil {
		results = []placeResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
