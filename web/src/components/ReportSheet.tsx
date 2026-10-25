"use client";

// ReportSheet: the "report a problem" form, shown after the user taps the map in report mode.
//
// Designed for someone who has never seen the app:
//   - every choice is a big labelled button with an icon and a one-line explanation (no jargon
//     like "severity"; the question is "can someone in a wheelchair get past it?"),
//   - only the type is required; everything else has a sensible default,
//   - errors say what to do next ("tap closer to a path"), not what went wrong internally.
//
// It's a modal dialog for assistive tech (role="dialog", focus moves into it, Escape cancels).

import { CircleAlert, LoaderCircle, Megaphone, X } from "lucide-react";
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
      className="pointer-events-auto max-h-[85dvh] w-full max-w-xl animate-slide-up overflow-y-auto rounded-3xl bg-white px-5 pt-5 shadow-2xl ring-1 ring-stone-900/5"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-amber-100 text-amber-800">
            <Megaphone aria-hidden className="h-4.5 w-4.5" />
          </span>
          <div>
            <h2 id="report-title" className="text-lg leading-tight font-bold text-stone-900">
              What&apos;s the problem here?
            </h2>
            <p className="text-xs text-stone-500">It shows up for everyone nearby and changes their routes.</p>
          </div>
        </div>
        <button onClick={onCancel} aria-label="Close" className="rounded-lg p-1.5 text-stone-400 transition-colors hover:bg-stone-100 hover:text-stone-700">
          <X className="h-5 w-5" />
        </button>
      </div>

      {/* Step 1: type (required). aria-pressed tells screen readers which one is chosen. */}
      <div className="mt-4 grid gap-2 sm:grid-cols-2">
        {TYPES.map((t, i) => {
          const { label, hint, color, icon: Icon } = HAZARD_STYLE[t];
          const selected = type === t;
          return (
            <button
              key={t}
              ref={i === 0 ? firstButton : undefined}
              aria-pressed={selected}
              onClick={() => {
                setType(t);
                setBlocks(undefined); // reset to this type's default
              }}
              className={`flex items-start gap-3 rounded-2xl border p-3 text-left transition-all ${
                selected ? "border-brand-700 bg-brand-50 ring-1 ring-brand-700" : "border-stone-200 hover:border-stone-300 hover:bg-stone-50"
              }`}
            >
              <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-white" style={{ background: color }}>
                <Icon aria-hidden className="h-4 w-4" />
              </span>
              <span>
                <span className="block text-sm font-semibold text-stone-900">{label}</span>
                <span className="block text-xs text-stone-600">{hint}</span>
              </span>
            </button>
          );
        })}
      </div>

      {type && (
        <div className="animate-fade-in">
          {/* Step 2: does it block the way? Maps to severity major/minor. */}
          <fieldset className="mt-5">
            <legend className="text-sm font-semibold text-stone-900">Can someone in a wheelchair get past it?</legend>
            <div className="mt-2 grid grid-cols-2 gap-1 rounded-xl bg-stone-100 p-1">
              {[
                { value: true, label: "No, it blocks the way" },
                { value: false, label: "Yes, with care" },
              ].map((o) => (
                <button
                  key={String(o.value)}
                  aria-pressed={effectiveBlocks === o.value}
                  onClick={() => setBlocks(o.value)}
                  className={`rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                    effectiveBlocks === o.value ? "bg-white text-stone-900 shadow-sm" : "text-stone-600 hover:text-stone-900"
                  }`}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </fieldset>

          {/* Step 3: optional note. */}
          <label className="mt-5 block text-sm font-semibold text-stone-900">
            Anything else? <span className="font-normal text-stone-500">(optional)</span>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              maxLength={280}
              rows={2}
              placeholder="e.g. fence across the path until Friday"
              className="mt-1.5 block w-full resize-none rounded-xl border border-stone-200 p-2.5 text-sm font-normal text-stone-900 transition-colors placeholder:text-stone-400 focus:border-brand-600 focus:ring-2 focus:ring-brand-600/20 focus:outline-none"
            />
          </label>
        </div>
      )}

      {error && (
        <p role="alert" className="mt-4 flex gap-2 rounded-xl bg-red-50 p-3 text-sm text-red-800">
          <CircleAlert aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </p>
      )}

      {/* Sticky footer: Send is always visible, even when the form scrolls on a small phone. */}
      <div className="sticky bottom-0 -mx-5 mt-5 flex items-center justify-end gap-2 border-t border-stone-100 bg-white px-5 py-3">
        <button onClick={onCancel} className="rounded-xl px-4 py-2.5 text-sm font-medium text-stone-700 transition-colors hover:bg-stone-100">
          Cancel
        </button>
        <button
          onClick={submit}
          disabled={!type || sending}
          className="flex items-center gap-2 rounded-xl bg-brand-700 px-5 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-brand-800 disabled:cursor-not-allowed disabled:bg-stone-200 disabled:text-stone-500"
        >
          {sending && <LoaderCircle aria-hidden className="h-4 w-4 animate-spin" />}
          {sending ? "Sending…" : "Send report"}
        </button>
      </div>
    </div>
  );
}
