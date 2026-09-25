import { useCallback, useEffect, useMemo, type MouseEvent } from "react";

import { useMentionResolver } from "../app/mentions";
import { useNavigation } from "../app/navigation";

import { highlightCodeBlocks } from "./codeHighlight";
import styles from "./MarkdownContent.module.css";

import {
  HANDLE_MENTION,
  RESERVED_HANDLES,
  collectHandles,
} from "./content/mentions";

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
