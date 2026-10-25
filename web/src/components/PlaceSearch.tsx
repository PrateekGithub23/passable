"use client";

// PlaceSearch: one "From" or "To" field. Type a building name, code ("DC", "SLC") or a typo'd
// name ("dana portr"); pick a result to set that end of the route. The field also shows
// points chosen by tapping the map ("Point on map").
//
// Accessibility: follows the WAI-ARIA combobox pattern. The input owns a listbox; arrow keys
// move the highlighted option (announced via aria-activedescendant), Enter picks it, Escape
// closes the list.

import { LoaderCircle, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";

import { ApiError, searchPlaces, type Place } from "@/lib/api";

interface Props {
  label: string; // accessible name, e.g. "Start"
  placeholder: string;
  icon: ReactNode; // the coloured marker shown at the left of the field
  value: string; // current label of this waypoint ("" if unset)
  onSelect: (place: Place) => void;
  onClear: () => void;
}

const DEBOUNCE_MS = 200; // wait for a pause in typing; also keeps us well under the API's rate limit

export default function PlaceSearch({ label, placeholder, icon, value, onSelect, onClear }: Props) {
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(value);
  const [results, setResults] = useState<Place[]>([]);
  // The query `results` answer. Until a search for the current text finishes, show "searching",
  // never an old result list or a premature "no matches".
  const [resultsFor, setResultsFor] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [error, setError] = useState<string>();

  // When the waypoint changes from outside (map tap, swap, clear), show its label. Adjusting
  // state during render on a prop change is React's recommended alternative to an effect.
  const [lastValue, setLastValue] = useState(value);
  // The latest value, for the blur timeout below: a value captured when the field was focused
  // would be stale by the time the timeout runs (e.g. just after a place was picked).
  const valueRef = useRef(value);
  useEffect(() => {
    valueRef.current = value;
  }, [value]);
  if (value !== lastValue) {
    setLastValue(value);
    setText(value);
  }

  // Search as the user types (debounced, and cancelling the previous request).
  const query = text.trim();
  const shouldSearch = open && query.length >= 2 && query !== value;
  useEffect(() => {
    if (!shouldSearch) return;
    const controller = new AbortController();
    const t = setTimeout(async () => {
      try {
        const found = await searchPlaces(query, controller.signal);
        setResults(found);
        setResultsFor(query);
        setActive(found.length ? 0 : -1);
        setError(undefined);
      } catch (err) {
        if (controller.signal.aborted) return;
        setResults([]);
        setResultsFor(query);
        setError(err instanceof ApiError && err.message.includes("too many") ? "Slow down a little and try again." : "Search isn't available right now.");
      }
    }, DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      controller.abort();
    };
  }, [query, shouldSearch]);

  const choose = (p: Place) => {
    onSelect(p);
    setOpen(false);
    setResults([]);
    inputRef.current?.blur();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" && results.length) {
      e.preventDefault();
      setOpen(true);
      setActive((i) => (i + 1) % results.length);
    } else if (e.key === "ArrowUp" && results.length) {
      e.preventDefault();
      setActive((i) => (i - 1 + results.length) % results.length);
    } else if (e.key === "Enter" && open && resultsFor === query && results[active]) {
      e.preventDefault();
      choose(results[active]);
    } else if (e.key === "Escape") {
      setOpen(false);
      setText(value);
    }
  };

  const settled = resultsFor === query; // results belong to what's typed now
  const showList = open && shouldSearch && settled;
  const optionId = (i: number) => `${listId}-opt-${i}`;

  return (
    <div className="relative">
      <div className="flex items-center gap-2.5 rounded-xl border border-stone-200 bg-white px-3 transition-colors focus-within:border-brand-600 focus-within:ring-2 focus-within:ring-brand-600/20 hover:border-stone-300">
        <span aria-hidden className="flex w-4 shrink-0 justify-center">
          {icon}
        </span>
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-label={label}
          aria-expanded={showList}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={showList && active >= 0 ? optionId(active) : undefined}
          value={text}
          placeholder={placeholder}
          onChange={(e) => {
            setText(e.target.value);
            setOpen(true);
          }}
          onFocus={(e) => {
            setOpen(true);
            e.target.select(); // replacing a chosen place should take one keystroke
          }}
          onBlur={() => {
            // Delay so a click on an option registers before the list disappears.
            setTimeout(() => {
              setOpen(false);
              setText(valueRef.current);
            }, 150);
          }}
          onKeyDown={onKeyDown}
          className="h-11 min-w-0 flex-1 bg-transparent text-[15px] text-stone-900 placeholder:text-stone-400 focus:outline-none"
        />
        {shouldSearch && !settled && <LoaderCircle aria-hidden className="h-4 w-4 shrink-0 animate-spin text-stone-400" />}
        {value && !(shouldSearch && !settled) && (
          <button
            type="button"
            onClick={() => {
              onClear();
              setText("");
              inputRef.current?.focus();
            }}
            aria-label={`Clear ${label.toLowerCase()}`}
            className="-mr-1 rounded-md p-1 text-stone-400 transition-colors hover:bg-stone-100 hover:text-stone-700"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>

      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label={`${label} suggestions`}
          className="absolute inset-x-0 top-full z-30 mt-1 max-h-72 animate-fade-in overflow-y-auto rounded-xl border border-stone-200 bg-white py-1 shadow-lg"
        >
          {results.map((p, i) => (
            <li
              key={`${p.name}-${p.point.join(",")}`}
              id={optionId(i)}
              role="option"
              aria-selected={i === active}
              // onMouseDown (not onClick) fires before the input's blur closes the list.
              onMouseDown={(e) => {
                e.preventDefault();
                choose(p);
              }}
              onMouseEnter={() => setActive(i)}
              className={`flex cursor-pointer items-baseline gap-2 px-3 py-2 text-sm ${i === active ? "bg-brand-50" : ""}`}
            >
              <span className="min-w-0 flex-1 truncate text-stone-900">{p.name}</span>
              {p.ref && <span className="shrink-0 rounded bg-stone-100 px-1.5 py-0.5 text-[11px] font-semibold text-stone-600">{p.ref}</span>}
            </li>
          ))}
          {results.length === 0 && (
            <li className="px-3 py-2 text-sm text-stone-500">{error ?? "No places match. Try a building code like “DC” or tap the map."}</li>
          )}
        </ul>
      )}
    </div>
  );
}
