"use client";

// StatusBoard: the public /status page. Polls GET /api/v1/status every 5 s and shows whether
// the API is up, the live state (graph version, hazards, connected clients), routing
// latency from real traffic in the last 15 minutes, and hazard propagation latency.

import Link from "next/link";
import { useEffect, useState } from "react";

import { API_URL } from "@/lib/api";

interface Status {
  uptime_s: number;
  graph: { nodes: number; edges: number; version: number; live_hazards: number };
  ws_clients: number;
  routes_15m: { requests: number; p50_ms: number; p95_ms: number; p99_ms: number; cache_hit_rate: number; avg_nodes_expanded: number };
  propagation: { samples: number; p50_ms: number; p95_ms: number };
  generated_at: string;
}

const POLL_MS = 5000;

export default function StatusBoard() {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState(false);
  const [checkedAt, setCheckedAt] = useState<Date>();

  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const res = await fetch(`${API_URL}/api/v1/status`, { cache: "no-store" });
        if (!res.ok) throw new Error(String(res.status));
        const body: Status = await res.json();
        if (!cancelled) {
          setStatus(body);
          setError(false);
        }
      } catch {
        if (!cancelled) setError(true);
      }
      if (!cancelled) setCheckedAt(new Date());
    };
    poll();
    const id = setInterval(poll, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const up = status && !error;
  return (
    <main className="mx-auto max-w-3xl p-6">
      <header className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-gray-900">Passable status</h1>
          <p className="text-sm text-gray-600">Live numbers from the API, refreshed every 5 seconds.</p>
        </div>
        <Link href="/" className="text-sm text-blue-700 hover:underline">
          ← Back to the map
        </Link>
      </header>

      {/* Overall health: one sentence a non-engineer can read. */}
      <div
        role="status"
        className={`mb-6 flex items-center gap-3 rounded-xl p-4 ${up ? "bg-green-50 text-green-900" : status === undefined && !error ? "bg-gray-50 text-gray-700" : "bg-red-50 text-red-900"}`}
      >
        <span aria-hidden className={`h-3 w-3 rounded-full ${up ? "bg-green-600" : status === undefined && !error ? "bg-gray-400" : "bg-red-600"}`} />
        <span className="font-medium">
          {up ? "All systems operational" : status === undefined && !error ? "Checking…" : "The API is not responding"}
        </span>
        {checkedAt && <span className="ml-auto text-xs opacity-70">checked {checkedAt.toLocaleTimeString()}</span>}
      </div>

      {status && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Card title="Routing (last 15 min)">
            <Row label="Requests" value={status.routes_15m.requests.toLocaleString()} />
            <Row label="Latency p50 / p95 / p99" value={`${status.routes_15m.p50_ms} / ${status.routes_15m.p95_ms} / ${status.routes_15m.p99_ms} ms`} />
            <Row label="Cache hit rate" value={`${Math.round(status.routes_15m.cache_hit_rate * 100)}%`} />
            <Row label="Nodes searched per route" value={status.routes_15m.avg_nodes_expanded.toLocaleString()} />
          </Card>
          <Card title="Live hazards">
            <Row label="Active reports" value={status.graph.live_hazards} />
            <Row label="Graph version" value={status.graph.version} hint="Bumps on every hazard change" />
            <Row
              label="Report → map latency p50 / p95"
              value={status.propagation.samples ? `${status.propagation.p50_ms} / ${status.propagation.p95_ms} ms` : "no reports yet"}
              hint="From the report being saved to it being pushed to open maps"
            />
            <Row label="Open live connections" value={status.ws_clients} hint="On the server that answered" />
          </Card>
          <Card title="Map data">
            <Row label="Graph nodes" value={status.graph.nodes.toLocaleString()} />
            <Row label="Path segments" value={status.graph.edges.toLocaleString()} />
          </Card>
          <Card title="Server">
            <Row label="Up for" value={formatUptime(status.uptime_s)} />
            <Row label="Reported at" value={new Date(status.generated_at).toLocaleTimeString()} />
          </Card>
        </div>
      )}
    </main>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-gray-200 p-4">
      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-gray-500">{title}</h2>
      <dl className="space-y-1">{children}</dl>
    </section>
  );
}

function Row({ label, value, hint }: { label: string; value: React.ReactNode; hint?: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 text-sm" title={hint}>
      <dt className="text-gray-600">{label}</dt>
      <dd className="font-medium tabular-nums text-gray-900">{value}</dd>
    </div>
  );
}

function formatUptime(s: number): string {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : m ? `${m} min` : `${s} s`;
}
