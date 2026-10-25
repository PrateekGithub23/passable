"use client";

// RouteSegmentList: the route as a vertical timeline, one row per part. The API has already
// merged consecutive edges that look the same. Each row's line uses the same colour and
// dashing as the route on the map, so the list and the map read as one thing.

import { DoorStairwell, Footprints, Road } from "lucide-react";

import type { Segment } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR, CONFIDENCE_LABEL, HAZARD_STYLE, formatDistance, humanizeReason, humanizeSurface } from "@/lib/style";

export default function RouteSegmentList({ segments }: { segments: Segment[] }) {
  return (
    <ol className="pt-1">
      {segments.map((s, i) => {
        const color = s.blocked ? BLOCKED_COLOR : CONFIDENCE_COLOR[s.confidence];
        const dashed = s.confidence === "unknown" && !s.blocked;
        // Title: the path's real name when mapped ("Two Row Path"), otherwise what it is.
        const title = s.name || s.description;
        const facts = [
          s.name ? s.description : null,
          s.surface ? humanizeSurface(s.surface) : null,
          s.kerb ? `${humanizeSurface(s.kerb)} kerb` : null,
        ].filter(Boolean);
        // Reasons that matter to a person; the surface is already in `facts`.
        const caveats = (s.penalties ?? [])
          .filter((r) => !(r.startsWith("surface ") && r !== "surface unknown")) // e.g. "surface gravel" is already shown
          .map(humanizeReason);
        const Icon = s.description === "Steps" ? DoorStairwell : s.description === "Crossing" ? Footprints : s.description.startsWith("Road") ? Road : null;
        return (
          // Index keys are fine: the list is replaced wholesale for each new route, never reordered.
          <li key={i} className="flex gap-3">
            {/* The timeline line, drawn like the map line. */}
            <span aria-hidden className="relative flex w-3 shrink-0 justify-center">
              <span
                className="absolute top-0 bottom-0 w-1 rounded-full"
                style={{ background: dashed ? `repeating-linear-gradient(${color} 0 6px, transparent 6px 10px)` : color }}
              />
            </span>
            <div className="min-w-0 flex-1 border-b border-stone-100 py-2.5 last:border-0">
              <div className="flex items-baseline justify-between gap-2">
                <p className="flex min-w-0 items-center gap-1.5 text-sm font-medium text-stone-900">
                  {Icon && <Icon aria-hidden className="h-3.5 w-3.5 shrink-0 text-stone-500" />}
                  <span className="truncate">{title}</span>
                </p>
                <span className="shrink-0 text-xs text-stone-500 tabular-nums">{formatDistance(s.length_m)}</span>
              </div>
              <p className="text-xs text-stone-500">
                {s.blocked ? (
                  <span className="font-semibold text-red-700">Can&apos;t pass with this profile</span>
                ) : (
                  CONFIDENCE_LABEL[s.confidence]
                )}
                {facts.length > 0 && ` · ${facts.join(" · ")}`}
              </p>
              {caveats.length > 0 && <p className="mt-0.5 text-xs text-amber-800">{caveats.join(" · ")}</p>}
              {s.hazards?.map((h) => (
                <p key={h.id} className="mt-0.5 text-xs font-medium" style={{ color: HAZARD_STYLE[h.type].color }}>
                  Reported: {HAZARD_STYLE[h.type].label.toLowerCase()}
                </p>
              ))}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
