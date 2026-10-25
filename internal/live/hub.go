// Package live pushes hazard changes to browsers over WebSocket and fans them out across API
// instances through Redis pub/sub.
//
// Protocol (JSON text messages on GET /ws):
//
//	client -> server
//	  {"type":"subscribe.viewport","bbox":[west,south,east,north]}   hazards inside this box
//	  {"type":"subscribe.route","profile":"wheelchair","edge_ids":[...]}  my active route
//	  {"type":"unsubscribe.route"}
//
//	server -> client
//	  {"type":"hello","graph_version":12}
//	  {"type":"hazard.created"|"hazard.updated"|"hazard.expired","hazard":{...},"graph_version":13}
//	  {"type":"route.affected","hazard":{...},"edge_id":4035,"graph_version":13}
//	  {"type":"error","message":"..."}
//
// Who gets what:
//   - hazard.* goes to every client whose viewport contains the report's point.
//   - route.affected goes ONLY to clients whose subscribed route uses the report's edge, found
//     through an inverted index (edge id -> clients), and only if the hazard actually affects
//     that client's profile (ice is a soft penalty, a blocked sidewalk blocks, an elevator
//     outage doesn't matter to someone walking).
package live

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/coder/websocket"
)

const (
	sendBuffer   = 32               // queued messages per client before it counts as too slow
	maxRouteEdge = 5000             // a route subscription can't list more edges than this
	pingEvery    = 25 * time.Second // keepalive; also detects dead connections
	writeTimeout = 5 * time.Second
)

// Hub tracks connected clients and their subscriptions.
type Hub struct {
	origins []string     // host patterns allowed to connect cross-origin, e.g. "localhost:3000"
	version func() int64 // current graph version, for the hello message

	mu      sync.RWMutex // guards everything below and the subscription fields of every client
	clients map[*client]struct{}
	byEdge  map[int64]map[*client]struct{} // inverted index: edge DB id -> clients routing over it
}

// client is one WebSocket connection.
type client struct {
	conn *websocket.Conn
	send chan []byte // outgoing messages; drained by writeLoop

	// Subscriptions (guarded by Hub.mu).
	viewport *[4]float64 // west, south, east, north; nil = no viewport subscription
	profile  cost.Profile
	edges    []int64 // edges of the subscribed route (also present in Hub.byEdge)
}

// NewHub creates a hub. allowedOrigins are full origins like "http://localhost:3000" (the same
// list as CORS); version reports the current graph version.
func NewHub(allowedOrigins []string, version func() int64) *Hub {
	var hosts []string
	for _, o := range allowedOrigins {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host) // the websocket library matches on host, not full origin
		}
	}
	return &Hub{origins: hosts, version: version, clients: map[*client]struct{}{}, byEdge: map[int64]map[*client]struct{}{}}
}

// Clients is the number of connected clients (for metrics).
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeWS upgrades the request to a WebSocket and serves the client until it disconnects.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	// The HTTP server's read/write timeouts were set on the connection for this request; a
	// WebSocket lives for minutes or hours, so clear them before taking the connection over.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	// Accept checks the Origin header against OriginPatterns, so other websites can't open a
	// socket to us from a visitor's browser (the WebSocket equivalent of CORS).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	conn.SetReadLimit(64 << 10) // messages are tiny; refuse anything big

	c := &client{conn: conn, send: make(chan []byte, sendBuffer)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer h.remove(c)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel() // stops writeLoop when the read loop ends
	go c.writeLoop(ctx)

	c.trySend(mustJSON(map[string]any{"type": "hello", "graph_version": h.version()}))

	// Read loop: runs until the client disconnects or sends something invalid at the protocol
	// level. (Read also processes pongs, which the keepalive ping in writeLoop relies on.)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		h.handle(c, data)
	}
}

// writeLoop is the only goroutine that writes to the connection (websocket writes must not
// run concurrently). It sends queued messages and a periodic ping.
func (c *client) writeLoop(ctx context.Context) {
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.conn.CloseNow()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 2*writeTimeout)
			err := c.conn.Ping(pctx) // waits for the pong; fails if the client is gone
			cancel()
			if err != nil {
				c.conn.CloseNow()
				return
			}
		}
	}
}

// trySend queues msg without blocking. A client whose buffer is full is too slow to keep up;
// it is disconnected (it can reconnect and reload) rather than slowing everyone else down.
func (c *client) trySend(msg []byte) {
	select {
	case c.send <- msg:
	default:
		go c.conn.Close(websocket.StatusPolicyViolation, "too slow")
	}
}

// remove unregisters a disconnected client from the client set and the edge index.
func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	h.unindexLocked(c)
	c.conn.CloseNow()
}

// inbound is any client -> server message; which fields are used depends on Type.
type inbound struct {
	Type    string    `json:"type"`
	BBox    []float64 `json:"bbox"`
	Profile string    `json:"profile"`
	EdgeIDs []int64   `json:"edge_ids"`
}

// handle applies one client message to its subscriptions.
func (h *Hub) handle(c *client, data []byte) {
	var m inbound
	if err := json.Unmarshal(data, &m); err != nil {
		c.trySend(errorMsg("invalid JSON"))
		return
	}
	switch m.Type {
	case "subscribe.viewport":
		if len(m.BBox) != 4 || m.BBox[0] >= m.BBox[2] || m.BBox[1] >= m.BBox[3] {
			c.trySend(errorMsg("bbox must be [west,south,east,north]"))
			return
		}
		vp := [4]float64{m.BBox[0], m.BBox[1], m.BBox[2], m.BBox[3]}
		h.mu.Lock()
		c.viewport = &vp
		h.mu.Unlock()

	case "subscribe.route":
		p, err := cost.ParseProfile(m.Profile)
		if err != nil {
			c.trySend(errorMsg(err.Error()))
			return
		}
		if len(m.EdgeIDs) > maxRouteEdge {
			c.trySend(errorMsg("too many edge_ids"))
			return
		}
		h.mu.Lock()
		h.unindexLocked(c) // replace any previous route
		c.profile, c.edges = p, m.EdgeIDs
		for _, e := range c.edges {
			if h.byEdge[e] == nil {
				h.byEdge[e] = map[*client]struct{}{}
			}
			h.byEdge[e][c] = struct{}{}
		}
		h.mu.Unlock()

	case "unsubscribe.route":
		h.mu.Lock()
		h.unindexLocked(c)
		h.mu.Unlock()

	default:
		c.trySend(errorMsg("unknown message type " + m.Type))
	}
}

// unindexLocked removes c's route from the edge index. Caller holds h.mu for writing.
func (h *Hub) unindexLocked(c *client) {
	for _, e := range c.edges {
		delete(h.byEdge[e], c)
		if len(h.byEdge[e]) == 0 {
			delete(h.byEdge, e) // keep the index small: only edges someone is routing over
		}
	}
	c.edges = nil
}

// Broadcast delivers one committed change to the clients that care about it.
func (h *Hub) Broadcast(ch reports.Change) {
	r := ch.Report
	// resolved -> hazard.updated (status says "resolved"; clients drop non-live reports)
	typ := "hazard.updated"
	switch ch.Kind {
	case "created":
		typ = "hazard.created"
	case "expired":
		typ = "hazard.expired"
	}
	msg := mustJSON(map[string]any{"type": typ, "hazard": r, "graph_version": ch.Seq})

	h.mu.RLock()
	defer h.mu.RUnlock()

	// 1) Viewport subscribers whose box contains the report.
	for c := range h.clients {
		if vp := c.viewport; vp != nil &&
			r.Point[0] >= vp[0] && r.Point[0] <= vp[2] && r.Point[1] >= vp[1] && r.Point[1] <= vp[3] {
			c.trySend(msg)
		}
	}

	// 2) Route subscribers on this edge: one map lookup, not a scan of every client. Only for
	//    new reports (votes on an existing one shouldn't re-prompt), and only when the hazard
	//    changes this client's route cost.
	if ch.Kind != "created" || !r.Status.Live() {
		return
	}
	var affected []byte
	for c := range h.byEdge[r.EdgeID] {
		if ef := hazard.EffectOn(r.Type, r.Severity, c.profile); !ef.Block && ef.Mult <= 1 {
			continue
		}
		if affected == nil { // encode lazily: most hazards hit no active route
			affected = mustJSON(map[string]any{"type": "route.affected", "hazard": r, "edge_id": r.EdgeID, "graph_version": ch.Seq})
		}
		c.trySend(affected)
	}
}

func errorMsg(s string) []byte { return mustJSON(map[string]any{"type": "error", "message": s}) }

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("live: marshal: %v", err) // only possible with unsupported types: a bug
	}
	return b
}
