package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/coder/websocket"
)

// testClient is a WebSocket client connected to a test hub. A background goroutine reads
// messages into a channel: in coder/websocket a Read whose context expires closes the
// connection, so "wait briefly for a message that should NOT come" must not use Read's ctx.
type testClient struct {
	t    *testing.T
	conn *websocket.Conn
	msgs chan map[string]any
}

func dial(t *testing.T, srv *httptest.Server) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &testClient{t: t, conn: conn, msgs: make(chan map[string]any, 16)}
	go func() {
		for {
			_, b, err := conn.Read(context.Background())
			if err != nil {
				close(c.msgs)
				return
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			c.msgs <- m
		}
	}()
	if m := c.next(); m["type"] != "hello" {
		t.Fatalf("first message %v, want hello", m)
	}
	return c
}

func (c *testClient) sendJSON(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next message, or nil if none arrives within 300 ms.
func (c *testClient) next() map[string]any {
	select {
	case m := <-c.msgs:
		return m
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func change(kind string, id, edge int64, typ hazard.Type, sev hazard.Severity, status hazard.Status, lon, lat float64) reports.Change {
	return reports.Change{Seq: id, Kind: kind, Report: reports.Report{
		ID: id, EdgeID: edge, Type: typ, Severity: sev, Status: status, Point: [2]float64{lon, lat}}}
}

func TestHubRoutingOfMessages(t *testing.T) {
	hub := NewHub(nil, func() int64 { return 0 })
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()

	wheel := dial(t, srv)  // wheelchair route over edges 10, 11
	winter := dial(t, srv) // walk_winter route over edge 11
	viewer := dial(t, srv) // only watches a viewport around campus
	wheel.sendJSON(map[string]any{"type": "subscribe.route", "profile": "wheelchair", "edge_ids": []int64{10, 11}})
	winter.sendJSON(map[string]any{"type": "subscribe.route", "profile": "walk_winter", "edge_ids": []int64{11}})
	viewer.sendJSON(map[string]any{"type": "subscribe.viewport", "bbox": []float64{-80.55, 43.46, -80.53, 43.48}})
	time.Sleep(100 * time.Millisecond) // let the server process the subscriptions

	// 1) An elevator outage on edge 11: blocks wheelchair, irrelevant to walk_winter.
	hub.Broadcast(change("created", 1, 11, hazard.ElevatorOut, hazard.Major, hazard.Active, -80.54, 43.47))
	if m := wheel.next(); m["type"] != "route.affected" || m["edge_id"] != float64(11) {
		t.Errorf("wheelchair: got %v, want route.affected on edge 11", m)
	}
	if m := winter.next(); m != nil {
		t.Errorf("walk_winter is unaffected by an elevator outage, got %v", m)
	}
	if m := viewer.next(); m["type"] != "hazard.created" {
		t.Errorf("viewer: got %v, want hazard.created", m)
	}

	// 2) A hazard on an edge nobody routes over, outside the viewport: nobody hears anything.
	hub.Broadcast(change("created", 2, 99, hazard.BlockedSidewalk, hazard.Major, hazard.Active, -80.51, 43.46))
	for name, c := range map[string]*testClient{"wheel": wheel, "winter": winter, "viewer": viewer} {
		if m := c.next(); m != nil {
			t.Errorf("%s: got %v, want nothing", name, m)
		}
	}

	// 3) A vote update on report 1 reaches the viewport but does not re-prompt the route.
	hub.Broadcast(change("updated", 1, 11, hazard.ElevatorOut, hazard.Major, hazard.Confirmed, -80.54, 43.47))
	if m := wheel.next(); m != nil {
		t.Errorf("wheelchair re-prompted on an update: %v", m)
	}
	if m := viewer.next(); m["type"] != "hazard.updated" {
		t.Errorf("viewer: got %v, want hazard.updated", m)
	}

	// 4) After unsubscribing, the route subscriber is out of the edge index.
	wheel.sendJSON(map[string]any{"type": "unsubscribe.route"})
	time.Sleep(100 * time.Millisecond)
	hub.Broadcast(change("created", 3, 10, hazard.BlockedSidewalk, hazard.Major, hazard.Active, -80.51, 43.46))
	if m := wheel.next(); m != nil {
		t.Errorf("unsubscribed client got %v", m)
	}
	hub.mu.RLock()
	_, stillIndexed := hub.byEdge[10]
	hub.mu.RUnlock()
	if stillIndexed {
		t.Error("edge 10 still in the index after the only subscriber left")
	}

	// 5) Bad input gets an error message, not a dropped connection.
	viewer.sendJSON(map[string]any{"type": "subscribe.viewport", "bbox": []float64{1, 2}})
	if m := viewer.next(); m["type"] != "error" {
		t.Errorf("bad bbox: got %v, want error", m)
	}
}

func TestHubRejectsForeignOrigins(t *testing.T) {
	hub := NewHub([]string{"http://localhost:3000"}, func() int64 { return 0 })
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 for a foreign origin, got err=%v resp=%v", err, resp)
	}
}
