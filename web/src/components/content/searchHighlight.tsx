import type { ReactNode } from "react";

function ranges(value: string, query: string): Array<[number, number]> {
  if (!query) return [];
  const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return Array.from(value.matchAll(new RegExp(escaped, "giu")), (match) => [
    match.index,
    match.index + match[0].length,
  ]);
}

function markRanges(
  value: string,
  matches: Array<[number, number]>,
  offset = 0,
): ReactNode {
  const result: ReactNode[] = [];
  let cursor = 0;
  for (const [from, to] of matches) {
    const start = Math.max(0, from - offset);
    const end = Math.min(value.length, to - offset);
    if (end <= start) continue;
    result.push(
      value.slice(cursor, start),
      <mark key={start}>{value.slice(start, end)}</mark>,
    );
    cursor = end;
  }
  result.push(value.slice(cursor));
  return result;
}

export function matchedText(value: string, query: string): ReactNode {
  return query ? markRanges(value, ranges(value, query)) : value;
}

/** Match the complete code, even when a query crosses syntax token boundaries. */
export function highlightedCode(
  source: string,
  html: string | null,
  query: string,
): ReactNode {
  if (!html) return matchedText(source, query);
  const matches = ranges(source, query);
  const body = new DOMParser().parseFromString(html, "text/html").body;
  let offset = 0;
  const visit = (node: Node, key: number): ReactNode => {
    if (node.nodeType === Node.TEXT_NODE) {
      const value = node.textContent ?? "";
      const content = markRanges(value, matches, offset);
      offset += value.length;
      return content;
    }
    // highlight.js emits escaped text and spans only, never user markup.
    return (
      <span
        key={key}
        className={(node as Element).getAttribute("class") ?? undefined}
      >
        {Array.from(node.childNodes, visit)}
      </span>
    );
  };
  return Array.from(body.childNodes, visit);
}
