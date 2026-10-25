// /status: public live metrics. The page shell is static; StatusBoard fetches the numbers in
// the browser so they're always current.

import type { Metadata } from "next";

import StatusBoard from "@/components/StatusBoard";

export const metadata: Metadata = { title: "Status · Passable" };

export default function StatusPage() {
  return <StatusBoard />;
}
