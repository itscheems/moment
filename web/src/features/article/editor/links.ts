/**
 * Where an article is allowed to point.
 *
 * Mirrors validateLink (internal/domain/markdown.go) and the reading side's
 * own copy in api/public.ts: a destination inside this site, or one of the
 * three schemes a reader can be sent to safely. A protocol-relative address is
 * refused because it inherits whatever scheme the page was loaded over, which
 * is not a promise this side can make.
 */
export function isAllowedArticleHref(value: string): boolean {
  const trimmed = value.trim();
  if (trimmed === "" || trimmed.startsWith("//")) return false;
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/.exec(trimmed);
  if (!scheme) return true;
  return ["http", "https", "mailto"].includes(scheme[1].toLowerCase());
}

/**
 * What the owner typed, turned into a destination.
 *
 * A bare host reads as a place on the web, not as a path on this site: an
 * owner who types "example.com" means https://example.com, and storing it as
 * a relative path would point the link at a page of their own that does not
 * exist.
 */
export function normalizeArticleHref(value: string): string | null {
  const trimmed = value.trim();
  if (trimmed === "") return null;
  const hasScheme = /^[A-Za-z][A-Za-z0-9+.-]*:/.test(trimmed);
  const looksInternal =
    trimmed.startsWith("/") ||
    trimmed.startsWith("#") ||
    trimmed.startsWith("?");
  const candidate = hasScheme || looksInternal ? trimmed : `https://${trimmed}`;
  return isAllowedArticleHref(candidate) ? candidate : null;
}
