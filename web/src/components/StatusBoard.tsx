"use client";

// StatusBoard: the /status page, for the technically curious (linked from About, not from the
// main app). Polls GET /api/v1/status every 5 s and shows whether the API is up, the live state
// (graph version, hazards, connections), routing latency from real traffic in the last 15
// minutes, and hazard propagation latency. Every number comes straight from the API.

import { Activity, ArrowLeft, Map as MapIcon, Radio, Route, Server } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { API_URL } from "@/lib/api";
import { LogoMark } from "./ui";

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

  const state = status && !error ? "up" : status === undefined && !error ? "checking" : "down";
  const banner = {
    up: { box: "bg-brand-50 text-brand-900 ring-brand-100", dot: "bg-brand-600", text: "All systems operational" },
    checking: { box: "bg-stone-100 text-stone-700 ring-stone-200", dot: "bg-stone-400", text: "Checking…" },
    down: { box: "bg-red-50 text-red-900 ring-red-100", dot: "bg-red-600", text: "The routing service isn't responding" },
  }[state];

  return (
    <div className="min-h-full bg-stone-50">
      <header className="border-b border-stone-200 bg-white">
        <div className="mx-auto flex max-w-3xl items-center justify-between px-5 py-4">
          <Link href="/" className="flex items-center gap-2.5 rounded-lg">
            <LogoMark className="h-8 w-8" />
            <span className="text-xl font-bold tracking-tight text-stone-900">Passable</span>
          </Link>
          <Link href="/" className="flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-brand-800 transition-colors hover:bg-brand-50">
            <ArrowLeft aria-hidden className="h-4 w-4" />
            Back to the map
          </Link>
        </div>
      </header>

      <main className="mx-auto max-w-3xl px-5 py-10">
        <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-stone-900">
          <Activity aria-hidden className="h-6 w-6 text-brand-700" />
          System status
        </h1>
        <p className="mt-1 text-sm text-stone-600">Live numbers from the routing service, refreshed every 5 seconds.</p>

        <div role="status" className={`mt-6 flex items-center gap-3 rounded-2xl p-4 ring-1 ${banner.box}`}>
          <span aria-hidden className="relative flex h-3 w-3">
            {state === "up" && <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-brand-500 opacity-50" />}
            <span className={`relative inline-flex h-3 w-3 rounded-full ${banner.dot}`} />
          </span>
          <span className="font-semibold">{banner.text}</span>
          {checkedAt && <span className="ml-auto text-xs opacity-70">checked {checkedAt.toLocaleTimeString()}</span>}
        </div>

        {status && (
          <div className="mt-6 grid gap-4 sm:grid-cols-2">
            <Card icon={Route} title="Routing, last 15 minutes">
              <Row label="Route requests" value={status.routes_15m.requests.toLocaleString()} />
              <Row label="Response time (typical / slow / slowest 1%)" value={`${status.routes_15m.p50_ms} / ${status.routes_15m.p95_ms} / ${status.routes_15m.p99_ms} ms`} />
              <Row label="Served from cache" value={`${Math.round(status.routes_15m.cache_hit_rate * 100)}%`} />
              <Row label="Path junctions searched per route" value={status.routes_15m.avg_nodes_expanded.toLocaleString()} />
            </Card>
            <Card icon={Radio} title="Live hazard reports">
              <Row label="Active reports" value={status.graph.live_hazards} />
              <Row
                label="Report → open maps (typical / slow)"
                value={status.propagation.samples ? `${status.propagation.p50_ms} / ${status.propagation.p95_ms} ms` : "no reports yet"}
              />
              <Row label="Open live connections" value={status.ws_clients} />
              <Row label="Graph version" value={status.graph.version} />
            </Card>
            <Card icon={MapIcon} title="Map data">
              <Row label="Path junctions" value={status.graph.nodes.toLocaleString()} />
              <Row label="Path segments" value={status.graph.edges.toLocaleString()} />
            </Card>
            <Card icon={Server} title="Server">
              <Row label="Up for" value={formatUptime(status.uptime_s)} />
              <Row label="Reported at" value={new Date(status.generated_at).toLocaleTimeString()} />
            </Card>
          </div>
        )}
      </main>
    </div>
  );
}

function Card({ icon: Icon, title, children }: { icon: React.ComponentType<{ className?: string }>; title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-stone-200 bg-white p-5">
      <h2 className="mb-3 flex items-center gap-2 text-sm font-bold text-stone-900">
        <Icon aria-hidden className="h-4 w-4 text-brand-700" />
        {title}
      </h2>
      <dl className="space-y-2">{children}</dl>
    </section>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 text-sm">
      <dt className="text-stone-600">{label}</dt>
      <dd className="font-semibold text-stone-900 tabular-nums">{value}</dd>
    </div>
  );
}

function formatUptime(s: number): string {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : m ? `${m} min` : `${s} s`;
}
