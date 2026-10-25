// Command wsprobe measures report-to-client propagation latency: the time from POSTing a
// hazard to one API instance until a WebSocket client connected to (possibly another)
// instance receives the hazard.created event, and route.affected for its subscribed route.
//
//	go run ./cmd/wsprobe -post http://localhost:8080 -ws ws://localhost:8081/ws -n 50
//
// Each probe hazard is resolved right after it is measured, so the probe leaves no live
// hazards behind. Run it against a dev or staging deployment, not production.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"
)

func main() {
	postBase := flag.String("post", "http://localhost:8080", "API base URL that receives the hazard POSTs")
	wsURL := flag.String("ws", "ws://localhost:8080/ws", "WebSocket URL the listening client connects to")
	origin := flag.String("origin", "http://localhost:3000", "Origin header (must be allowed by the API)")
	n := flag.Int("n", 30, "number of hazards to post")
	// A point on Two Row Path (UW campus) and the edge it snaps to.
	lat := flag.Float64("lat", 43.4708681, "hazard latitude")
	lng := flag.Float64("lng", -80.5427402, "hazard longitude")
	flag.Parse()
	ctx := context.Background()

	// 1) Connect the listener and wait for hello.
	conn, _, err := websocket.Dial(ctx, *wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {*origin}}})
	if err != nil {
		log.Fatalf("dial %s: %v", *wsURL, err)
	}
	defer conn.CloseNow()
	msgs := make(chan map[string]any, 64)
	go func() { // reader goroutine: every message goes to msgs with its arrival time attached
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				close(msgs)
				return
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			m["_at"] = time.Now()
			msgs <- m
		}
	}()
	<-msgs // hello

	// 2) One throwaway hazard to learn which edge the point snaps to, then subscribe the
	//    listener's "route" to that edge, plus a viewport around the point.
	sid := newSession(*postBase)
	first := post(*postBase, sid, *lat, *lng)
	edge := int64(first["edge_id"].(float64))
	resolve(*postBase, sid, int64(first["id"].(float64)))
	send(ctx, conn, map[string]any{"type": "subscribe.viewport", "bbox": []float64{*lng - 0.01, *lat - 0.01, *lng + 0.01, *lat + 0.01}})
	send(ctx, conn, map[string]any{"type": "subscribe.route", "profile": "wheelchair", "edge_ids": []int64{edge}})
	time.Sleep(300 * time.Millisecond)
	drain(msgs)

	// 3) Measure.
	var created, affected []time.Duration
	for i := 0; i < *n; i++ {
		start := time.Now()
		rep := post(*postBase, sid, *lat, *lng)
		id := rep["id"].(float64)
		deadline := time.After(3 * time.Second)
		gotCreated, gotAffected := false, false
		for !(gotCreated && gotAffected) {
			select {
			case m := <-msgs:
				h, _ := m["hazard"].(map[string]any)
				if h == nil || h["id"] != id {
					continue
				}
				d := m["_at"].(time.Time).Sub(start)
				switch m["type"] {
				case "hazard.created":
					created, gotCreated = append(created, d), true
				case "route.affected":
					affected, gotAffected = append(affected, d), true
				}
			case <-deadline:
				log.Fatalf("hazard %v: no event within 3 s (created=%v affected=%v)", id, gotCreated, gotAffected)
			}
		}
		resolve(*postBase, sid, int64(id))
		time.Sleep(50 * time.Millisecond)
		drain(msgs)
	}

	fmt.Printf("POST %s -> WS %s, %d hazards\n", *postBase, *wsURL, *n)
	fmt.Printf("  POST sent -> hazard.created received: %s\n", summary(created))
	fmt.Printf("  POST sent -> route.affected received: %s\n", summary(affected))
	fmt.Println("  (includes the POST's own DB transaction; the client sees the event at about the same time the POST returns)")
}

func summary(ds []time.Duration) string {
	slices.Sort(ds)
	p := func(q int) time.Duration { return ds[(len(ds)-1)*q/100].Round(10 * time.Microsecond) }
	return fmt.Sprintf("p50 %s  p95 %s  p99 %s  max %s", p(50), p(95), p(99), ds[len(ds)-1].Round(10*time.Microsecond))
}

func newSession(base string) string {
	var out struct {
		SessionID string `json:"session_id"`
	}
	mustDo("POST", base+"/api/v1/session", "", nil, &out)
	return out.SessionID
}

func post(base, sid string, lat, lng float64) map[string]any {
	var out map[string]any
	body, _ := json.Marshal(map[string]any{"lat": lat, "lng": lng, "type": "blocked_sidewalk", "note": "wsprobe"})
	mustDo("POST", base+"/api/v1/hazards", sid, body, &out)
	return out
}

func resolve(base, sid string, id int64) {
	mustDo("POST", fmt.Sprintf("%s/api/v1/hazards/%d/resolve", base, id), sid, nil, nil)
}

func mustDo(method, url, sid string, body []byte, out any) {
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set("X-Session-ID", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Fatalf("%s %s: HTTP %d", method, url, resp.StatusCode)
	}
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
}

func send(ctx context.Context, conn *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		log.Fatal(err)
	}
}

// drain discards messages already queued (e.g. the resolve updates of earlier probes).
func drain(msgs chan map[string]any) {
	for {
		select {
		case <-msgs:
		default:
			return
		}
	}
}
