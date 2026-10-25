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
  hazards?: HazardRef[]; // live hazard reports on this segment
  edge_ids: number[];
  coordinates: LngLat[];
}

export type HazardType = "elevator_out" | "blocked_sidewalk" | "construction" | "ice_snow" | "missing_curb_ramp";
export type HazardStatus = "active" | "confirmed" | "resolved" | "expired";

/** A live hazard report affecting a route segment. */
export interface HazardRef {
  id: number;
  type: HazardType;
  status: HazardStatus;
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
  graph_version: number; // hazard event sequence the route was computed at; bumps on every hazard change
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

/** A hazard report (GET /api/v1/hazards, and the payload of live WebSocket events). */
export interface Hazard {
  id: number;
  edge_id: number;
  type: HazardType;
  severity: "minor" | "major";
  status: HazardStatus;
  note?: string;
  point: LngLat;
  confirm_count: number;
  resolve_count: number;
  created_at: string;
  expires_at: string;
  demo: boolean; // filed by the demo simulator, not a real person
}

/** [west, south, east, north] */
export type BBox = [number, number, number, number];

/** Live (active or confirmed) hazards inside bbox, plus the graph version the list reflects. */
export async function fetchHazards(bbox: BBox, signal?: AbortSignal): Promise<{ hazards: Hazard[]; graph_version: number }> {
  const res = await fetch(`${API_URL}/api/v1/hazards?bbox=${bbox.join(",")}`, { signal });
  const body = await res.json();
  if (!res.ok) throw new ApiError(body.error ?? `Request failed (${res.status})`);
  return body;
}

/** WebSocket endpoint: same host as the API, ws:// or wss:// instead of http:// or https://. */
export const WS_URL = API_URL.replace(/^http/, "ws") + "/ws";

// ---- Writes (need an anonymous session) ----

const SESSION_KEY = "passable.session";
let sessionPromise: Promise<string> | null = null;

/**
 * The anonymous session id the API uses to count one vote per person per report. Created on
 * first use with POST /api/v1/session and kept in localStorage, so a returning visitor is
 * the same "person" and can't confirm the same report twice by reloading.
 */
function getSession(): Promise<string> {
  if (!sessionPromise) {
    sessionPromise = (async () => {
      try {
        const saved = localStorage.getItem(SESSION_KEY);
        if (saved) return saved;
      } catch {
        // storage blocked (private mode): fall through to a session for this page only
      }
      const res = await fetch(`${API_URL}/api/v1/session`, { method: "POST" });
      const body = await res.json();
      if (!res.ok) throw new ApiError(body.error ?? "Could not start a session");
      try {
        localStorage.setItem(SESSION_KEY, body.session_id);
      } catch {}
      return body.session_id as string;
    })();
    sessionPromise.catch(() => (sessionPromise = null)); // let the next call retry
  }
  return sessionPromise;
}

/** POST with the session header. If the server no longer knows our session (e.g. the
 *  database was reset), get a fresh one and retry once. */
async function postWithSession<T>(path: string, body?: object): Promise<T> {
  for (let attempt = 0; attempt < 2; attempt++) {
    const sid = await getSession();
    const res = await fetch(`${API_URL}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Session-ID": sid },
      body: body ? JSON.stringify(body) : undefined,
    });
    const json = await res.json();
    if (res.status === 401 && attempt === 0) {
      try {
        localStorage.removeItem(SESSION_KEY);
      } catch {}
      sessionPromise = null;
      continue;
    }
    if (!res.ok) throw new ApiError(json.error ?? `Request failed (${res.status})`);
    return json as T;
  }
  throw new ApiError("Could not start a session");
}

export interface NewHazard {
  lat: number;
  lng: number;
  type: HazardType;
  severity: "minor" | "major";
  note?: string;
}

/** File a hazard report. The server attaches it to the nearest path (within 30 m). */
export const createHazard = (h: NewHazard) => postWithSession<Hazard>("/api/v1/hazards", h);

/** "Still there" (confirm) or "It's gone" (resolve). */
export const voteHazard = (id: number, kind: "confirm" | "resolve") =>
  postWithSession<Hazard>(`/api/v1/hazards/${id}/${kind}`);
