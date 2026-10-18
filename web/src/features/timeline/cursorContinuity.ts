import { PublicApiError } from "../../api/public";

export class CursorProgressError extends PublicApiError {
  constructor(message: string) {
    super(message, { code: "API_RESPONSE_INVALID", status: 0 });
  }
}

export function invalidCursorProgress(message: string): PublicApiError {
  return new CursorProgressError(message);
}

export function isCursorProgressError(error: unknown): boolean {
  return (
    error instanceof CursorProgressError ||
    (error instanceof PublicApiError &&
      error.code === "INVALID_ARGUMENT" &&
      error.details.field === "cursor")
  );
}

export function appendUnique<T>(
  current: T[],
  incoming: T[],
  key: (item: T) => string,
): T[] {
  const merged = new Map(current.map((item) => [key(item), item]));
  for (const item of incoming) merged.set(key(item), item);
  return [...merged.values()];
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

/** The two directions a list records its progress in, as URL parameters. */
export type ProgressName = "loaded_through" | "loaded_newer";

export function loadedThroughFromSearch(
  search: string,
  name: ProgressName = "loaded_through",
): string | null {
  const progress = new URLSearchParams(search).getAll(name);
  if (
    progress.length > 1 ||
    (progress.length === 1 &&
      (progress[0].length === 0 || progress[0].length > 4_096))
  ) {
    return null;
  }
  return progress[0] ?? null;
}

export function withoutLoadedProgress(search: string): string {
  const query = new URLSearchParams(search);
  query.delete("loaded_through");
  query.delete("loaded_newer");
  const value = query.toString();
  return value ? `?${value}` : "";
}

/** The mirror of `appendUnique` for a page that goes in front. */
export function prependUnique<T>(
  current: T[],
  incoming: T[],
  key: (item: T) => string,
): T[] {
  const merged = new Map(incoming.map((item) => [key(item), item]));
  for (const item of current)
    if (!merged.has(key(item))) merged.set(key(item), item);
  return [...merged.values()];
}

/**
 * The newer direction's page checks: a page that has nothing and still says
 * there is more is lying, and a continuation the list has already read is a
 * loop. There is no "points to itself" case -- the cursor that was sent is
 * in `visited` already.
 */
export function validateNewerPage<T>(
  page: { items: T[]; prevCursor: string | null },
  visited: ReadonlySet<string>,
  label: string,
): void {
  if (page.items.length === 0 && page.prevCursor !== null) {
    throw invalidCursorProgress(`empty ${label} page advances the cursor`);
  }
  if (page.prevCursor !== null && visited.has(page.prevCursor)) {
    throw invalidCursorProgress(`${label} cursor cycle detected`);
  }
}
