"use client";

// RoutePanel: the side panel (a bottom sheet on phones). Top to bottom:
//   header (brand, live-update status, About) -> From / To search -> profile ->
//   one state at a time: empty guidance, "pick a destination", loading, error, or the route.
// Technical numbers (search time, nodes expanded) are deliberately left out; they live on
// the /status page.

import {
  Accessibility,
  ArrowDownUp,
  CircleAlert,
  CircleCheck,
  CircleHelp,
  Info,
  Megaphone,
  RefreshCw,
  Search,
  TriangleAlert,
  X,
} from "lucide-react";
import Link from "next/link";

import type { Confidence, Place, Profile, RouteResponse, Waypoint } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR, CONFIDENCE_HELP, CONFIDENCE_LABEL, HAZARD_STYLE, formatDistance, humanizeReason } from "@/lib/style";
import PlaceSearch from "./PlaceSearch";
import ProfileSelector from "./ProfileSelector";
import RouteSegmentList from "./RouteSegmentList";
import { InfoTip, LogoMark } from "./ui";

interface Props {
  profile: Profile;
  onProfileChange: (p: Profile) => void;
  start?: Waypoint;
  end?: Waypoint;
  onPickStart: (p: Place) => void;
  onPickEnd: (p: Place) => void;
  onClearStart: () => void;
  onClearEnd: () => void;
  onSwap: () => void;
  route?: RouteResponse;
  loading: boolean;
  error?: string;
  onRetry: () => void;
  onClear: () => void;
  live: boolean; // WebSocket connected
}

export default function RoutePanel(props: Props) {
  const { start, end, route, loading, error } = props;
  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between px-5 pt-4 pb-3">
        <Link href="/" className="flex items-center gap-2.5 rounded-lg" aria-label="Passable home">
          <LogoMark className="h-8 w-8" />
          <span className="text-xl font-bold tracking-tight text-stone-900">Passable</span>
        </Link>
        <div className="flex items-center gap-1">
          <LiveBadge live={props.live} />
          <Link
            href="/about"
            className="flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm text-stone-600 transition-colors hover:bg-stone-100 hover:text-stone-900"
          >
            <CircleHelp aria-hidden className="h-4 w-4" />
            About
          </Link>
        </div>
      </header>

      {/* Where from, where to, and how. */}
      <section aria-label="Plan a route" className="space-y-3 px-5">
        <div className="relative space-y-2">
          <PlaceSearch
            label="Start"
            placeholder="Start: search or tap the map"
            icon={<span className="block h-3 w-3 rounded-full border-[3px] border-brand-700 bg-white" />}
            value={start?.label ?? ""}
            onSelect={props.onPickStart}
            onClear={props.onClearStart}
          />
          <PlaceSearch
            label="Destination"
            placeholder="Destination"
            icon={<span className="block h-3 w-3 rounded-full bg-brand-700" />}
            value={end?.label ?? ""}
            onSelect={props.onPickEnd}
            onClear={props.onClearEnd}
          />
          {(start || end) && (
            <button
              type="button"
              onClick={props.onSwap}
              aria-label="Swap start and destination"
              title="Swap start and destination"
              className="absolute top-1/2 right-11 -translate-y-1/2 rounded-full border border-stone-200 bg-white p-1.5 text-stone-500 shadow-sm transition-colors hover:border-stone-300 hover:text-stone-900"
            >
              <ArrowDownUp className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <ProfileSelector value={props.profile} onChange={props.onProfileChange} />
      </section>

      {/* The one thing that matters right now. aria-live announces changes to screen readers. */}
      <div className="mt-4 flex-1 overflow-y-auto px-5 pb-4" aria-live="polite">
        {error ? (
          <ErrorCard message={error} onRetry={props.onRetry} />
        ) : loading ? (
          <LoadingCard />
        ) : route ? (
          <RouteSummary route={route} onClear={props.onClear} />
        ) : start && !end ? (
          <Hint icon={<Search className="h-4 w-4" />} text="Now choose a destination: search above, or tap the map." />
        ) : !start && end ? (
          <Hint icon={<Search className="h-4 w-4" />} text="Now choose where you're starting from." />
        ) : (
          <EmptyState />
        )}
      </div>

      <footer className="flex items-center justify-between border-t border-stone-200 px-5 py-2.5 text-xs text-stone-500">
        <span>
          Map data ©{" "}
          <a href="https://www.openstreetmap.org/copyright" className="underline-offset-2 hover:underline">
            OpenStreetMap
          </a>
        </span>
        <Link href="/about" className="underline-offset-2 hover:underline">
          About &amp; limitations
        </Link>
      </footer>
    </div>
  );
}

/** Whether live hazard updates are connected: a small trust signal, explained on hover. */
function LiveBadge({ live }: { live: boolean }) {
  return (
    <span
      className="flex items-center gap-1.5 rounded-full px-2 py-1 text-xs font-medium text-stone-600"
      title={live ? "Live: new hazard reports appear on the map as they happen" : "Reconnecting to live updates…"}
    >
      <span aria-hidden className="relative flex h-2 w-2">
        {live && <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-brand-500 opacity-60" />}
        <span className={`relative inline-flex h-2 w-2 rounded-full ${live ? "bg-brand-600" : "bg-stone-300"}`} />
      </span>
      <span>{live ? "Live" : "Offline"}</span>
    </span>
  );
}

function EmptyState() {
  const steps = [
    { icon: Search, title: "Search a building or tap the map", text: "Try a code like DC, SLC or MC." },
    { icon: Accessibility, title: "Choose how you get around", text: "Routes avoid what you can't use." },
    { icon: Megaphone, title: "Spot a problem? Report it", text: "Ice, a blocked path or a broken elevator changes routes for everyone." },
  ];
  return (
    <div className="animate-fade-in">
      <h2 className="text-base font-bold text-stone-900">Accessible routes around UW</h2>
      <p className="mt-1 text-sm text-stone-600">
        Step-free and winter-safe walking routes on campus and uptown Waterloo, kept current by the people using them.
      </p>
      <ol className="mt-4 space-y-3">
        {steps.map(({ icon: Icon, title, text }) => (
          <li key={title} className="flex gap-3">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-brand-700">
              <Icon aria-hidden className="h-4 w-4" />
            </span>
            <span>
              <span className="block text-sm font-semibold text-stone-900">{title}</span>
              <span className="block text-xs text-stone-500">{text}</span>
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function Hint({ icon, text }: { icon: React.ReactNode; text: string }) {
  return (
    <p className="flex animate-fade-in items-center gap-2.5 rounded-xl bg-brand-50 px-3.5 py-3 text-sm text-brand-900">
      <span aria-hidden className="text-brand-700">
        {icon}
      </span>
      {text}
    </p>
  );
}

function LoadingCard() {
  return (
    <div className="animate-fade-in rounded-2xl border border-stone-200 bg-white p-4" role="status">
      <span className="sr-only">Finding a route…</span>
      <div className="h-7 w-24 animate-pulse rounded-md bg-stone-200" />
      <div className="mt-3 h-2 w-full animate-pulse rounded-full bg-stone-100" />
      <div className="mt-4 space-y-2">
        <div className="h-3 w-3/4 animate-pulse rounded bg-stone-100" />
        <div className="h-3 w-1/2 animate-pulse rounded bg-stone-100" />
      </div>
    </div>
  );
}

function ErrorCard({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="flex animate-fade-in gap-3 rounded-2xl border border-red-200 bg-red-50 p-4">
      <CircleAlert aria-hidden className="mt-0.5 h-5 w-5 shrink-0 text-red-700" />
      <div className="flex-1">
        <p className="text-sm font-semibold text-red-900">Couldn&apos;t find a route</p>
        <p className="mt-0.5 text-sm text-red-800">{message}</p>
        <button
          onClick={onRetry}
          className="mt-2 inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-sm font-medium text-red-900 ring-1 ring-red-200 transition-colors hover:bg-red-100"
        >
          <RefreshCw aria-hidden className="h-3.5 w-3.5" />
          Try again
        </button>
      </div>
    </div>
  );
}

const CONFIDENCE_ORDER: Confidence[] = ["verified", "inferred", "unknown"];

function RouteSummary({ route, onClear }: { route: RouteResponse; onClear: () => void }) {
  const total = route.distance_m || 1;
  // Floor, not round: 99.6% must not read as "100%" next to "4 m without details".
  const knownPct = Math.floor((100 * (route.confidence_m.verified + route.confidence_m.inferred)) / total);

  // "Things to know": built from the structured response, worst first. The API's own notes
  // are shown only for situations not covered here (e.g. "the nearest path is steps").
  const items: { tone: "danger" | "warn" | "info"; text: string }[] = [];
  for (const b of route.barriers ?? []) {
    items.push({ tone: "danger", text: `${b.description} (${formatDistance(b.length_m)}): ${b.reasons.map(humanizeReason).join(", ")}` });
  }
  const hazardTypes = [...new Set(route.segments.flatMap((s) => (s.hazards ?? []).map((h) => h.type)))];
  for (const t of hazardTypes) items.push({ tone: "warn", text: `${HAZARD_STYLE[t].label} reported on this route` });
  if (route.confidence_m.unknown >= 1) {
    items.push({ tone: "warn", text: `${formatDistance(route.confidence_m.unknown)} without accessibility details. Check as you go.` });
  }
  const noKerb = route.segments.filter((s) => s.description === "Crossing" && !s.kerb && !s.blocked).length;
  if (noKerb > 0) items.push({ tone: "info", text: `${noKerb} crossing${noKerb > 1 ? "s" : ""} without mapped kerb ramps` });
  const covered = ["No fully accessible route", "missing surface or kerb data", "have no kerb data"];
  for (const n of route.notes) if (!covered.some((c) => n.includes(c))) items.push({ tone: "info", text: n });

  return (
    <section className="animate-slide-up space-y-4" aria-label="Route">
      <div className="rounded-2xl border border-stone-200 bg-white p-4 shadow-sm">
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="text-3xl font-bold tracking-tight text-stone-900 tabular-nums">{formatDistance(route.distance_m)}</p>
            <p className="text-sm text-stone-500">walking distance</p>
          </div>
          {route.accessible ? (
            <span className="inline-flex items-center gap-1 rounded-full bg-brand-50 px-2.5 py-1 text-xs font-semibold text-brand-800">
              <CircleCheck aria-hidden className="h-3.5 w-3.5" />
              Accessible route
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2.5 py-1 text-xs font-semibold text-red-800">
              <CircleAlert aria-hidden className="h-3.5 w-3.5" />
              Has barriers
            </span>
          )}
        </div>

        {/* How much of the route the map actually describes: the honest part of the answer. */}
        <div className="mt-4">
          <div className="flex items-center gap-1 text-sm text-stone-700">
            <span>
              <span className="font-semibold">{knownPct}%</span> of this route has accessibility details
            </span>
            <InfoTip text="From OpenStreetMap: surfaces, kerbs and steps. Assumed stretches are sidewalks and roads, which are paved here." />
          </div>
          <div className="mt-2 flex h-1.5 overflow-hidden rounded-full bg-stone-100" aria-hidden>
            {CONFIDENCE_ORDER.map((c) => (
              <div key={c} style={{ width: `${(100 * route.confidence_m[c]) / total}%`, background: CONFIDENCE_COLOR[c] }} />
            ))}
          </div>
          <ul className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-stone-500">
            {CONFIDENCE_ORDER.filter((c) => route.confidence_m[c] > 0).map((c) => (
              <li key={c} className="flex items-center gap-1.5" title={CONFIDENCE_HELP[c]}>
                <LineSample confidence={c} />
                {CONFIDENCE_LABEL[c]} {formatDistance(route.confidence_m[c])}
              </li>
            ))}
          </ul>
        </div>
      </div>

      {items.length > 0 && (
        <div>
          <h2 className="mb-2 text-xs font-semibold tracking-wide text-stone-500 uppercase">Things to know</h2>
          <ul className="space-y-1.5">
            {items.map((it, i) => (
              <Callout key={i} tone={it.tone} text={it.text} />
            ))}
          </ul>
        </div>
      )}

      {/* Native <details>: keyboard and screen-reader friendly, and closed by default to keep
          the summary short. */}
      <details className="group rounded-2xl border border-stone-200 bg-white">
        <summary className="flex cursor-pointer list-none items-center justify-between rounded-2xl px-4 py-3 text-sm font-semibold text-stone-800 transition-colors hover:bg-stone-50">
          Route details
          <span className="flex items-center gap-2 text-xs font-normal text-stone-500">
            {route.segments.length} parts
            <span aria-hidden className="transition-transform group-open:rotate-180">
              ▾
            </span>
          </span>
        </summary>
        <div className="border-t border-stone-100 px-4 pb-2">
          <RouteSegmentList segments={route.segments} />
        </div>
      </details>

      <button
        onClick={onClear}
        className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm text-stone-600 transition-colors hover:bg-stone-100 hover:text-stone-900"
      >
        <X aria-hidden className="h-4 w-4" />
        Clear route
      </button>
    </section>
  );
}

function Callout({ tone, text }: { tone: "danger" | "warn" | "info"; text: string }) {
  const style = {
    danger: { box: "bg-red-50 text-red-900", icon: <CircleAlert className="h-4 w-4 text-red-700" /> },
    warn: { box: "bg-amber-50 text-amber-950", icon: <TriangleAlert className="h-4 w-4 text-amber-700" /> },
    info: { box: "bg-stone-100 text-stone-800", icon: <Info className="h-4 w-4 text-stone-500" /> },
  }[tone];
  return (
    <li className={`flex gap-2.5 rounded-xl px-3 py-2 text-sm ${style.box}`}>
      <span aria-hidden className="mt-0.5 shrink-0">
        {style.icon}
      </span>
      {text}
    </li>
  );
}

/** A tiny sample of how this confidence level is drawn on the map (dashed when not mapped). */
export function LineSample({ confidence, blocked }: { confidence: Confidence; blocked?: boolean }) {
  const color = blocked ? BLOCKED_COLOR : CONFIDENCE_COLOR[confidence];
  const dashed = confidence === "unknown" && !blocked;
  return (
    <span
      aria-hidden
      className="inline-block h-1 w-4 rounded-full"
      style={{ background: dashed ? `repeating-linear-gradient(90deg, ${color} 0 4px, transparent 4px 6px)` : color }}
    />
  );
}
