import { type MentionSuggestion } from "../../api/public";

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

export type RecentSearch = string | { kind: "user"; user: MentionSuggestion };

export function recentSearchKey(item: RecentSearch): string {
  return typeof item === "string"
    ? `query:${item.toLowerCase()}`
    : `user:${item.user.userId}`;
}

function validUser(value: unknown): value is MentionSuggestion {
  if (!value || typeof value !== "object") return false;
  const user = value as Record<string, unknown>;
  return (
    typeof user.userId === "string" &&
    /^[0-9a-f]{32}$/.test(user.userId) &&
    typeof user.username === "string" &&
    /^[a-z][a-z0-9_]{2,29}$/.test(user.username) &&
    typeof user.displayName === "string" &&
    Boolean(user.displayName.trim()) &&
    (user.avatarMediaId === null ||
      (typeof user.avatarMediaId === "string" &&
        /^[0-9A-HJKMNP-TV-Z]{26}$/.test(user.avatarMediaId))) &&
    user.profileUrl === `/${user.username}`
  );
}

function parse(raw: string): RecentSearch[] {
  try {
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value)) return [];
    const entries: RecentSearch[] = [];
    const keys = new Set<string>();
    for (const item of value) {
      if (
        typeof item === "string"
          ? !item.trim() || Array.from(item).length > 1_000
          : !item || item.kind !== "user" || !validUser(item.user)
      )
        continue;
      const key = recentSearchKey(item);
      if (!keys.has(key)) {
        entries.push(item);
        keys.add(key);
      }
      if (entries.length === LIMIT) break;
    }
    return entries;
  } catch {
    return [];
  }
}

function write(scope: string | null, entries: RecentSearch[]) {
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
    [
      query,
      ...parse(snapshot(scope)).filter(
        (item) => recentSearchKey(item) !== recentSearchKey(query),
      ),
    ].slice(0, LIMIT),
  );
}

export function removeRecentSearch(scope: string | null, query: RecentSearch) {
  write(
    scope,
    parse(snapshot(scope)).filter(
      (item) => recentSearchKey(item) !== recentSearchKey(query),
    ),
  );
}

export function rememberUserSearch(
  scope: string | null,
  user: MentionSuggestion,
) {
  if (!validUser(user)) return;
  const entry: RecentSearch = { kind: "user", user };
  write(
    scope,
    [
      entry,
      ...parse(snapshot(scope)).filter(
        (item) => recentSearchKey(item) !== recentSearchKey(entry),
      ),
    ].slice(0, LIMIT),
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

export function useRecentSearches(scope: string | null): RecentSearch[] {
  const raw = useSyncExternalStore(
    subscribe,
    () => snapshot(scope),
    () => "",
  );
  return useMemo(() => parse(raw), [raw]);
}
