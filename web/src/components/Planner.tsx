"use client";

// Planner: the stateful heart of the page. It owns the start/end points, the profile and the
// current route, and re-fetches the route whenever any of them change. MapView and RoutePanel
// are "dumb" views: they render props and report user actions back up through callbacks.
//
// Click flow: 1st click = start, 2nd click = destination (triggers routing), 3rd click =
// new start (clears the old route).
//
// Live updates (useLive): the map's visible area and the current route's edges are sent to the
// server over WebSocket. New hazards appear on the map as they're reported; one that lands on
// this route shows the "reroute?" toast.

import { useCallback, useEffect, useMemo, useState } from "react";

import { ApiError, fetchRoute, type BBox, type LngLat, type Profile, type RouteResponse } from "@/lib/api";
import { useLive } from "@/lib/useLive";
import LiveToast from "./LiveToast";
import MapView from "./MapView";
import RoutePanel from "./RoutePanel";

// A finished request, tagged with the inputs it answers.
interface RouteResult {
  key: string;
  route?: RouteResponse;
  error?: string;
}

export default function Planner() {
  const [start, setStart] = useState<LngLat>();
  const [end, setEnd] = useState<LngLat>();
  const [profile, setProfile] = useState<Profile>("wheelchair");
  const [result, setResult] = useState<RouteResult>();
  const [viewport, setViewport] = useState<BBox>();
  // Bumped by "Reroute": part of the request key, so the same points + profile fetch again
  // (and get a new route, since the hazard changed the graph version and thus the cache key).
  const [reroutes, setReroutes] = useState(0);

  // A string identifying "the route we want right now"; undefined until both points are set.
  const requestKey = start && end ? `${start}|${end}|${profile}|${reroutes}` : undefined;

  // Loading/route/error are *derived* rather than stored: the stored result only counts if it
  // answers the current inputs. So when the user clicks a new point or switches profile, the
  // old route disappears immediately and "loading" becomes true, with no extra state to keep in sync.
  const current = result && result.key === requestKey ? result : undefined;
  const loading = requestKey !== undefined && current === undefined;

  // The route subscription: which edges to watch, for which profile. Memoized on the route
  // object so useLive re-subscribes only when the route itself changes.
  const shownRoute = current?.route;
  const routeSub = useMemo(
    () =>
      shownRoute
        ? { profile: shownRoute.profile, edgeIds: [...new Set(shownRoute.segments.flatMap((s) => s.edge_ids))] }
        : undefined,
    [shownRoute],
  );
  const { hazards, affected, clearAffected } = useLive(viewport, routeSub);

  const handleMapClick = useCallback(
    (p: LngLat) => {
      clearAffected(); // a prompt about the old route is meaningless once the user moves on
      if (!start || end) {
        // Starting over: this click becomes the new start.
        setStart(p);
        setEnd(undefined);
      } else {
        setEnd(p);
      }
    },
    [start, end, clearAffected],
  );

  // Fetch a route whenever the request key changes (new points or new profile).
  useEffect(() => {
    if (!start || !end || !requestKey) return;
    // AbortController cancels this request if the inputs change before it finishes (the
    // cleanup below runs first), so the network work for an outdated request stops early.
    const controller = new AbortController();
    fetchRoute(start, end, profile, controller.signal)
      .then((route) => setResult({ key: requestKey, route }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return; // superseded by a newer request; not a real error
        const error = err instanceof ApiError ? err.message : "Could not reach the routing server. Is the API running?";
        setResult({ key: requestKey, error });
      });
    return () => controller.abort();
  }, [start, end, profile, requestKey]);

  const clear = () => {
    setStart(undefined);
    setEnd(undefined);
    clearAffected();
  };

  const reroute = () => {
    clearAffected();
    setReroutes((n) => n + 1);
  };

  // Layout: panel on the left on wide screens; on phones the map is on top and the panel
  // scrolls underneath it.
  return (
    <div className="flex h-dvh flex-col md:flex-row">
      <aside className="order-2 h-[45dvh] shrink-0 border-gray-200 bg-white md:order-1 md:h-full md:w-96 md:border-r">
        <RoutePanel
          profile={profile}
          onProfileChange={(p) => {
            clearAffected();
            setProfile(p);
          }}
          hasStart={!!start}
          hasEnd={!!end}
          route={current?.route}
          loading={loading}
          error={current?.error}
          onClear={clear}
        />
      </aside>
      <main className="relative order-1 min-h-0 flex-1 md:order-2">
        <MapView
          start={start}
          end={end}
          route={shownRoute}
          hazards={hazards}
          onMapClick={handleMapClick}
          onViewportChange={setViewport}
        />
        {/* Toast floats over the map; only shown while a route is displayed. */}
        {affected && shownRoute && (
          <div className="pointer-events-none absolute inset-x-0 top-4 flex justify-center px-4">
            <LiveToast affected={affected} onReroute={reroute} onDismiss={clearAffected} />
          </div>
        )}
      </main>
    </div>
  );
}
