import { useLayoutEffect, useRef } from "react";
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

export function MarkdownContent({
  className,
  html,
}: {
  className?: string;
  html: string;
}) {
  const rootRef = useRef<HTMLDivElement>(null);

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
  }, [html]);

  return (
    <div
      className={`${styles.root} ${className ?? ""}`}
      dangerouslySetInnerHTML={{ __html: html }}
      ref={rootRef}
    />
  );
}
