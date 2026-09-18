import { useCallback, useEffect, useMemo, type MouseEvent } from "react";

import { useMentionResolver } from "../app/mentions";
import { useNavigation } from "../app/navigation";

import { highlightCodeBlocks } from "./codeHighlight";
import styles from "./MarkdownContent.module.css";

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
const HANDLE_MENTION =
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
const RESERVED_HANDLES = new Set([
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
function collectHandles(html: string): string[] {
  if (!html.includes("@")) return [];
  const found = new Set<string>();
  for (const match of html.matchAll(HANDLE_MENTION)) {
    const handle = match[2].toLocaleLowerCase();
    if (!RESERVED_HANDLES.has(handle)) found.add(handle);
  }
  return [...found];
}

/**
 * The markup as it will be committed: mentions linked, fenced blocks coloured.
 *
 * Both passes work on one parse of the body, and both belong here rather than
 * in an effect over the rendered tree -- see codeHighlight.ts for what reaching
 * into the committed DOM cost.
 */
function prepareHtml(
  html: string,
  resolves: (handle: string) => boolean,
): string {
  const mentionable = html.includes("@");
  if (!mentionable && !html.includes("<pre")) return html;
  const parsed = new DOMParser().parseFromString(html, "text/html");
  highlightCodeBlocks(parsed.body);
  if (!mentionable) return parsed.body.innerHTML;
  const walker = parsed.createTreeWalker(parsed.body, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    // Never inside a link, and never inside code: an `@handle` in a snippet is
    // part of the snippet.
    if (!node.parentElement?.closest("a, code, pre")) nodes.push(node);
  }
  for (const node of nodes) {
    const value = node.nodeValue ?? "";
    const matches = [...value.matchAll(HANDLE_MENTION)].filter((match) => {
      const handle = match[2].toLocaleLowerCase();
      return !RESERVED_HANDLES.has(handle) && resolves(handle);
    });
    if (matches.length === 0) continue;
    const fragment = parsed.createDocumentFragment();
    let cursor = 0;
    for (const match of matches) {
      const index = match.index ?? 0;
      const prefix = match[1] ?? "";
      const typed = match[2];
      const handle = typed.toLowerCase();
      fragment.append(value.slice(cursor, index + prefix.length));
      const link = parsed.createElement("a");
      link.setAttribute("href", `/${handle}`);
      link.textContent = `@${typed}`;
      link.dataset.momentMention = handle;
      fragment.append(link);
      cursor = index + match[0].length;
    }
    fragment.append(value.slice(cursor));
    node.replaceWith(fragment);
  }
  return parsed.body.innerHTML;
}

export function MarkdownContent({
  className,
  html,
}: {
  className?: string;
  html: string;
}) {
  const { navigate } = useNavigation();
  const { resolves, consider } = useMentionResolver();
  const mentioned = useMemo(() => collectHandles(html), [html]);
  useEffect(() => consider(mentioned), [consider, mentioned]);
  const renderedHtml = useMemo(
    () => prepareHtml(html, resolves),
    [html, resolves],
  );
  // One object per rendered string. React 19 compares this prop by identity
  // and rewrites innerHTML whenever it sees a new one, so an inline literal
  // here rebuilt every post's body on every render of the list -- measured:
  // a paragraph found after the first paint was detached by the next commit.
  // A rebuilt body loses the reader's text selection and, with a link card's
  // player inside it, the player.
  const markup = useMemo(() => ({ __html: renderedHtml }), [renderedHtml]);

  /**
   * Keep a link written into this HTML inside the app.
   *
   * The markup goes in through dangerouslySetInnerHTML, so these are real
   * anchors that no React handler sits on -- every one of them reloaded the
   * whole page, mentions included, which throws away the timeline the reader
   * was in the middle of. Delegating from the container is what reaches them,
   * because they are not this component's elements to give props to.
   */
  const followInternalLink = useCallback(
    (event: MouseEvent<HTMLDivElement>) => {
      // The modifiers all mean "not here": a new tab, a new window, a save.
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      ) {
        return;
      }
      const anchor = (event.target as Element | null)?.closest?.("a");
      if (!(anchor instanceof HTMLAnchorElement)) return;
      // An author's own link to somewhere else is theirs to keep, and so is
      // one asking for a new tab or a download.
      if (anchor.target && anchor.target !== "_self") return;
      if (anchor.hasAttribute("download")) return;
      if (anchor.origin !== window.location.origin) return;
      event.preventDefault();
      navigate(`${anchor.pathname}${anchor.search}${anchor.hash}`);
    },
    [navigate],
  );

  return (
    <div
      className={`${styles.root} ${className ?? ""}`}
      dangerouslySetInnerHTML={markup}
      onClick={followInternalLink}
    />
  );
}
