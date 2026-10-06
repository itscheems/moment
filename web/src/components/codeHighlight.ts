/**
 * Syntax highlighting for a fenced block, done to markup rather than to the DOM.
 *
 * It used to be a layout effect that reached into the committed tree and
 * rewrote every `pre code` in place. That works exactly once. React 19 compares
 * `dangerouslySetInnerHTML` by the identity of the prop object, and a render
 * builds a fresh `{ __html }` every time, so any later re-render -- a hover, a
 * resolved mention, a sibling's state -- re-commits the original markup over
 * the highlighted markup. The effect does not run again, because the html
 * string it depends on never changed. A post read on its own page kept its
 * colours because nothing re-rendered it; the same post in a timeline lost
 * them, which is exactly the shape the bug was reported in.
 *
 * Highlighting the html before it is handed to React removes the race instead
 * of racing better: every commit writes the coloured markup, and the same
 * function serves the share card, which has no DOM to mutate at all.
 */
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import go from "highlight.js/lib/languages/go";
import graphql from "highlight.js/lib/languages/graphql";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import kotlin from "highlight.js/lib/languages/kotlin";
import lua from "highlight.js/lib/languages/lua";
import makefile from "highlight.js/lib/languages/makefile";
import markdown from "highlight.js/lib/languages/markdown";
import nginx from "highlight.js/lib/languages/nginx";
import objectivec from "highlight.js/lib/languages/objectivec";
import php from "highlight.js/lib/languages/php";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import ruby from "highlight.js/lib/languages/ruby";
import rust from "highlight.js/lib/languages/rust";
import scss from "highlight.js/lib/languages/scss";
import sql from "highlight.js/lib/languages/sql";
import swift from "highlight.js/lib/languages/swift";
import toml from "highlight.js/lib/languages/ini";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

export const codeLanguages = {
  bash,
  c,
  cpp,
  csharp,
  css,
  diff,
  go,
  graphql,
  java,
  html: xml,
  javascript,
  json,
  kotlin,
  lua,
  makefile,
  markdown,
  nginx,
  objectivec,
  php,
  plaintext,
  python,
  ruby,
  rust,
  scss,
  sql,
  swift,
  toml,
  typescript,
  xml,
  yaml,
  shell: bash,
};

for (const [name, language] of Object.entries(codeLanguages)) {
  hljs.registerLanguage(name, language);
}

export const codeLanguageAliases: Record<string, keyof typeof codeLanguages> = {
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

/** The language this block asked for, if it is one that was registered. */
export function registeredLanguage(code: Element): string | null {
  for (const className of code.classList) {
    if (!className.startsWith("language-")) continue;
    const requested = className.slice("language-".length).toLocaleLowerCase();
    const canonical = codeLanguageAliases[requested] ?? requested;
    if (canonical in codeLanguages) return canonical;
  }
  return null;
}

/**
 * Colour every fenced block under `root`, in place.
 *
 * The tree is usually a detached parse, not the live document. highlight.js
 * escapes what it emits, so the result is markup this codebase produced from
 * text -- it does not widen what a post can put on the page.
 */
export function highlightCodeBlocks(root: ParentNode): void {
  for (const code of root.querySelectorAll("pre code")) {
    if (code.classList.contains("hljs")) continue;
    const language = registeredLanguage(code);
    if (!language) continue;
    code.innerHTML = hljs.highlight(code.textContent ?? "", {
      language,
      ignoreIllegals: true,
    }).value;
    code.classList.add("hljs");
  }
}

/** Escape and highlight a document code node without touching mounted DOM. */
export function highlightSource(
  source: string,
  requested: string,
): string | null {
  const name = requested.toLowerCase();
  const language = codeLanguageAliases[name] ?? name;
  if (!(language in codeLanguages)) return null;
  return hljs.highlight(source, { language, ignoreIllegals: true }).value;
}
