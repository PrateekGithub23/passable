// /about: what Passable is, how it decides routes, where the data comes from, what it can't do,
// and what it stores. One page for "about" and "limitations": the limitations are part of
// understanding the product, so they sit in a prominent card rather than a separate page.
// Static content (a Server Component, no client JS). Numbers are the measured ones from the
// README's "Data coverage" section; update both together.

import {
  Accessibility,
  ArrowLeft,
  Baby,
  Activity,
  Database,
  Lock,
  Megaphone,
  Route,
  Snowflake,
  TriangleAlert,
} from "lucide-react";
import type { Metadata } from "next";
import Link from "next/link";

import { LogoMark } from "@/components/ui";
import { CONFIDENCE_COLOR } from "@/lib/style";

export const metadata: Metadata = { title: "About · Passable" };

export default function AboutPage() {
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

      <main className="mx-auto max-w-3xl space-y-10 px-5 py-10 text-[15px] leading-relaxed text-stone-700">
        <section>
          <h1 className="text-3xl font-bold tracking-tight text-stone-900">Routes that work for the way you get around</h1>
          <p className="mt-3 max-w-2xl">
            Passable finds walking routes around the University of Waterloo campus and uptown Waterloo for people who
            can&apos;t just take the shortest path: wheelchair users, people with strollers, and anyone walking in winter.
            When someone reports a problem, such as a blocked sidewalk, an elevator that&apos;s out or an icy ramp, routes
            change for everyone within seconds.
          </p>
        </section>

        <Section icon={Route} title="How routes are chosen">
          <div className="grid gap-3 sm:grid-cols-3">
            {[
              { icon: Accessibility, name: "Wheelchair", text: "Never uses steps, raised kerbs or rough ground like gravel or grass." },
              { icon: Baby, name: "Stroller", text: "Never uses steps. Rough surfaces and kerbs are avoided where possible." },
              { icon: Snowflake, name: "Winter", text: "Avoids steps and unpaved paths, and reacts most strongly to ice reports." },
            ].map(({ icon: Icon, name, text }) => (
              <div key={name} className="rounded-2xl border border-stone-200 bg-white p-4">
                <Icon aria-hidden className="h-5 w-5 text-brand-700" />
                <p className="mt-2 font-semibold text-stone-900">{name}</p>
                <p className="mt-1 text-sm">{text}</p>
              </div>
            ))}
          </div>
          <p className="mt-4">
            When no fully accessible route exists, Passable still shows the one with the fewest barriers and says exactly
            what they are, rather than just &quot;no route&quot;.
          </p>
          <ul className="mt-4 space-y-2 text-sm">
            {(
              [
                ["verified", "Mapped", "the map data describes the surface, kerbs and steps."],
                ["inferred", "Assumed", "not mapped, but almost certainly fine (sidewalks and roads here are paved)."],
                ["unknown", "Not mapped (dashed)", "no accessibility details. Check as you go."],
              ] as const
            ).map(([c, name, text]) => (
              <li key={c} className="flex items-center gap-3">
                <span
                  aria-hidden
                  className="inline-block h-1.5 w-8 shrink-0 rounded-full"
                  style={{
                    background: c === "unknown" ? `repeating-linear-gradient(90deg, ${CONFIDENCE_COLOR[c]} 0 6px, transparent 6px 9px)` : CONFIDENCE_COLOR[c],
                  }}
                />
                <span>
                  <b className="text-stone-900">{name}:</b> {text}
                </span>
              </li>
            ))}
          </ul>
        </Section>

        <Section icon={Megaphone} title="Reporting problems">
          <p>
            Anyone can report a problem, with no account needed. A report starts as <i>reported</i>, becomes{" "}
            <i>confirmed</i> when two other people say &quot;still there&quot;, and disappears when people say &quot;it&apos;s
            gone&quot; or when it expires on its own: ice after 12 hours, construction after a week. Each confirmation resets
            the clock. Reports marked <span className="rounded bg-amber-100 px-1 text-xs font-semibold text-amber-800 uppercase">Demo</span>{" "}
            are simulated, for demonstrations.
          </p>
        </Section>

        {/* Limitations: prominent on purpose. Trust comes from saying what the app can't do. */}
        <section className="rounded-3xl border border-amber-200 bg-amber-50 p-6" aria-labelledby="limits">
          <h2 id="limits" className="flex items-center gap-2 text-lg font-bold text-amber-950">
            <TriangleAlert aria-hidden className="h-5 w-5 text-amber-700" />
            Limitations
          </h2>
          <ul className="mt-3 space-y-2 text-amber-950">
            <li>
              <b>Slopes aren&apos;t considered.</b> Elevation isn&apos;t in the data, so a steep path can be suggested to a
              wheelchair user.
            </li>
            <li>
              <b>Indoor routes are mostly missing.</b> Only 8 elevators are mapped, and indoor corridors usually aren&apos;t
              connected to the outdoor paths.
            </li>
            <li>
              <b>Crossings are recognised only where they&apos;re mapped as their own path.</b>
            </li>
            <li>
              <b>Reports come from people.</b> They&apos;re unverified until others confirm them.
            </li>
            <li>Only the UW campus and uptown Waterloo area is covered.</li>
          </ul>
        </section>

        <Section icon={Database} title="Where the map data comes from">
          <p>
            Paths, sidewalks, crossings, steps, kerbs and surfaces come from{" "}
            <a href="https://www.openstreetmap.org/copyright" className="font-medium text-brand-800 underline-offset-2 hover:underline">
              OpenStreetMap
            </a>{" "}
            (© OpenStreetMap contributors, ODbL). The basemap is from{" "}
            <a href="https://openfreemap.org" className="font-medium text-brand-800 underline-offset-2 hover:underline">
              OpenFreeMap
            </a>
            . Coverage in this area, measured when the data was imported:
          </p>
          <ul className="mt-3 list-disc space-y-1 pl-5">
            <li>About 70% of footpaths have a surface type. The rest are shown as assumed or not mapped.</li>
            <li>1,277 mapped kerbs, 246 sets of steps and 2,167 crossing segments.</li>
            <li>Smoothness, slope and explicit wheelchair tags are on fewer than 2% of paths.</li>
          </ul>
        </Section>

        <Section icon={Lock} title="Privacy">
          <p>
            There are no accounts. To count one vote per person, your browser gets a random anonymous ID, stored on your
            device. A report stores where and what it is, plus that anonymous ID. Route requests are logged without any
            identifier (profile, distance and timing only) to measure performance. Your IP address is used for rate limiting
            (to stop spam) and is kept only in short-lived counters that expire within minutes.
          </p>
        </Section>

        <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-stone-200 pt-6 text-sm text-stone-500">
          <span>Built as a student project.</span>
          {/* Status is for the technically curious (latency, uptime), not part of using the app. */}
          <Link href="/status" className="flex items-center gap-1.5 underline-offset-2 hover:text-stone-800 hover:underline">
            <Activity aria-hidden className="h-4 w-4" />
            System status (technical)
          </Link>
        </footer>
      </main>
    </div>
  );
}

function Section({ icon: Icon, title, children }: { icon: React.ComponentType<{ className?: string }>; title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-3 flex items-center gap-2 text-lg font-bold text-stone-900">
        <Icon aria-hidden className="h-5 w-5 text-brand-700" />
        {title}
      </h2>
      {children}
    </section>
  );
}
