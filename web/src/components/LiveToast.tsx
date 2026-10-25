"use client";

// LiveToast: the "route affected, reroute?" prompt. Shown when the server sends route.affected,
// i.e. someone just reported a hazard on a part of the route this tab is showing, and that
// hazard matters for the selected profile. The user decides whether to reroute; we never swap
// the route under them without asking.

import { Route } from "lucide-react";

import type { RouteAffected } from "@/lib/useLive";
import { HAZARD_STYLE } from "@/lib/style";

interface Props {
  affected: RouteAffected;
  onReroute: () => void;
  onDismiss: () => void;
}

export default function LiveToast({ affected, onReroute, onDismiss }: Props) {
  const { label, color, icon: Icon } = HAZARD_STYLE[affected.hazard.type];
  return (
    // role="alert" makes screen readers announce it immediately, like a sighted user noticing it.
    <div
      role="alert"
      className="pointer-events-auto flex w-[min(28rem,calc(100vw-1.5rem))] animate-slide-up items-start gap-3 rounded-2xl bg-white p-4 shadow-xl ring-1 ring-stone-900/5"
    >
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-white" style={{ background: color }}>
        <Icon aria-hidden className="h-4.5 w-4.5" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-1.5 text-sm font-bold text-stone-900">
          {label} reported on your route
          {affected.hazard.demo && (
            <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-amber-800 uppercase">Demo</span>
          )}
        </p>
        {affected.hazard.note && <p className="mt-0.5 truncate text-sm text-stone-600">“{affected.hazard.note}”</p>}
        <div className="mt-3 flex gap-2">
          <button
            onClick={onReroute}
            className="flex items-center gap-1.5 rounded-xl bg-brand-700 px-3.5 py-2 text-sm font-semibold text-white transition-colors hover:bg-brand-800"
          >
            <Route aria-hidden className="h-4 w-4" />
            Reroute
          </button>
          <button onClick={onDismiss} className="rounded-xl px-3 py-2 text-sm font-medium text-stone-600 transition-colors hover:bg-stone-100">
            Dismiss
          </button>
        </div>
      </div>
    </div>
  );
}
