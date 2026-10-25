"use client";

// RoutePanel: the side panel. Shows what to do next (click start / destination), the profile
// selector, and once a route exists: a summary, how much of it is backed by verified data,
// the API's plain-language notes, any barriers, and the segment list.

import Link from "next/link";

import type { Profile, RouteResponse } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR, CONFIDENCE_LABEL, formatDistance } from "@/lib/style";
import ProfileSelector from "./ProfileSelector";
import RouteSegmentList from "./RouteSegmentList";

interface Props {
  profile: Profile;
  onProfileChange: (p: Profile) => void;
  hasStart: boolean;
  hasEnd: boolean;
  route?: RouteResponse;
  loading: boolean;
  error?: string;
  onClear: () => void;
}

export default function RoutePanel({ profile, onProfileChange, hasStart, hasEnd, route, loading, error, onClear }: Props) {
  // The instruction line walks the user through the two clicks.
  const instruction = !hasStart
    ? "Click the map to set your start."
    : !hasEnd
      ? "Now click your destination."
      : "Click the map again to start a new route.";

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <header>
        <h1 className="text-xl font-semibold text-gray-900">Passable</h1>
        <p className="text-sm text-gray-600">Accessible walking routes on UW campus and uptown Waterloo.</p>
      </header>

      <ProfileSelector value={profile} onChange={onProfileChange} />

      <div className="flex items-center justify-between gap-2">
        {/* aria-live announces the changing instruction/status to screen readers. */}
        <p className="text-sm text-gray-800" aria-live="polite">
          {loading ? "Finding a route…" : instruction}
        </p>
        {(hasStart || hasEnd) && (
          <button onClick={onClear} className="text-sm text-blue-700 underline-offset-2 hover:underline">
            Clear
          </button>
        )}
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-red-50 p-3 text-sm text-red-800">
          {error}
        </p>
      )}

      {route && <RouteSummary route={route} />}

      {/* Small footer links, pushed to the bottom of the panel. */}
      <footer className="mt-auto flex gap-3 pt-4 text-xs text-gray-500">
        <Link href="/about" className="hover:underline">
          About &amp; limitations
        </Link>
        <Link href="/status" className="hover:underline">
          Status
        </Link>
      </footer>
    </div>
  );
}

function RouteSummary({ route }: { route: RouteResponse }) {
  const total = route.distance_m || 1;
  const order = ["verified", "inferred", "unknown"] as const;

  return (
    <section className="flex flex-col gap-3">
      {!route.accessible && (
        <p role="alert" className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800">
          No fully accessible route for this profile. The route shown has the fewest barriers, marked in red.
        </p>
      )}

      <div className="flex items-baseline gap-3">
        <span className="text-2xl font-semibold tabular-nums text-gray-900">{formatDistance(route.distance_m)}</span>
        <span className="text-xs text-gray-500">
          found in {route.search_ms.toFixed(2)} ms · {route.nodes_expanded} nodes searched
        </span>
      </div>

      {/* Stacked bar: what share of the route is verified / inferred / unknown. */}
      <div>
        <div className="flex h-2 overflow-hidden rounded-full bg-gray-100" aria-hidden>
          {order.map((c) => (
            <div key={c} style={{ width: `${(100 * route.confidence_m[c]) / total}%`, background: CONFIDENCE_COLOR[c] }} />
          ))}
        </div>
        <ul className="mt-1 flex flex-wrap gap-x-3 text-xs text-gray-600">
          {order.map((c) => (
            <li key={c} className="flex items-center gap-1">
              <span aria-hidden className="inline-block h-2 w-2 rounded-full" style={{ background: CONFIDENCE_COLOR[c] }} />
              {CONFIDENCE_LABEL[c]} {formatDistance(route.confidence_m[c])}
            </li>
          ))}
        </ul>
      </div>

      {route.notes.length > 0 && (
        <ul className="list-disc space-y-1 pl-5 text-sm text-gray-700">
          {route.notes.map((n, i) => (
            <li key={i}>{n}</li>
          ))}
        </ul>
      )}

      {route.barriers && route.barriers.length > 0 && (
        <div>
          <h2 className="text-sm font-medium" style={{ color: BLOCKED_COLOR }}>
            Barriers
          </h2>
          <ul className="text-sm text-gray-800">
            {route.barriers.map((b, i) => (
              <li key={i}>
                {b.description} ({formatDistance(b.length_m)}): {b.reasons.join(", ")}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div>
        <h2 className="mb-1 text-sm font-medium text-gray-700">Route details</h2>
        <RouteSegmentList segments={route.segments} />
      </div>
    </section>
  );
}
