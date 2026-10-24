// Shared visual vocabulary: the same colours and labels are used on the map lines, the
// legend and the segment list, so a user can match a list entry to the line on the map.

import type { Confidence, Profile } from "./api";

export const CONFIDENCE_COLOR: Record<Confidence, string> = {
  verified: "#1d4ed8", // blue-700: tags present
  inferred: "#60a5fa", // blue-400: filled in from context (e.g. sidewalks are paved)
  unknown: "#d97706", // amber-600: data missing; drawn dashed on the map
};

// Barriers (only on "no accessible route" responses) get a colour that reads as "stop".
export const BLOCKED_COLOR = "#dc2626"; // red-600

export const CONFIDENCE_LABEL: Record<Confidence, string> = {
  verified: "Verified",
  inferred: "Inferred",
  unknown: "Unknown",
};

export const PROFILES: { id: Profile; label: string; hint: string }[] = [
  { id: "wheelchair", label: "Wheelchair", hint: "No steps or raised kerbs; avoids rough surfaces" },
  { id: "stroller", label: "Stroller", hint: "No steps; mild penalties for rough surfaces" },
  { id: "walk_winter", label: "Winter walk", hint: "Steps allowed but penalized; avoids unpaved paths" },
];

/** 1234 -> "1.2 km", 87.4 -> "87 m". */
export function formatDistance(m: number): string {
  return m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${Math.round(m)} m`;
}
