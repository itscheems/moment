import { PublicApiError } from "../../api/public";

export function invalidCursorProgress(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

export function appendUnique<T>(
  current: T[],
  incoming: T[],
  key: (item: T) => string,
  label: string,
): T[] {
  const existing = new Set(current.map(key));
  if (incoming.some((item) => existing.has(key(item)))) {
    throw invalidCursorProgress(`continued ${label} page contains a duplicate`);
  }
  return [...current, ...incoming];
}

export function validateCursorPage<T>(
  page: { items: T[]; nextCursor: string | null },
  requestedCursor: string | null,
  visited: ReadonlySet<string>,
  label: string,
): void {
  if (page.items.length === 0 && page.nextCursor !== null) {
    throw invalidCursorProgress(`empty ${label} page advances the cursor`);
  }
  if (page.nextCursor !== null && visited.has(page.nextCursor)) {
    throw invalidCursorProgress(
      page.nextCursor === requestedCursor
        ? `${label} cursor points to itself`
        : `${label} cursor cycle detected`,
    );
  }
}

export function loadedThroughFromSearch(search: string): string | null {
  const progress = new URLSearchParams(search).getAll("loaded_through");
  if (
    progress.length > 1 ||
    (progress.length === 1 &&
      (progress[0].length === 0 || progress[0].length > 4_096))
  ) {
    throw invalidCursorProgress("loaded cursor is invalid");
  }
  return progress[0] ?? null;
}

export function withoutLoadedProgress(search: string): string {
  const query = new URLSearchParams(search);
  query.delete("loaded_through");
  const value = query.toString();
  return value ? `?${value}` : "";
}
