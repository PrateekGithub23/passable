"use client";

// HazardPopup: the card shown when someone taps a hazard marker. Rendered into MapLibre's popup
// with its own React root (see MapView), so it uses the same components and styles as the rest
// of the app. React escapes text, so a user-written note can never inject HTML.
//
// The two buttons keep reports honest: "Still there" confirms (and extends the report's
// lifetime), "It's gone" votes to resolve it.

import { Check, LoaderCircle, X } from "lucide-react";
import { useState } from "react";

import type { Hazard } from "@/lib/api";
import { HAZARD_STYLE } from "@/lib/style";

interface Props {
  hazard: Hazard;
  onVote: (kind: "confirm" | "resolve") => Promise<Hazard>;
}

function timeAgo(iso: string): string {
  const min = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 60000));
  if (min < 1) return "just now";
  if (min < 60) return `${min} min ago`;
  const h = Math.round(min / 60);
  return h < 24 ? `${h} h ago` : `${Math.round(h / 24)} d ago`;
}

export default function HazardPopup({ hazard, onVote }: Props) {
  const [h, setH] = useState(hazard);
  const [busy, setBusy] = useState<"confirm" | "resolve">();
  const [done, setDone] = useState<string>();
  const [error, setError] = useState<string>();
  const { label, color, icon: Icon } = HAZARD_STYLE[h.type];

  const vote = async (kind: "confirm" | "resolve") => {
    setBusy(kind);
    setError(undefined);
    try {
      const updated = await onVote(kind);
      setH(updated);
      setDone(updated.status === "resolved" ? "Marked as gone. Thanks!" : kind === "confirm" ? "Thanks for confirming!" : "Thanks! Noted that it's gone.");
    } catch (err) {
      setError(err instanceof Error && err.message.includes("too many") ? "Too many votes. Try again soon." : "Couldn't save your vote.");
    } finally {
      setBusy(undefined);
    }
  };

  return (
    <div className="w-64 p-3.5">
      <div className="flex items-start gap-2.5 pr-5">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-white" style={{ background: color }}>
          <Icon aria-hidden className="h-4 w-4" />
        </span>
        <div className="min-w-0">
          <p className="flex items-center gap-1.5 text-sm font-bold text-stone-900">
            {label}
            {h.demo && <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-amber-800 uppercase">Demo</span>}
          </p>
          <p className="text-xs text-stone-500">
            {h.status === "confirmed" ? "Confirmed" : "Reported"} {timeAgo(h.created_at)}
            {h.confirm_count > 0 && ` · ${h.confirm_count} confirmed`}
          </p>
        </div>
      </div>
      {h.note && <p className="mt-2.5 rounded-lg bg-stone-50 px-2.5 py-2 text-sm text-stone-700">“{h.note}”</p>}
      {h.demo && <p className="mt-2 text-xs text-amber-800">Simulated for demonstration, not a real report.</p>}

      {done ? (
        <p className="mt-3 flex items-center gap-1.5 text-sm font-medium text-brand-800">
          <Check aria-hidden className="h-4 w-4" />
          {done}
        </p>
      ) : (
        <>
          <p className="mt-3 text-xs text-stone-500">Is it still there?</p>
          <div className="mt-1.5 grid grid-cols-2 gap-2">
            <button
              onClick={() => vote("confirm")}
              disabled={busy !== undefined}
              className="flex items-center justify-center gap-1.5 rounded-lg bg-stone-900 px-2 py-2 text-xs font-semibold text-white transition-colors hover:bg-stone-700 disabled:opacity-60"
            >
              {busy === "confirm" ? <LoaderCircle aria-hidden className="h-3.5 w-3.5 animate-spin" /> : <Check aria-hidden className="h-3.5 w-3.5" />}
              Still there
            </button>
            <button
              onClick={() => vote("resolve")}
              disabled={busy !== undefined}
              className="flex items-center justify-center gap-1.5 rounded-lg bg-stone-100 px-2 py-2 text-xs font-semibold text-stone-900 transition-colors hover:bg-stone-200 disabled:opacity-60"
            >
              {busy === "resolve" ? <LoaderCircle aria-hidden className="h-3.5 w-3.5 animate-spin" /> : <X aria-hidden className="h-3.5 w-3.5" />}
              It&apos;s gone
            </button>
          </div>
          {error && <p className="mt-2 text-xs text-red-700">{error}</p>}
        </>
      )}
    </div>
  );
}
