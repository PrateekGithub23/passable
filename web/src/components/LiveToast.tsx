"use client";

// LiveToast: the "route affected, reroute?" prompt. Shown when the server sends route.affected,
// i.e. someone just reported a hazard on an edge of the route this tab is showing, and that
// hazard matters for the selected profile. The user decides whether to reroute; we never swap
// the route under them without asking.

import type { RouteAffected } from "@/lib/useLive";
import { HAZARD_STYLE } from "@/lib/style";

interface Props {
  affected: RouteAffected;
  onReroute: () => void;
  onDismiss: () => void;
}

export default function LiveToast({ affected, onReroute, onDismiss }: Props) {
  const { label, color } = HAZARD_STYLE[affected.hazard.type];
  return (
    // role="alert" makes screen readers announce it immediately, like a sighted user noticing it.
    <div
      role="alert"
      className="pointer-events-auto flex w-[min(28rem,calc(100vw-2rem))] items-start gap-3 rounded-xl bg-white p-4 shadow-lg ring-1 ring-black/10"
    >
      <span aria-hidden className="mt-1 h-3 w-3 shrink-0 rounded-full" style={{ background: color }} />
      <div className="flex-1">
        <p className="text-sm font-semibold text-gray-900">
          {label} reported on your route
          {affected.hazard.demo && (
            <span className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800">Demo</span>
          )}
        </p>
        {affected.hazard.note && <p className="text-sm text-gray-600">“{affected.hazard.note}”</p>}
        <div className="mt-3 flex gap-2">
          <button onClick={onReroute} className="rounded-lg bg-blue-700 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-800">
            Reroute
          </button>
          <button onClick={onDismiss} className="rounded-lg px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100">
            Dismiss
          </button>
        </div>
      </div>
    </div>
  );
}
