"use client";

// Planner: the stateful heart of the page. It owns the start/end points, the profile and the
// current route, and re-fetches the route whenever any of them change. MapView and RoutePanel
// are "dumb" views: they render props and report user actions back up through callbacks.
//
// Click flow: 1st click = start, 2nd click = destination (triggers routing), 3rd click =
// new start (clears the old route).

import { useCallback, useEffect, useState } from "react";

import { ApiError, fetchRoute, type LngLat, type Profile, type RouteResponse } from "@/lib/api";
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

  // A string identifying "the route we want right now"; undefined until both points are set.
  const requestKey = start && end ? `${start}|${end}|${profile}` : undefined;

  // Loading/route/error are *derived* rather than stored: the stored result only counts if it
  // answers the current inputs. So when the user clicks a new point or switches profile, the
  // old route disappears immediately and "loading" becomes true, with no extra state to keep in sync.
  const current = result && result.key === requestKey ? result : undefined;
  const loading = requestKey !== undefined && current === undefined;

  const handleMapClick = useCallback(
    (p: LngLat) => {
      if (!start || end) {
        // Starting over: this click becomes the new start.
        setStart(p);
        setEnd(undefined);
      } else {
        setEnd(p);
      }
    },
    [start, end],
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
  };

  // Layout: panel on the left on wide screens; on phones the map is on top and the panel
  // scrolls underneath it.
  return (
    <div className="flex h-dvh flex-col md:flex-row">
      <aside className="order-2 h-[45dvh] shrink-0 border-gray-200 bg-white md:order-1 md:h-full md:w-96 md:border-r">
        <RoutePanel
          profile={profile}
          onProfileChange={setProfile}
          hasStart={!!start}
          hasEnd={!!end}
          route={current?.route}
          loading={loading}
          error={current?.error}
          onClear={clear}
        />
      </aside>
      <main className="order-1 min-h-0 flex-1 md:order-2">
        <MapView start={start} end={end} route={current?.route} onMapClick={handleMapClick} />
      </main>
    </div>
  );
}
