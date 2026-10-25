// k6 load test for the Passable API.
//
//   k6 run -e BASE=http://localhost:8080 -e RATE=200 -e DURATION=60s scripts/k6/load.js
//
// Scenarios (run concurrently):
//   routes   - constant arrival rate of route requests (RATE per second) between random pairs
//              of 60 on-path campus points (points.json), random profile. Pairs repeat, so
//              some requests hit the Redis route cache, like real traffic would.
//   hazards  - (HAZARDS=1, default on) one hazard report per second, each resolved 3 s later
//              by its reporter: keeps the overlay changing and invalidating the cache (new
//              graph version => new cache keys) while routes are being served.
//
// Run the API with -rate-limit=false: this test deliberately exceeds per-IP limits.

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE = __ENV.BASE || "http://localhost:8080";
const RATE = Number(__ENV.RATE || 200);
const DURATION = __ENV.DURATION || "60s";
const POINTS = JSON.parse(open("./points.json")); // [[lat, lng], ...]
const PROFILES = ["wheelchair", "stroller", "walk_winter"];

// Custom metrics: cache hit rate, and latency split by hit/miss (a hit skips snapping and A*).
const cacheHits = new Rate("route_cache_hit");
const missLatency = new Trend("route_miss_duration", true);
const hitLatency = new Trend("route_hit_duration", true);

const scenarios = {
  routes: {
    executor: "constant-arrival-rate",
    exec: "route",
    rate: RATE,
    timeUnit: "1s",
    duration: DURATION,
    preAllocatedVUs: 50,
    maxVUs: 400,
  },
};
if ((__ENV.HAZARDS || "1") === "1") {
  scenarios.hazards = {
    executor: "constant-arrival-rate",
    exec: "hazard",
    rate: 1,
    timeUnit: "1s",
    duration: DURATION,
    preAllocatedVUs: 5,
    maxVUs: 10,
  };
}

export const options = {
  scenarios,
  thresholds: {
    "http_req_failed{scenario:routes}": ["rate<0.01"], // < 1% errors
    "http_req_duration{scenario:routes}": ["p(95)<200"], // p95 under 200 ms
  },
  summaryTrendStats: ["avg", "med", "p(90)", "p(95)", "p(99)", "max"],
};

// setup runs once: one anonymous session per hazard VU.
export function setup() {
  const sessions = [];
  for (let i = 0; i < 10; i++) {
    sessions.push(http.post(`${BASE}/api/v1/session`).json("session_id"));
  }
  return { sessions };
}

const pick = (arr) => arr[Math.floor(Math.random() * arr.length)];

export function route() {
  const a = pick(POINTS);
  let b = pick(POINTS);
  while (b === a) b = pick(POINTS);
  const res = http.get(`${BASE}/api/v1/route?from=${a[0]},${a[1]}&to=${b[0]},${b[1]}&profile=${pick(PROFILES)}`, {
    tags: { name: "route" },
  });
  check(res, { "route 200": (r) => r.status === 200 });
  const hit = res.headers["X-Cache"] === "HIT";
  cacheHits.add(hit);
  (hit ? hitLatency : missLatency).add(res.timings.duration);
}

export function hazard(data) {
  const sid = data.sessions[__VU % data.sessions.length];
  const p = pick(POINTS);
  const headers = { "Content-Type": "application/json", "X-Session-ID": sid };
  const res = http.post(`${BASE}/api/v1/hazards`, JSON.stringify({ lat: p[0], lng: p[1], type: "blocked_sidewalk", note: "k6 load test" }), {
    headers,
    tags: { name: "hazard-create" },
  });
  check(res, { "hazard 201": (r) => r.status === 201 });
  if (res.status !== 201) return;
  sleep(3);
  http.post(`${BASE}/api/v1/hazards/${res.json("id")}/resolve`, null, { headers, tags: { name: "hazard-resolve" } });
}
