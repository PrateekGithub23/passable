"use client";

// Small shared UI pieces used across pages, so the brand mark and tooltips look identical
// everywhere.

import { Info } from "lucide-react";
import { useId } from "react";

/** The Passable mark: a path between two points, in the brand teal (same drawing as app/icon.svg). */
export function LogoMark({ className = "h-8 w-8" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden>
      <rect width="32" height="32" rx="8" fill="#0f766e" />
      <path d="M9 23c0-5 4-6 7-8s7-3 7-8" fill="none" stroke="#fff" strokeWidth="3" strokeLinecap="round" />
      <circle cx="9" cy="23" r="3" fill="#99f6e4" />
      <circle cx="23" cy="7" r="3" fill="#fff" />
    </svg>
  );
}

/**
 * InfoTip: a small (i) button that reveals a short explanation on hover or keyboard focus.
 * Pure CSS (group-hover / group-focus-within), so there's no JS or positioning library. The
 * text is linked with aria-describedby so screen readers announce it with the button.
 */
export function InfoTip({ text, label = "More information" }: { text: string; label?: string }) {
  const id = useId();
  return (
    <span className="group relative inline-flex align-middle">
      <button
        type="button"
        aria-label={label}
        aria-describedby={id}
        className="rounded-full p-0.5 text-stone-400 transition-colors hover:text-stone-700"
      >
        <Info className="h-3.5 w-3.5" />
      </button>
      <span
        id={id}
        role="tooltip"
        className="pointer-events-none invisible absolute bottom-full left-1/2 z-30 mb-1.5 w-56 -translate-x-1/2 rounded-lg bg-stone-900 px-2.5 py-1.5 text-xs leading-snug font-normal text-white opacity-0 shadow-lg transition-opacity group-focus-within:visible group-focus-within:opacity-100 group-hover:visible group-hover:opacity-100"
      >
        {text}
      </span>
    </span>
  );
}
