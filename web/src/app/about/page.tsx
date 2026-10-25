// /about: what Passable is, where its data comes from, how it decides routes, and what it
// can't do. Static content (a Server Component, no client JS). Numbers are the measured ones
// from the README's "Data coverage" section; update both together.

import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = { title: "About · Passable" };

export default function AboutPage() {
  return (
    <main className="mx-auto max-w-3xl p-6 text-gray-800">
      <header className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-gray-900">About Passable</h1>
        <Link href="/" className="text-sm text-blue-700 hover:underline">
          ← Back to the map
        </Link>
      </header>

      <div className="space-y-8 text-sm leading-6">
        <section>
          <p>
            Passable finds walking routes around the University of Waterloo campus and uptown Waterloo for people who
            can&apos;t just take the shortest path: wheelchair users, people with strollers, and anyone walking in winter.
            It also lets people report problems as they find them, such as a blocked sidewalk, an elevator that&apos;s out or
            an icy ramp. Those reports change everyone&apos;s routes within seconds.
          </p>
        </section>

        <Section title="How routes are chosen">
          <ul className="list-disc space-y-1 pl-5">
            <li>
              <b>Wheelchair:</b> never uses steps, raised kerbs or rough surfaces like gravel or grass. Penalizes paths with
              missing information.
            </li>
            <li>
              <b>Stroller:</b> never uses steps. Rough surfaces and kerbs are allowed but avoided where possible.
            </li>
            <li>
              <b>Winter walk:</b> steps are allowed but avoided, and unpaved paths are avoided (they often aren&apos;t
              cleared). Reacts most strongly to ice reports.
            </li>
          </ul>
          <p className="mt-2">
            When no fully accessible route exists, Passable still shows the route with the fewest barriers and names
            each barrier, rather than just saying &quot;no route&quot;.
          </p>
        </Section>

        <Section title="What the colours on a route mean">
          <ul className="list-disc space-y-1 pl-5">
            <li>
              <b>Verified</b> (dark blue): the map data for this stretch says what we need to know, such as the surface
              and the kerbs at crossings.
            </li>
            <li>
              <b>Inferred</b> (light blue): data was missing but is almost certainly fine. For example, sidewalks here
              are paved.
            </li>
            <li>
              <b>Unknown</b> (dashed amber): data is missing and we can&apos;t tell. Treat these stretches with caution.
            </li>
          </ul>
        </Section>

        <Section title="Hazard reports">
          <p>
            Anyone can report a problem. Reports start as <i>reported</i>, become <i>confirmed</i> when two other people
            say &quot;still there&quot;, and disappear when people say &quot;it&apos;s gone&quot; or when they expire. Ice expires
            after 12 hours, construction after a week, and each confirmation resets the clock. Reports marked{" "}
            <i>Demo</i> are simulated, for demonstrations.
          </p>
        </Section>

        <Section title="Where the map data comes from">
          <p>
            Paths, sidewalks, crossings, steps, kerbs and surfaces come from{" "}
            <a href="https://www.openstreetmap.org/copyright" className="text-blue-700 hover:underline">
              OpenStreetMap
            </a>{" "}
            (© OpenStreetMap contributors, ODbL), downloaded through the Overpass API. The basemap is from{" "}
            <a href="https://openfreemap.org" className="text-blue-700 hover:underline">
              OpenFreeMap
            </a>
            . Coverage in this area, measured when the data was imported:
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            <li>About 70% of footpaths have a surface type; the rest are shown as inferred or unknown.</li>
            <li>1,277 mapped kerbs, 246 sets of steps, and 2,167 crossing segments.</li>
            <li>Smoothness, slope and explicit wheelchair tags are on fewer than 2% of paths.</li>
          </ul>
        </Section>

        <Section title="Limitations">
          <ul className="list-disc space-y-1 pl-5">
            <li>
              <b>Slopes are not considered.</b> Elevation isn&apos;t in the data, so a steep path can be suggested to a
              wheelchair user.
            </li>
            <li>
              <b>Indoor routes are mostly missing.</b> Only 8 elevators are mapped, and indoor corridors are usually
              not connected to the outdoor network.
            </li>
            <li>
              <b>Crossings are recognised only where they&apos;re mapped as their own path.</b> Where sidewalks aren&apos;t
              mapped separately, a crossing along the road isn&apos;t flagged.
            </li>
            <li>
              <b>Reports are only as good as the people making them.</b> They are unverified until confirmed.
            </li>
            <li>Only the UW campus and uptown Waterloo area is covered.</li>
          </ul>
        </Section>

        <Section title="Privacy">
          <p>
            There are no accounts. To count one vote per person, your browser gets a random anonymous ID, stored on
            your device. A report stores where and what it is, plus that anonymous ID. Route requests are logged
            without any identifier (profile, distance and timing only) to measure performance. Your IP address is used
            for rate limiting (to stop spam) and is kept only in short-lived counters that expire within minutes.
          </p>
        </Section>

        <p className="text-xs text-gray-500">
          Built as a student project.{" "}
          <Link href="/status" className="hover:underline">
            Live system status
          </Link>
          .
        </p>
      </div>
    </main>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-2 text-base font-semibold text-gray-900">{title}</h2>
      {children}
    </section>
  );
}
