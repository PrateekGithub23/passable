"use client";

// ProfileSelector: how the person gets around. A native radio group styled as a segmented
// control, so keyboard (arrow keys) and screen-reader behaviour come for free, which matters
// for an accessibility-focused app.

import type { Profile } from "@/lib/api";
import { PROFILES } from "@/lib/style";

interface Props {
  value: Profile;
  onChange: (p: Profile) => void;
}

export default function ProfileSelector({ value, onChange }: Props) {
  const current = PROFILES.find((p) => p.id === value);
  return (
    <fieldset>
      <legend className="sr-only">How are you getting around?</legend>
      <div className="grid grid-cols-3 gap-1 rounded-xl bg-stone-100 p-1">
        {PROFILES.map((p) => {
          const Icon = p.icon;
          return (
            // The <input> is visually hidden but focusable; the label is the visible segment.
            <label
              key={p.id}
              className="flex cursor-pointer flex-col items-center gap-1 rounded-lg px-2 py-2 text-xs font-semibold text-stone-600 transition-colors hover:text-stone-900 has-[:checked]:bg-white has-[:checked]:text-brand-800 has-[:checked]:shadow-sm has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-brand-600"
            >
              <input
                type="radio"
                name="profile"
                value={p.id}
                checked={value === p.id}
                onChange={() => onChange(p.id)}
                className="sr-only"
              />
              <Icon aria-hidden className="h-5 w-5" strokeWidth={2} />
              {p.label}
            </label>
          );
        })}
      </div>
      <p className="mt-1.5 px-1 text-xs text-stone-500">{current?.hint}</p>
    </fieldset>
  );
}
