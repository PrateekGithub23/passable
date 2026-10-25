// Command simulator keeps a few realistic, clearly labelled DEMO hazards alive on campus, so the
// live features (markers appearing, confirmations, reroute prompts) can be shown without
// waiting for real reports.
//
//	go run ./cmd/simulator -api http://localhost:8080 -every 45s -max 6
//
// It only uses the public HTTP API, like any other client (so it also works against a
// deployment). Every report it files has "demo": true, which the UI shows as a "Demo" badge,
// and on exit (Ctrl-C) it resolves the demo hazards that are still live.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"
)

// Campus core, where demo hazards are placed (west, south, east, north).
var campus = [4]float64{-80.552, 43.466, -80.536, 43.476}

// Hazard types with how often each is picked and plausible notes.
var kinds = []struct {
	typ    string
	weight int
	notes  []string
}{
	{"ice_snow", 35, []string{"Icy patch by the doors", "Ramp not salted yet", "Black ice on the path"}},
	{"blocked_sidewalk", 25, []string{"Delivery truck parked across the sidewalk", "Fence across the path", "Snowbank over the curb cut"}},
	{"construction", 15, []string{"Path dug up for pipe work", "Construction hoarding, no detour sign"}},
	{"elevator_out", 10, []string{"Elevator out of service", "Lift stuck on the ground floor"}},
	{"missing_curb_ramp", 15, []string{"No curb cut at this crossing", "Curb ramp blocked by a planter"}},
}

type hazard struct {
	ID     int64  `json:"id"`
	Demo   bool   `json:"demo"`
	Status string `json:"status"`
}

type client struct {
	api      string
	sessions []string
	r        *rand.Rand
	verbose  bool
}

func main() {
	api := flag.String("api", "http://localhost:8080", "API base URL")
	every := flag.Duration("every", 45*time.Second, "time between actions")
	maxLive := flag.Int("max", 6, "maximum live demo hazards at once")
	nSessions := flag.Int("sessions", 4, "number of simulated people (sessions)")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed")
	verbose := flag.Bool("v", false, "log every step's state")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c := &client{api: *api, r: rand.New(rand.NewSource(*seed)), verbose: *verbose}
	for i := 0; i < *nSessions; i++ {
		var out struct {
			SessionID string `json:"session_id"`
		}
		for {
			err := c.do("POST", "/api/v1/session", "", nil, &out)
			if err == nil {
				break
			}
			// Rate limited: wait as long as the server asks, then try again.
			he, ok := err.(httpError)
			if !ok || he.status != http.StatusTooManyRequests {
				log.Fatalf("create session: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(he.retryAfter) * time.Second):
			}
		}
		c.sessions = append(c.sessions, out.SessionID)
	}
	log.Printf("simulator: %d sessions, one action every %s, at most %d live demo hazards (Ctrl-C to stop and clean up)", *nSessions, *every, *maxLive)

	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		c.step(*maxLive)
		select {
		case <-ctx.Done():
			c.cleanup()
			return
		case <-t.C:
		}
	}
}

// step does one random action: report a new hazard, confirm one, or resolve one.
func (c *client) step(maxLive int) {
	live := c.liveDemo()
	roll := c.r.Float64()
	if c.verbose {
		log.Printf("step: %d live demo hazard(s), roll %.2f", len(live), roll)
	}
	switch {
	case len(live) < maxLive && (roll < 0.55 || len(live) == 0):
		c.report()
	case roll < 0.85 && len(live) > 0:
		h := live[c.r.Intn(len(live))]
		if err := c.do("POST", fmt.Sprintf("/api/v1/hazards/%d/confirm", h.ID), c.session(), nil, nil); err == nil {
			log.Printf("confirmed #%d", h.ID)
		}
	case len(live) > 0:
		c.resolve(live[c.r.Intn(len(live))].ID)
	}
}

// report files a new demo hazard at a random campus point, retrying points that are too far
// from any path (the API answers 422 for those).
func (c *client) report() {
	k := c.pickKind()
	for attempt := 0; attempt < 8; attempt++ {
		lng := campus[0] + c.r.Float64()*(campus[2]-campus[0])
		lat := campus[1] + c.r.Float64()*(campus[3]-campus[1])
		body, _ := json.Marshal(map[string]any{
			"lat": lat, "lng": lng, "type": k.typ, "note": k.notes[c.r.Intn(len(k.notes))], "demo": true,
		})
		var h hazard
		err := c.do("POST", "/api/v1/hazards", c.session(), body, &h)
		if err == nil {
			log.Printf("reported #%d %s at %.5f,%.5f", h.ID, k.typ, lat, lng)
			return
		}
		if he, ok := err.(httpError); !ok || he.status != http.StatusUnprocessableEntity {
			return // rate limited or server error: already logged; try again next tick
		}
	}
}

// resolve needs two different people saying "it's gone" (or the reporter, whom we don't track).
func (c *client) resolve(id int64) {
	a := c.r.Intn(len(c.sessions))
	b := (a + 1) % len(c.sessions)
	for _, s := range []string{c.sessions[a], c.sessions[b]} {
		var h hazard
		if err := c.do("POST", fmt.Sprintf("/api/v1/hazards/%d/resolve", id), s, nil, &h); err != nil || h.Status == "resolved" {
			break // done (the first voter was the reporter, whose vote alone resolves it) or failed
		}
	}
	log.Printf("resolved #%d", id)
}

// cleanup resolves every live demo hazard so the demo leaves nothing behind.
func (c *client) cleanup() {
	live := c.liveDemo()
	for _, h := range live {
		c.resolve(h.ID)
	}
	log.Printf("cleanup: resolved %d demo hazard(s)", len(live))
}

func (c *client) liveDemo() []hazard {
	var out struct {
		Hazards []hazard `json:"hazards"`
	}
	bbox := fmt.Sprintf("%g,%g,%g,%g", campus[0], campus[1], campus[2], campus[3])
	if err := c.do("GET", "/api/v1/hazards?bbox="+bbox, "", nil, &out); err != nil {
		return nil
	}
	var demo []hazard
	for _, h := range out.Hazards {
		if h.Demo {
			demo = append(demo, h) // never touch real reports
		}
	}
	return demo
}

func (c *client) pickKind() (k struct {
	typ    string
	weight int
	notes  []string
}) {
	total := 0
	for _, k := range kinds {
		total += k.weight
	}
	n := c.r.Intn(total)
	for _, k := range kinds {
		if n < k.weight {
			return k
		}
		n -= k.weight
	}
	return kinds[0]
}

func (c *client) session() string { return c.sessions[c.r.Intn(len(c.sessions))] }

type httpError struct {
	status     int
	msg        string
	retryAfter int // seconds, from the Retry-After header on 429
}

func (e httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.msg) }

// do sends a request; non-2xx responses become httpError (and are logged, except 422).
func (c *client) do(method, path, sid string, body []byte, out any) error {
	req, _ := http.NewRequest(method, c.api+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set("X-Session-ID", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("%s %s: %v", method, path, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if resp.StatusCode == http.StatusTooManyRequests {
			log.Printf("rate limited on %s %s; next try in %d s", method, path, secs)
		} else if resp.StatusCode != http.StatusUnprocessableEntity {
			log.Printf("%s %s: HTTP %d %s", method, path, resp.StatusCode, e.Error)
		}
		return httpError{resp.StatusCode, e.Error, secs}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
