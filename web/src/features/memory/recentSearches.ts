import { useMemo, useSyncExternalStore } from "react";

const PREFIX = "moment.search.recent.v1:";
const CHANGED = "moment:recent-searches-changed";
const LIMIT = 20;
const sessionOnly = new Map<string, string>();

export function recentSearchScope(
  instanceId: string | null,
  accountId: string | null,
): string | null {
  return instanceId ? `${PREFIX}${instanceId}:${accountId ?? "visitor"}` : null;
}

function snapshot(scope: string | null): string {
  if (!scope) return "";
  if (sessionOnly.has(scope)) return sessionOnly.get(scope)!;
  try {
    return window.localStorage.getItem(scope) ?? "";
  } catch {
    return "";
  }
}

function parse(raw: string): string[] {
  try {
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value)) return [];
    return [
      ...new Set(
        value.filter(
          (item): item is string =>
            typeof item === "string" &&
            Boolean(item.trim()) &&
            Array.from(item).length <= 1_000,
        ),
      ),
    ].slice(0, LIMIT);
  } catch {
    return [];
  }
}

function write(scope: string | null, entries: string[]) {
  if (!scope) return;
  const raw = JSON.stringify(entries);
  try {
    if (entries.length) window.localStorage.setItem(scope, raw);
    else window.localStorage.removeItem(scope);
    sessionOnly.delete(scope);
  } catch {
    // Searches still work for this session when the browser denies storage.
    sessionOnly.set(scope, raw);
  }
  window.dispatchEvent(new CustomEvent(CHANGED, { detail: scope }));
}

export function rememberSearch(scope: string | null, input: string) {
  const query = input.trim();
  if (!query || Array.from(query).length > 1_000) return;
  write(
    scope,
    [query, ...parse(snapshot(scope)).filter((item) => item !== query)].slice(
      0,
      LIMIT,
    ),
  );
}

export function removeRecentSearch(scope: string | null, query: string) {
  write(
    scope,
    parse(snapshot(scope)).filter((item) => item !== query),
  );
}

export function clearRecentSearches(scope: string | null) {
  write(scope, []);
}

function subscribe(onChange: () => void) {
  const storageChanged = (event: StorageEvent) => {
    if (event.key === null) sessionOnly.clear();
    else sessionOnly.delete(event.key);
    onChange();
  };
  window.addEventListener("storage", storageChanged);
  window.addEventListener(CHANGED, onChange);
  return () => {
    window.removeEventListener("storage", storageChanged);
    window.removeEventListener(CHANGED, onChange);
  };
}

export function useRecentSearches(scope: string | null): string[] {
  const raw = useSyncExternalStore(
    subscribe,
    () => snapshot(scope),
    () => "",
  );
  return useMemo(() => parse(raw), [raw]);
}
