// Typed client for the Go API (cmd/api).
//
// The types below mirror the JSON the Go handlers produce (internal/api/route.go). Keeping
// them in one file means a change to the API shape only has to be reflected here, and the
// TypeScript compiler then points at every component that uses the changed field.

// Base URL of the Go API. NEXT_PUBLIC_* variables are inlined into the browser bundle at
// build time, so production builds must set it (e.g. on Vercel) before `next build`.
export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export type Profile = "wheelchair" | "stroller" | "walk_winter";

// How much of an edge's accessibility data came from explicit OSM tags (see DECISIONS.md #9).
export type Confidence = "verified" | "inferred" | "unknown";

/** A [longitude, latitude] pair, the order GeoJSON and MapLibre use. */
export type LngLat = [number, number];

/** A run of consecutive edges with the same description, surface, kerb and confidence. */
export interface Segment {
  description: string; // e.g. "Sidewalk", "Crossing", "Steps"
  name?: string; // OSM street/path name, when tagged
  highway: string;
  surface?: string;
  kerb?: string;
  length_m: number;
  confidence: Confidence;
  penalties?: string[]; // factors that raised the cost, e.g. "surface unknown"
  blocked?: boolean; // only true on "no accessible route" responses
  edge_ids: number[];
  coordinates: LngLat[];
}

/** Something on the route this profile cannot pass (steps for a wheelchair, ...). */
export interface Barrier {
  description: string;
  reasons: string[];
  length_m: number;
  point: LngLat;
  edge_ids: number[];
}

export interface SnapInfo {
  edge_id: number;
  distance_m: number; // how far the clicked point was from the path the route starts on
  point: LngLat;
}

export interface RouteResponse {
  profile: Profile;
  accessible: boolean; // false => the route shown crosses `barriers`
  distance_m: number;
  cost?: number;
  nodes_expanded: number;
  search_ms: number;
  snap: { from: SnapInfo; to: SnapInfo };
  confidence_m: Record<Confidence, number>;
  notes: string[];
  barriers?: Barrier[];
  segments: Segment[];
}

/** Error responses are `{"error": "..."}`; we surface that message to the user. */
export class ApiError extends Error {}

/**
 * Fetch a route. `signal` lets the caller cancel a request that is no longer wanted, e.g.
 * when the user clicks a new point before the previous route came back; otherwise a slow
 * old response could overwrite a newer one.
 */
export async function fetchRoute(
  from: LngLat,
  to: LngLat,
  profile: Profile,
  signal?: AbortSignal,
): Promise<RouteResponse> {
  // The API takes "lat,lng" (human order), while we store [lng, lat] (GeoJSON order).
  const params = new URLSearchParams({
    from: `${from[1]},${from[0]}`,
    to: `${to[1]},${to[0]}`,
    profile,
  });
  const res = await fetch(`${API_URL}/api/v1/route?${params}`, { signal });
  const body = await res.json();
  if (!res.ok) {
    throw new ApiError(body.error ?? `Request failed (${res.status})`);
  }
  return body as RouteResponse;
}
