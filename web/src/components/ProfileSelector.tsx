"use client";

// ProfileSelector: picks the routing profile. Built as a radio group (one choice out of
// three), so keyboard and screen-reader users get the standard arrow-key behaviour for free,
// which matters for an accessibility-focused app.

import type { Profile } from "@/lib/api";
import { PROFILES } from "@/lib/style";

interface Props {
  value: Profile;
  onChange: (p: Profile) => void;
}

export default function ProfileSelector({ value, onChange }: Props) {
  return (
    <fieldset>
      <legend className="mb-2 text-sm font-medium text-gray-700">Route for</legend>
      <div className="grid grid-cols-3 gap-2">
        {PROFILES.map((p) => (
          // The <input> is visually hidden but still focusable; the label is the visible button.
          <label
            key={p.id}
            title={p.hint}
            className="cursor-pointer rounded-lg border border-gray-300 px-2 py-2 text-center text-sm text-gray-800
                       has-[:checked]:border-blue-700 has-[:checked]:bg-blue-700 has-[:checked]:text-white
                       has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-blue-400"
          >
            <input
              type="radio"
              name="profile"
              value={p.id}
              checked={value === p.id}
              onChange={() => onChange(p.id)}
              className="sr-only"
            />
            {p.label}
          </label>
        ))}
      </div>
      <p className="mt-2 text-xs text-gray-500">{PROFILES.find((p) => p.id === value)?.hint}</p>
    </fieldset>
  );
}
