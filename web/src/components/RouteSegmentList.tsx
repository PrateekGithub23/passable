"use client";

// RouteSegmentList: the route as a readable list, one row per segment returned by the API
// (consecutive edges with the same description, surface, kerb and confidence are already
// merged server-side). The coloured bar on each row matches the line colour on the map.

import type { Segment } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR, CONFIDENCE_LABEL, formatDistance } from "@/lib/style";

export default function RouteSegmentList({ segments }: { segments: Segment[] }) {
  return (
    <ol className="divide-y divide-gray-100">
      {segments.map((s, i) => {
        const color = s.blocked ? BLOCKED_COLOR : CONFIDENCE_COLOR[s.confidence];
        // Small facts that matter for accessibility, shown under the title when known.
        const details = [s.surface && `surface: ${s.surface}`, s.kerb && `kerb: ${s.kerb}`].filter(Boolean);
        return (
          // Index keys are fine: the list is replaced wholesale for each new route, never reordered.
          <li key={i} className="flex gap-3 py-2">
            <span
              aria-hidden
              className="mt-1 w-1.5 shrink-0 rounded-full"
              style={{
                // Unknown segments are dashed on the map; mimic that with a striped bar.
                background:
                  s.confidence === "unknown" && !s.blocked
                    ? `repeating-linear-gradient(${color} 0 6px, transparent 6px 10px)`
                    : color,
              }}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2">
                <p className="truncate text-sm font-medium text-gray-900">
                  {s.description}
                  {s.name && <span className="font-normal text-gray-600"> · {s.name}</span>}
                </p>
                <span className="shrink-0 text-xs tabular-nums text-gray-600">{formatDistance(s.length_m)}</span>
              </div>
              <p className="text-xs text-gray-500">
                {s.blocked ? <span className="font-medium text-red-700">Not passable</span> : CONFIDENCE_LABEL[s.confidence]}
                {details.length > 0 && ` · ${details.join(" · ")}`}
              </p>
              {s.penalties && s.penalties.length > 0 && (
                <p className="text-xs text-amber-700">{s.penalties.join(", ")}</p>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
