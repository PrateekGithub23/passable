"use client";

// ReportSheet: the "report a problem" form, shown after the user taps the map in report mode.
//
// Designed for someone who has never seen the app:
//   - every choice is a big labelled button with a one-line explanation (no jargon like
//     "severity"; the question is "can people get past it?"),
//   - only the type is required; everything else has a sensible default,
//   - errors say what to do next ("tap closer to a path"), not what went wrong internally.
//
// It's a modal dialog for assistive tech (role="dialog", focus moves into it, Escape cancels).

import { useEffect, useRef, useState } from "react";

import { ApiError, createHazard, type Hazard, type HazardType, type LngLat } from "@/lib/api";
import { HAZARD_STYLE } from "@/lib/style";

interface Props {
  point: LngLat;
  onDone: (h: Hazard) => void;
  onCancel: () => void;
}

const TYPES = Object.keys(HAZARD_STYLE) as HazardType[];

export default function ReportSheet({ point, onDone, onCancel }: Props) {
  const [type, setType] = useState<HazardType>();
  // undefined = "use the type's default" until the user answers the question themselves.
  const [blocks, setBlocks] = useState<boolean>();
  const [note, setNote] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string>();
  const firstButton = useRef<HTMLButtonElement>(null);

  // Move keyboard focus into the dialog when it opens; Escape closes it.
  useEffect(() => {
    firstButton.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const effectiveBlocks = blocks ?? (type ? HAZARD_STYLE[type].blocks : true);

  const submit = async () => {
    if (!type) return;
    setSending(true);
    setError(undefined);
    try {
      const h = await createHazard({
        lat: point[1],
        lng: point[0],
        type,
        severity: effectiveBlocks ? "major" : "minor",
        note: note.trim() || undefined,
      });
      onDone(h);
    } catch (err) {
      // Turn the API's messages into next steps for the user.
      const msg = err instanceof ApiError ? err.message : "";
      if (msg.includes("no mapped path")) setError("That spot isn't on a path we know. Tap right on the sidewalk or path.");
      else if (msg.includes("too many requests")) setError("You've sent several reports in a short time. Please wait a few minutes.");
      else setError("Couldn't send the report. Check your connection and try again.");
      setSending(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="report-title"
      // Scrolls internally if the screen is short; the buttons below stay pinned (sticky).
      className="pointer-events-auto max-h-[85dvh] w-full max-w-xl overflow-y-auto rounded-2xl bg-white px-4 pt-4 shadow-xl ring-1 ring-black/10"
    >
      <h2 id="report-title" className="text-lg font-semibold text-gray-900">
        What&apos;s the problem here?
      </h2>

      {/* Step 1: type (required). Radio-like buttons: aria-pressed tells screen readers which is chosen. */}
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        {TYPES.map((t, i) => (
          <button
            key={t}
            ref={i === 0 ? firstButton : undefined}
            aria-pressed={type === t}
            onClick={() => {
              setType(t);
              setBlocks(undefined); // reset to this type's default
            }}
            className={`flex items-start gap-3 rounded-xl border p-3 text-left ${
              type === t ? "border-blue-700 bg-blue-50 ring-1 ring-blue-700" : "border-gray-200 hover:bg-gray-50"
            }`}
          >
            <span aria-hidden className="mt-1 h-3 w-3 shrink-0 rounded-full" style={{ background: HAZARD_STYLE[t].color }} />
            <span>
              <span className="block text-sm font-medium text-gray-900">{HAZARD_STYLE[t].label}</span>
              <span className="block text-xs text-gray-600">{HAZARD_STYLE[t].hint}</span>
            </span>
          </button>
        ))}
      </div>

      {type && (
        <>
          {/* Step 2: does it block the way? Maps to severity major/minor. */}
          <fieldset className="mt-4">
            <legend className="text-sm font-medium text-gray-900">Can someone in a wheelchair get past it?</legend>
            <div className="mt-2 grid grid-cols-2 gap-2">
              {[
                { value: true, label: "No, it blocks the way" },
                { value: false, label: "Yes, with care" },
              ].map((o) => (
                <button
                  key={String(o.value)}
                  aria-pressed={effectiveBlocks === o.value}
                  onClick={() => setBlocks(o.value)}
                  className={`rounded-lg border px-3 py-2 text-sm ${
                    effectiveBlocks === o.value ? "border-blue-700 bg-blue-700 text-white" : "border-gray-300 text-gray-800 hover:bg-gray-50"
                  }`}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </fieldset>

          {/* Step 3: optional note. */}
          <label className="mt-4 block text-sm font-medium text-gray-900">
            Anything else? <span className="font-normal text-gray-500">(optional)</span>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              maxLength={280}
              rows={2}
              placeholder="e.g. fence across the path until Friday"
              className="mt-1 block w-full rounded-lg border border-gray-300 p-2 text-sm font-normal text-gray-900"
            />
          </label>
        </>
      )}

      {error && (
        <p role="alert" className="mt-3 rounded-lg bg-red-50 p-2 text-sm text-red-800">
          {error}
        </p>
      )}

      {/* Sticky footer: Send is always visible, even when the form scrolls on a small phone. */}
      <div className="sticky bottom-0 -mx-4 mt-4 flex justify-end gap-2 border-t border-gray-100 bg-white px-4 py-3">
        <button onClick={onCancel} className="rounded-lg px-4 py-2 text-sm text-gray-700 hover:bg-gray-100">
          Cancel
        </button>
        <button
          onClick={submit}
          disabled={!type || sending}
          className="rounded-lg bg-blue-700 px-4 py-2 text-sm font-medium text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:bg-gray-300"
        >
          {sending ? "Sending…" : "Send report"}
        </button>
      </div>
    </div>
  );
}
