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

/**
 * Link every mention in the prose to the profile it names.
 *
 * This used to be handed a username, because a single-owner instance had
 * exactly one handle worth recognising and everything else was noise. A
 * mention can now name anybody -- the composer's autocomplete searches every
 * approved account -- and an autocompleted name that renders as flat text is
 * a stranger result than no autocomplete at all.
 *
 * So the shape of a handle is the whole test, and knowing who exists is
 * deliberately not this function's business. It could not be: deciding whether
 * a word is a link would mean a lookup per render. A handle belonging to
 * nobody lands on the profile route and renders "no such user" -- the same
 * answer every other site gives, and a better one than withholding a real
 * mention because a lookup had not come back yet.
 *
 * Case is accepted and canonicalised. Handles are lowercase by rule, nobody
 * typing one is thinking about that, and `@Cheems` naming a different person
 * than `@cheems` is not something this instance can express. What was typed
 * stays on screen; only the address it points at is lowered.
 *
 * A mention only becomes a link once the handle is known to name somebody. It
 * is text an author typed, not a reference anything checked: this instance
 * shipped with the handle `owner` and its owner renamed, so every `@owner`
 * written before that named nobody while still reading as a link to a profile
 * page that answers "no such user". An unresolved handle stays the text it was
 * typed as, which is also the right answer for a typo and for an account that
 * has since been removed.
 *
 * The other exception is a handle the site has reserved for itself. `@login`,
 * `@search` and `@assets` name pages and asset directories, and the server
 * refuses to create an account under any of them -- so unlike an unknown handle,
 * which may belong to somebody by tomorrow, a link to one is wrong for good.
 * Following it does not even reach a profile: these are plain anchors, so the
 * reader leaves the page they were reading and lands on the sign-in form or on
 * a directory of bundled assets. Those stay as the text they were typed as, the
 * same answer `@ab` and `@some-slug` already get.
 */
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
