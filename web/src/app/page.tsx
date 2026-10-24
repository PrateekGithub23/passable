// Home page ("/"). It stays a Server Component (the Next.js default) and just renders the
// client-side Planner; everything interactive lives below that "use client" boundary.

import Planner from "@/components/Planner";

export default function Home() {
  return <Planner />;
}
