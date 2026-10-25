"use client";

// useLive: the browser side of the live-update channel (GET /ws on the Go API).
//
// What it does:
//   1. keeps ONE WebSocket open, reconnecting with exponential backoff if it drops;
//   2. tells the server what this tab cares about: the visible map area (viewport) and the
//      edges of the route being shown (route), re-sending both after every reconnect;
//   3. keeps a live map of hazards in the viewport: loaded over HTTP when the viewport
//      changes or the socket (re)connects, then patched by hazard.* events;
//   4. surfaces route.affected events, which the server sends only when a new hazard lands on
//      one of this route's edges AND matters for its profile.
//
// Why reload over HTTP after reconnecting: events sent while we were disconnected are gone,
// so the list could be stale. One GET makes it correct again.

import { useCallback, useEffect, useRef, useState } from "react";

import { fetchHazards, WS_URL, type BBox, type Hazard, type Profile } from "./api";

export interface RouteSubscription {
  profile: Profile;
  edgeIds: number[];
}

/** A route.affected event: a newly reported hazard on the active route. */
export interface RouteAffected {
  hazard: Hazard;
  graphVersion: number;
}

const isLive = (h: Hazard) => h.status === "active" || h.status === "confirmed";

export function useLive(viewport: BBox | undefined, route: RouteSubscription | undefined) {
  const [hazards, setHazards] = useState<Map<number, Hazard>>(new Map());
  const [affected, setAffected] = useState<RouteAffected>();
  const [connected, setConnected] = useState(false);
  // Bumped on every successful (re)connect, so the hazard list is reloaded after a gap.
  const [connection, setConnection] = useState(0);

  const wsRef = useRef<WebSocket | null>(null);
  // Latest subscriptions in refs, so the onopen handler (created once per socket) always
  // sends the current ones rather than whatever they were when the socket was created.
  const viewportRef = useRef(viewport);
  const routeRef = useRef(route);
  // Highest graph version seen in a live event; an HTTP list older than this is discarded.
  const lastEventVersion = useRef(0);

  const send = useCallback((msg: object) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }, []);

  // 1) Connection lifecycle: connect on mount, reconnect with backoff, close on unmount.
  useEffect(() => {
    let closedByUs = false;
    let retryDelay = 1000;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      const ws = new WebSocket(WS_URL);
      wsRef.current = ws;

      ws.onopen = () => {
        retryDelay = 1000; // healthy again: reset the backoff
        setConnected(true);
        setConnection((n) => n + 1);
        // Re-send subscriptions: a new socket starts with none on the server.
        if (viewportRef.current) ws.send(JSON.stringify({ type: "subscribe.viewport", bbox: viewportRef.current }));
        const r = routeRef.current;
        if (r) ws.send(JSON.stringify({ type: "subscribe.route", profile: r.profile, edge_ids: r.edgeIds }));
      };

      ws.onmessage = (e) => {
        const msg = JSON.parse(e.data);
        if (typeof msg.graph_version === "number") {
          lastEventVersion.current = Math.max(lastEventVersion.current, msg.graph_version);
        }
        switch (msg.type) {
          case "hazard.created":
          case "hazard.updated":
          case "hazard.expired": {
            const h: Hazard = msg.hazard;
            // Copy the Map (React state must be replaced, not mutated, to trigger a render).
            setHazards((prev) => {
              const next = new Map(prev);
              if (isLive(h)) next.set(h.id, h);
              else next.delete(h.id); // resolved or expired: off the map
              return next;
            });
            break;
          }
          case "route.affected":
            setAffected({ hazard: msg.hazard, graphVersion: msg.graph_version });
            break;
        }
      };

      ws.onclose = () => {
        setConnected(false);
        if (closedByUs) return;
        // Exponential backoff with a cap: 1 s, 2 s, 4 s ... 30 s. Avoids hammering a server
        // that is restarting, while still recovering quickly from a blip.
        retryTimer = setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, 30_000);
      };
    };

    connect();
    return () => {
      closedByUs = true;
      clearTimeout(retryTimer);
      wsRef.current?.close();
    };
  }, []);

  // 2) Viewport changed (or we reconnected): subscribe to it and reload its hazards.
  useEffect(() => {
    viewportRef.current = viewport;
    if (!viewport) return;
    send({ type: "subscribe.viewport", bbox: viewport });
    const controller = new AbortController();
    fetchHazards(viewport, controller.signal)
      .then(({ hazards: list, graph_version }) => {
        // If a live event newer than this list already arrived, the list is stale.
        if (graph_version < lastEventVersion.current) return;
        setHazards(new Map(list.map((h) => [h.id, h])));
      })
      .catch(() => {}); // aborted, or API down: the socket's reconnect will retry
    return () => controller.abort();
  }, [viewport, connection, send]);

  // 3) Active route changed: (re)subscribe its edges, or unsubscribe when there is none.
  //    The caller memoizes `route`, so this runs only when the route really changes.
  useEffect(() => {
    routeRef.current = route;
    send(route ? { type: "subscribe.route", profile: route.profile, edge_ids: route.edgeIds } : { type: "unsubscribe.route" });
  }, [route, send]);

  const clearAffected = useCallback(() => setAffected(undefined), []);

  return { hazards: [...hazards.values()], affected, clearAffected, connected };
}
