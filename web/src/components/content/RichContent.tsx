import {
  Children,
  useCallback,
  useEffect,
  useMemo,
  type MouseEvent,
  type ReactNode,
} from "react";
import { TocContent } from "@content-notion/components/nodes/toc-node/toc-content";
import type { JSONContent } from "@tiptap/core";
import { renderToReactElement } from "@tiptap/static-renderer/pm/react";

import type { ArticleDocument, PublicArticleCover } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { useMentionResolver } from "../../app/mentions";
import { useNavigation } from "../../app/navigation";
import { highlightSource } from "../codeHighlight";
import { matchedText, highlightedCode } from "./searchHighlight";
import { contentExtensions, contentSchemas } from "./schema";
import { collectHandles, HANDLE_MENTION, RESERVED_HANDLES } from "./mentions";
import styles from "./Content.module.css";
import "./notion.scss";
import {
  articleOutlineExtensions,
  articleWithOutline,
} from "./outlineDocument";

const NO_MEDIA: readonly PublicArticleCover[] = [];

/** A renderer has no selection, transactions, plugins, or Editor instance. */
export function RichContent({
  document,
  media = NO_MEDIA,
  className = "",
  density = "article",
  format = "article",
  searchQuery = "",
}: {
  document: ArticleDocument | JSONContent;
  format?: "article" | "markdown";
  searchQuery?: string;
  media?: readonly PublicArticleCover[];
  className?: string;
  density?: "article" | "compact";
}) {
  const { navigate } = useNavigation();
  const schema = contentSchemas[format];
  const extensions =
    format === "article" ? articleOutlineExtensions : contentExtensions[format];
  const { resolves, consider } = useMentionResolver();
  const node = useMemo(
    () =>
      format === "article"
        ? articleWithOutline(document)
        : schema.nodeFromJSON(document),
    [document, schema, format],
  );
  const handles = useMemo(() => collectHandles(node.textContent), [node]);
  useEffect(() => consider(handles), [consider, handles]);
  const renderedNode = useMemo(() => {
    const adapt = (value: JSONContent): JSONContent => {
      const attrs = { ...value.attrs };
      if (value.type === "image" && typeof attrs.media_id === "string") {
        const geometry = media.find((item) => item.mediaId === attrs.media_id);
        attrs.src = mediaPreviewUrl(attrs.media_id);
        if (!attrs.width && geometry?.width) attrs.width = geometry.width;
        if (!attrs.height && geometry?.height) attrs.height = geometry.height;
      }
      return {
        ...value,
        attrs,
        ...(value.content ? { content: value.content.map(adapt) } : {}),
      };
    };
    return node.type.schema.nodeFromJSON(adapt(node.toJSON()));
  }, [node, media]);
  const content = useMemo(
    () =>
      renderToReactElement({
        extensions,
        content: renderedNode,
        options: {
          nodeMapping: {
            tocNode: ({ node }) => <TocContent attrs={node.attrs} />,
            paragraph: ({ children, node }) => (
              <p
                style={{
                  textAlign:
                    node.attrs.textAlign ??
                    node.attrs.nodeTextAlign ??
                    undefined,
                  backgroundColor: node.attrs.backgroundColor ?? undefined,
                }}
                id={node.attrs.id ?? undefined}
              >
                {Children.count(children) ? children : <br />}
              </p>
            ),
            text: ({ node: text }) => {
              const value = text.text ?? "";
              if (
                text.marks.some(
                  (mark) =>
                    mark.type.name === "link" || mark.type.name === "code",
                )
              )
                return matchedText(value, searchQuery);
              const parts: ReactNode[] = [];
              let cursor = 0;
              for (const match of value.matchAll(HANDLE_MENTION)) {
                const handle = match[2].toLowerCase();
                if (RESERVED_HANDLES.has(handle) || !resolves(handle)) continue;
                const start = (match.index ?? 0) + match[1].length;
                parts.push(
                  matchedText(value.slice(cursor, start), searchQuery),
                );
                parts.push(
                  <a
                    key={start}
                    href={`/${handle}`}
                    data-moment-mention={handle}
                  >
                    {matchedText(`@${match[2]}`, searchQuery)}
                  </a>,
                );
                cursor = start + match[2].length + 1;
              }
              parts.push(matchedText(value.slice(cursor), searchQuery));
              return parts;
            },
            codeBlock: ({ node: code }) => {
              const language =
                typeof code.attrs.language === "string"
                  ? code.attrs.language
                  : "";
              const highlighted = highlightSource(code.textContent, language);
              return (
                <pre>
                  <code
                    className={language ? `language-${language}` : undefined}
                    {...(searchQuery
                      ? {
                          children: highlightedCode(
                            code.textContent,
                            highlighted,
                            searchQuery,
                          ),
                        }
                      : highlighted
                        ? { dangerouslySetInnerHTML: { __html: highlighted } }
                        : { children: code.textContent })}
                  />
                </pre>
              );
            },
          },
        },
      }),
    [renderedNode, resolves, extensions, searchQuery],
  );

  const followInternalLink = useCallback(
    (event: MouseEvent<HTMLDivElement>) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      const anchor = (event.target as Element | null)?.closest?.("a");
      if (
        !(anchor instanceof HTMLAnchorElement) ||
        anchor.hasAttribute("download") ||
        (anchor.target && anchor.target !== "_self") ||
        anchor.origin !== window.location.origin
      )
        return;
      event.preventDefault();
      navigate(`${anchor.pathname}${anchor.search}${anchor.hash}`);
    },
    [navigate],
  );

  return (
    <div
      className={`tiptap ProseMirror ${styles.root} ${styles[density]} ${className}`}
      data-content-renderer="tiptap"
      onClick={followInternalLink}
    >
      {content}
    </div>
  );
}
