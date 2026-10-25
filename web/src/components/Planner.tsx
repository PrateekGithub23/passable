"use client";

// Planner: the stateful heart of the page. It owns the start/end waypoints, the profile and the
// current route, and re-fetches the route whenever any of them change. MapView and RoutePanel
// are "dumb" views: they render props and report user actions back up through callbacks.
//
// Choosing points: search a place in the From/To fields, or tap the map. A tap fills whichever
// end is still empty; when both are set, a tap starts a new route from that point.
//
// Live updates (useLive): the map's visible area and the current route's edges are sent to the
// server over WebSocket. New hazards appear on the map as they're reported; one that lands on
// this route shows the "reroute?" toast.
//
// Reporting: "Report a problem" switches the map into report mode. The next tap marks the
// spot and opens ReportSheet; sending it files the report, which then comes back over the
// WebSocket like anyone else's.

import { CircleCheck, MapPin, Megaphone, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { ApiError, fetchRoute, voteHazard, type BBox, type LngLat, type Place, type Profile, type RouteResponse, type Waypoint } from "@/lib/api";
import { useLive } from "@/lib/useLive";
import LiveToast from "./LiveToast";
import MapView from "./MapView";
import ReportSheet from "./ReportSheet";
import RoutePanel from "./RoutePanel";

// A finished request, tagged with the inputs it answers.
interface RouteResult {
  key: string;
  route?: RouteResponse;
  error?: string;
}

const MAP_POINT = "Point on map";

/** Turn API errors into a next step the user can take. */
function friendlyError(err: unknown): string {
  if (!(err instanceof ApiError)) return "We couldn't reach the routing service. Check your connection and try again.";
  const m = err.message;
  if (m.includes("no mapped path")) return "One of your points is too far from any mapped path. Try a spot closer to a sidewalk or path.";
  if (m.includes("outside the service area")) return "That point is outside the area Passable covers (UW campus and uptown Waterloo).";
  if (m.includes("too many requests")) return "Lots of requests in a short time. Wait a few seconds and try again.";
  return m;
}

export default function Planner() {
  const [start, setStart] = useState<Waypoint>();
  const [end, setEnd] = useState<Waypoint>();
  const [profile, setProfile] = useState<Profile>("wheelchair");
  const [result, setResult] = useState<RouteResult>();
  const [viewport, setViewport] = useState<BBox>();
  // Bumped by "Reroute" and "Try again": part of the request key, so the same points + profile
  // fetch again (after a hazard, the graph version and thus the cache key have changed).
  const [attempt, setAttempt] = useState(0);
  // Where the map should pan to after a place is picked from search (nonce = pan again even
  // if the same place is picked twice).
  const [focus, setFocus] = useState<{ point: LngLat; nonce: number }>();
  // Report flow: off -> choosing a spot (reporting, no point) -> filling the form (point set).
  const [reporting, setReporting] = useState(false);
  const [reportPoint, setReportPoint] = useState<LngLat>();
  const [thanks, setThanks] = useState(false);

  // A string identifying "the route we want right now"; undefined until both points are set.
  const requestKey = start && end ? `${start.point}|${end.point}|${profile}|${attempt}` : undefined;

  // Loading/route/error are *derived* rather than stored: the stored result only counts if it
  // answers the current inputs. So when the user picks a new point or switches profile, the
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
  const { hazards, affected, clearAffected, connected } = useLive(viewport, routeSub);

  const handleMapClick = useCallback(
    (p: LngLat) => {
      if (reporting) {
        setReportPoint(p); // in report mode a tap marks the problem, not a route point
        return;
      }
      clearAffected(); // a prompt about the old route is meaningless once the user moves on
      const wp = { point: p, label: MAP_POINT };
      if (!start) setStart(wp);
      else if (!end) setEnd(wp);
      else {
        // Both set: this tap starts a new route.
        setStart(wp);
        setEnd(undefined);
      }
    },
    [start, end, clearAffected, reporting],
  );

  const pick = (which: "start" | "end") => (place: Place) => {
    clearAffected();
    const wp = { point: place.point, label: place.name };
    if (which === "start") setStart(wp);
    else setEnd(wp);
    // Show where it is, unless the route (which fits itself into view) is about to appear.
    if ((which === "start" ? end : start) === undefined) setFocus({ point: place.point, nonce: Date.now() });
  };

  // Fetch a route whenever the request key changes (new points, new profile, retry).
  useEffect(() => {
    if (!start || !end || !requestKey) return;
    // AbortController cancels this request if the inputs change before it finishes (the
    // cleanup below runs first), so the network work for an outdated request stops early.
    const controller = new AbortController();
    fetchRoute(start.point, end.point, profile, controller.signal)
      .then((route) => setResult({ key: requestKey, route }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return; // superseded by a newer request; not a real error
        setResult({ key: requestKey, error: friendlyError(err) });
      });
    return () => controller.abort();
  }, [start, end, profile, requestKey]);

  const clear = () => {
    setStart(undefined);
    setEnd(undefined);
    clearAffected();
  };

  const retry = () => {
    clearAffected();
    setAttempt((n) => n + 1);
  };

  const cancelReport = useCallback(() => {
    setReporting(false);
    setReportPoint(undefined);
  }, []);

  // "Thanks" message auto-hides after a few seconds.
  useEffect(() => {
    if (!thanks) return;
    const t = setTimeout(() => setThanks(false), 5000);
    return () => clearTimeout(t);
  }, [thanks]);

  // Layout: panel on the left on wide screens. On phones the map is on top and the panel is a
  // rounded sheet that slightly overlaps it, so the two read as one surface.
  return (
    <div className="flex h-dvh flex-col bg-stone-50 md:flex-row">
      <aside className="relative z-10 order-2 -mt-4 h-[54dvh] shrink-0 rounded-t-3xl bg-white shadow-[0_-6px_20px_rgb(28_25_23/0.08)] md:order-1 md:mt-0 md:h-full md:w-[400px] md:rounded-none md:border-r md:border-stone-200 md:shadow-none">
        <span aria-hidden className="mx-auto mt-2 block h-1 w-10 rounded-full bg-stone-300 md:hidden" />
        <RoutePanel
          profile={profile}
          onProfileChange={(p) => {
            clearAffected();
            setProfile(p);
          }}
          start={start}
          end={end}
          onPickStart={pick("start")}
          onPickEnd={pick("end")}
          onClearStart={() => setStart(undefined)}
          onClearEnd={() => setEnd(undefined)}
          onSwap={() => {
            setStart(end);
            setEnd(start);
          }}
          route={shownRoute}
          loading={loading}
          error={current?.error}
          onRetry={retry}
          onClear={clear}
          live={connected}
        />
      </aside>
      <main className="relative order-1 min-h-0 flex-1 md:order-2">
        <MapView
          start={start?.point}
          end={end?.point}
          route={shownRoute}
          hazards={hazards}
          reporting={reporting}
          reportPoint={reportPoint}
          focus={focus}
          onMapClick={handleMapClick}
          onViewportChange={setViewport}
          onVote={voteHazard}
        />

        {/* Top of the map: report-mode instructions, the thank-you, or the reroute prompt. */}
        <div className="pointer-events-none absolute inset-x-0 top-3 flex justify-center px-3 md:top-4">
          {reporting && !reportPoint ? (
            <div role="status" className="pointer-events-auto flex animate-slide-up items-center gap-3 rounded-2xl bg-stone-900 py-2.5 pr-2.5 pl-4 text-sm text-white shadow-lg">
              <MapPin aria-hidden className="h-4 w-4 text-amber-300" />
              Tap the map exactly where the problem is.
              <button onClick={cancelReport} className="flex items-center gap-1 rounded-lg bg-white/15 px-2.5 py-1.5 transition-colors hover:bg-white/25">
                <X aria-hidden className="h-3.5 w-3.5" />
                Cancel
              </button>
            </div>
          ) : thanks ? (
            <div role="status" className="flex animate-slide-up items-center gap-2 rounded-2xl bg-brand-700 px-4 py-3 text-sm text-white shadow-lg">
              <CircleCheck aria-hidden className="h-4 w-4" />
              Thanks! Your report is live. People nearby can confirm it.
            </div>
          ) : (
            affected && shownRoute && <LiveToast affected={affected} onReroute={retry} onDismiss={clearAffected} />
          )}
        </div>

        {/* The report form: a full-width bottom sheet over the whole page on phones (the map is
            only the top half there), a floating card over the map on wider screens. */}
        {reportPoint && (
          <div className="pointer-events-none fixed inset-x-0 bottom-0 z-20 flex justify-center p-2 md:absolute md:bottom-4 md:px-4">
            <ReportSheet
              point={reportPoint}
              onCancel={cancelReport}
              onDone={() => {
                cancelReport();
                setThanks(true);
              }}
            />
          </div>
        )}

        {/* The single entry point for reporting. Dark rather than loud orange: always visible,
            but it doesn't compete with the route. */}
        {!reportPoint && !reporting && (
          <div className="pointer-events-none absolute inset-x-0 bottom-8 flex justify-center px-4 md:bottom-6">
            <button
              onClick={() => {
                clearAffected();
                setReporting(true);
              }}
              className="pointer-events-auto flex items-center gap-2 rounded-full bg-stone-900 px-5 py-3 text-sm font-semibold text-white shadow-lg transition-all hover:-translate-y-0.5 hover:bg-stone-800 hover:shadow-xl active:translate-y-0"
            >
              <Megaphone aria-hidden className="h-4 w-4 text-amber-300" />
              Report a problem
            </button>
          </div>
        )}
      </main>
    </div>
  );
}
