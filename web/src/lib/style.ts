// Shared visual vocabulary: colours, icons and the plain-language wording the whole UI uses,
// so a list entry, a map line and a legend always say the same thing in the same colour.

import {
  Accessibility,
  ArrowUpDown,
  Baby,
  Ban,
  Construction,
  Snowflake,
  TriangleAlert,
  type LucideIcon,
} from "lucide-react";

import type { Confidence, HazardType, Profile } from "./api";

// ---- Route lines -----------------------------------------------------------------------

export const CONFIDENCE_COLOR: Record<Confidence, string> = {
  verified: "#0f766e", // brand-700: accessibility details are in the map data
  inferred: "#14b8a6", // brand-500: not mapped, but safely assumed (e.g. paved sidewalk)
  unknown: "#d97706", // amber-600: not mapped; drawn dashed on the map
};

// Barriers (only on "no accessible route" responses) read as "stop".
export const BLOCKED_COLOR = "#dc2626";

/** User-facing names for the API's confidence labels: no jargon like "inferred". */
export const CONFIDENCE_LABEL: Record<Confidence, string> = {
  verified: "Mapped",
  inferred: "Assumed",
  unknown: "Not mapped",
};

export const CONFIDENCE_HELP: Record<Confidence, string> = {
  verified: "The map data describes the surface, kerbs and steps here.",
  inferred: "Not mapped, but almost certainly fine. Sidewalks and roads here are paved.",
  unknown: "The map has no accessibility details here. Check as you go.",
};

// ---- Profiles --------------------------------------------------------------------------

export const PROFILES: { id: Profile; label: string; hint: string; icon: LucideIcon }[] = [
  { id: "wheelchair", label: "Wheelchair", hint: "No steps or raised kerbs. Avoids rough ground.", icon: Accessibility },
  { id: "stroller", label: "Stroller", hint: "No steps. Prefers smooth paths.", icon: Baby },
  { id: "walk_winter", label: "Winter", hint: "Avoids steps, ice and unpaved paths.", icon: Snowflake },
];

// ---- Hazards ---------------------------------------------------------------------------

// `hint` is the one-line explanation shown in the report form, written for someone who has
// never seen the app. `blocks` is the default answer to "can people get past it?".
export const HAZARD_STYLE: Record<
  HazardType,
  { label: string; color: string; hint: string; blocks: boolean; icon: LucideIcon }
> = {
  blocked_sidewalk: { label: "Blocked sidewalk", color: "#dc2626", hint: "Something is in the way: fence, vehicle, snowbank", blocks: true, icon: Ban },
  ice_snow: { label: "Ice or snow", color: "#0284c7", hint: "Slippery or not cleared", blocks: false, icon: Snowflake },
  construction: { label: "Construction", color: "#ea580c", hint: "Path dug up or fenced off for work", blocks: true, icon: Construction },
  elevator_out: { label: "Elevator out", color: "#7c3aed", hint: "An elevator or lift isn't working", blocks: true, icon: ArrowUpDown },
  missing_curb_ramp: { label: "No curb ramp", color: "#be185d", hint: "No ramp where the sidewalk meets the road", blocks: true, icon: TriangleAlert },
};

// ---- Wording helpers -------------------------------------------------------------------

/** 1234 -> "1.2 km", 87.4 -> "87 m". */
export function formatDistance(m: number): string {
  return m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${Math.round(m)} m`;
}

/** OSM surface values -> words: "paving_stones" -> "Paving stones", "concrete:plates" -> "Concrete plates". */
export function humanizeSurface(s: string): string {
  const t = s.replace(/[_:]/g, " ");
  return t.charAt(0).toUpperCase() + t.slice(1);
}

/**
 * The API explains costs with short internal reasons (see internal/cost). Turn each into
 * something a pedestrian would say. Unknown strings pass through unchanged.
 */
export function humanizeReason(r: string): string {
  const reported = r.match(/^reported (.+) \(#\d+\)$/);
  if (reported) return `Reported: ${reported[1]}`;
  if (r.startsWith("surface ") && r !== "surface unknown") return `${humanizeSurface(r.slice(8))} surface`;
  const map: Record<string, string> = {
    steps: "Steps",
    "tagged wheelchair=no": "Marked not wheelchair accessible",
    "surface unknown": "Surface not mapped",
    "smoothness intermediate": "Uneven surface",
    "raised kerb": "Raised kerb",
    "kerb of unknown height": "Kerb height not mapped",
    "crossing without kerb data": "Kerb ramps not mapped",
    "road with separately mapped sidewalk": "On the road (a sidewalk runs alongside)",
    "walking along a major road": "Along a busy road",
    "road without sidewalk data": "Road, sidewalk not mapped",
  };
  if (map[r]) return map[r];
  if (r.startsWith("smoothness ")) return "Rough surface";
  return r;
}
