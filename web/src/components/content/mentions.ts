/**
 * Anything shaped like a handle, immediately after an `@` that begins a word.
 *
 * The same shape the profile route matches and the server enforces, widened
 * only to accept the capitals a person types without thinking. The leading
 * guard is what keeps `you@example.com` out of it: an address has a word
 * character before the at sign, a mention never does.
 *
 * The trailing guard rejects a hyphen as well as a word character, so
 * `@some-slug` stays text instead of linking the `@some` buried in it. An
 * underscore can be part of a handle and a hyphen can never be, which makes a
 * run containing one a word that happens to open with an at sign.
 */
export const HANDLE_MENTION =
  /(^|[^A-Za-z0-9_])@([A-Za-z][A-Za-z0-9_]{2,29})(?![A-Za-z0-9_-])/g;

/**
 * The handles no account can ever hold, mirroring `reservedUsernames` in
 * internal/domain/user.go.
 *
 * The list is copied rather than fetched because deciding whether a word is a
 * link must not cost a request; it changes about as often as the route table
 * does, and the Go side is the authority if the two ever disagree. Names too
 * short to be a handle at all are kept in it anyway, so the two lists can be
 * read side by side.
 */
export const RESERVED_HANDLES = new Set([
  // Server routes: /healthz, /readyz, and everything under /api.
  "api",
  "healthz",
  "readyz",
  // Browser routes declared in web/src/app/routes.ts.
  "search",
  "archive",
  "profile",
  // Bundled asset directories under internal/webassets/dist.
  "assets",
  "fonts",
  "icons",
  // Held back so the paths stay available without renaming anyone later.
  "about",
  "account",
  "admin",
  "auth",
  "comment",
  "comments",
  "delete",
  "edit",
  "favicon",
  "feed",
  "help",
  "login",
  "logout",
  "manifest",
  "me",
  "media",
  "moment",
  "moments",
  "new",
  "oauth",
  "post",
  "posts",
  "public",
  "register",
  "robots",
  "root",
  "rss",
  "settings",
  "signup",
  "sitemap",
  "static",
  "support",
  "user",
  "users",
  "www",
]);

/** collectHandles reports the handles a body mentions, reserved ones excluded. */
export function collectHandles(html: string): string[] {
  if (!html.includes("@")) return [];
  const found = new Set<string>();
  for (const match of html.matchAll(HANDLE_MENTION)) {
    const handle = match[2].toLocaleLowerCase();
    if (!RESERVED_HANDLES.has(handle)) found.add(handle);
  }
  return [...found];
}
