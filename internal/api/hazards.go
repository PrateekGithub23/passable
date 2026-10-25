package api

// Hazard endpoints:
//
//	POST /api/v1/session                  -> {"session_id": "..."}   anonymous id for writes
//	GET  /api/v1/hazards?bbox=w,s,e,n     -> live reports in the box
//	POST /api/v1/hazards                  -> create a report        (X-Session-ID header)
//	POST /api/v1/hazards/{id}/confirm     -> "still there"          (X-Session-ID header)
//	POST /api/v1/hazards/{id}/resolve     -> "it's gone"            (X-Session-ID header)
//	GET  /api/v1/graph/version            -> current graph version (hazard event seq)
//
// Handlers only translate HTTP <-> Go; the rules live in packages hazard and reports.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/reports"
)

// A report must be within this distance of a mapped path; it is attached to the nearest one.
const maxHazardSnapM = 30

// sessionHeader carries the anonymous session id. A header (not a cookie) because the
// frontend and API are on different domains, where cookies need extra cross-site setup.
const sessionHeader = "X-Session-ID"

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	id, err := s.reports.NewSession(r.Context())
	if err != nil {
		log.Printf("new session: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"session_id": id})
}

// handleVersion reports the current graph version, i.e. the last hazard event applied here.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	snap := s.overlay.Load()
	writeJSON(w, http.StatusOK, map[string]any{"version": snap.Seq, "live_hazards": snap.Len()})
}

func (s *Server) handleListHazards(w http.ResponseWriter, r *http.Request) {
	// Default to the whole service area when no bbox is given.
	west, south, east, north := bboxW, bboxS, bboxE, bboxN
	if b := r.URL.Query().Get("bbox"); b != "" {
		parts := strings.Split(b, ",")
		var vals [4]float64
		ok := len(parts) == 4
		for i := 0; ok && i < 4; i++ {
			var err error
			vals[i], err = strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
			ok = err == nil
		}
		if !ok || vals[0] >= vals[2] || vals[1] >= vals[3] {
			writeError(w, http.StatusBadRequest, "bbox must be west,south,east,north")
			return
		}
		west, south, east, north = vals[0], vals[1], vals[2], vals[3]
	}
	list, err := s.reports.List(r.Context(), west, south, east, north)
	if err != nil {
		log.Printf("list hazards: %v", err)
		writeError(w, http.StatusInternalServerError, "could not list hazards")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hazards": list, "graph_version": s.overlay.Load().Seq})
}

// createHazardRequest is the POST /api/v1/hazards body.
type createHazardRequest struct {
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Type     string  `json:"type"`
	Severity string  `json:"severity"` // optional; defaults per type
	Note     string  `json:"note"`     // optional, <= 280 characters
}

func (s *Server) handleCreateHazard(w http.ResponseWriter, r *http.Request) {
	// Cap the body size: a report is a few hundred bytes, so anything large is abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req createHazardRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // typos in field names become errors instead of silent defaults
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate every field before touching the database.
	pt, err := parseLatLng(fmt.Sprintf("%f,%f", req.Lat, req.Lng))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	typ, err := hazard.ParseType(req.Type)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sev, err := hazard.ParseSeverity(req.Severity, typ)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > 280 {
		writeError(w, http.StatusBadRequest, "note must be at most 280 characters")
		return
	}

	// Attach the report to the nearest mapped path (any path: a hazard on steps matters too).
	cands, err := s.candidates(r.Context(), pt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snap failed")
		return
	}
	if len(cands) == 0 || cands[0].distM > maxHazardSnapM {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("no mapped path within %d m; tap closer to the path", maxHazardSnapM))
		return
	}

	rep, err := s.reports.Create(r.Context(), r.Header.Get(sessionHeader), reports.CreateInput{
		Edge: cands[0].edge, Type: typ, Severity: sev, Note: note, Lat: pt.Lat, Lon: pt.Lon,
	})
	if err != nil {
		writeReportError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rep)
}

// handleVote returns the handler for one vote kind (confirm or resolve).
func (s *Server) handleVote(kind reports.VoteKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64) // {id} from the route pattern
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid report id")
			return
		}
		rep, err := s.reports.Vote(r.Context(), r.Header.Get(sessionHeader), id, kind)
		if err != nil {
			writeReportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

// writeReportError maps service errors to HTTP status codes.
func writeReportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, reports.ErrInvalidSession):
		writeError(w, http.StatusUnauthorized, "missing or unknown "+sessionHeader+"; get one from POST /api/v1/session")
	case errors.Is(err, reports.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, reports.ErrNotLive):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("hazard write: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save")
	}
}
