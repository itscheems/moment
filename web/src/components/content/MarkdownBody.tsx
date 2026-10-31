import { SourceMentionProvider } from "../../app/mentions";
import type { SubjectKind } from "../../api/interactions";
import { useMemo } from "react";
import { DOMParser as DocumentParser } from "@tiptap/pm/model";
import { contentSchemas } from "./schema";
import { RichContent } from "./RichContent";

const parser = DocumentParser.fromSchema(contentSchemas.markdown);

/** Goldmark owns Markdown interpretation; Tiptap owns the displayed document. */
export function parseRenderedMarkdown(html: string) {
  const source = new DOMParser().parseFromString(html, "text/html").body;
  // HTML serialization adds formatting newlines between blocks. Whitespace
  // inside prose is significant in Moment, including ordinary soft breaks.
  const walker = source.ownerDocument.createTreeWalker(
    source,
    NodeFilter.SHOW_TEXT,
  );
  const formatting: Text[] = [];
  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    const parent = node.parentElement;
    if (
      !node.data.trim() &&
      parent &&
      (/^(BODY|DIV|BLOCKQUOTE|UL|OL|TABLE|THEAD|TBODY|TR)$/.test(
        parent.tagName,
      ) ||
        (parent.tagName === "LI" && parent.querySelector(":scope > p")))
    )
      formatting.push(node);
  }
  formatting.forEach((node) => node.remove());
  for (const br of source.querySelectorAll("br")) {
    if (br.nextSibling?.nodeType === Node.TEXT_NODE) {
      const text = br.nextSibling as Text;
      text.data = text.data.replace(/^\r?\n/, "");
    }
  }
  return parser.parse(source, { preserveWhitespace: "full" }).toJSON();
}

export function MarkdownBody({
  html,
  className,
  searchQuery,
  source,
}: {
  html: string;
  className?: string;
  searchQuery?: string;
  source?: { kind: SubjectKind; id: string };
}) {
  const document = useMemo(() => parseRenderedMarkdown(html), [html]);
  const content = (
    <RichContent
      document={document}
      searchQuery={searchQuery}
      format="markdown"
      density="compact"
      className={className}
    />
  );
  return source ? (
    <SourceMentionProvider kind={source.kind} id={source.id} revision={html}>
      {content}
    </SourceMentionProvider>
  ) : (
    content
  );
}
