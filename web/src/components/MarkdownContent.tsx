import { useLayoutEffect, useMemo, useRef } from "react";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";

import styles from "./MarkdownContent.module.css";

const languages = {
  bash,
  css,
  go,
  java,
  javascript,
  json,
  markdown,
  python,
  sql,
  typescript,
  xml,
};

for (const [name, language] of Object.entries(languages)) {
  hljs.registerLanguage(name, language);
}

const aliases: Record<string, keyof typeof languages> = {
  html: "xml",
  js: "javascript",
  jsx: "javascript",
  md: "markdown",
  py: "python",
  sh: "bash",
  shell: "bash",
  ts: "typescript",
  tsx: "typescript",
  xml: "xml",
};

function registeredLanguage(code: HTMLElement): string | null {
  for (const className of code.classList) {
    if (!className.startsWith("language-")) continue;
    const requested = className.slice("language-".length).toLocaleLowerCase();
    const canonical = aliases[requested] ?? requested;
    if (canonical in languages) return canonical;
  }
  return null;
}

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
 */
function linkHandleMentions(html: string): string {
  if (!html.includes("@")) return html;
  const parsed = new DOMParser().parseFromString(html, "text/html");
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
    const matches = [...value.matchAll(HANDLE_MENTION)];
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
  const rootRef = useRef<HTMLDivElement>(null);
  const renderedHtml = useMemo(() => linkHandleMentions(html), [html]);
  useLayoutEffect(() => {
    for (const code of rootRef.current?.querySelectorAll<HTMLElement>(
      "pre code",
    ) ?? []) {
      const language = registeredLanguage(code);
      if (!language) continue;
      const source = code.textContent ?? "";
      code.innerHTML = hljs.highlight(source, {
        language,
        ignoreIllegals: true,
      }).value;
      code.classList.add("hljs");
      code.dataset.highlighted = "yes";
    }
  }, [renderedHtml]);

  return (
    <div
      className={`${styles.root} ${className ?? ""}`}
      dangerouslySetInnerHTML={{ __html: renderedHtml }}
      ref={rootRef}
    />
  );
}
