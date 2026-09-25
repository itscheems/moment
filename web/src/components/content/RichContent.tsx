import {
  Children,
  useCallback,
  useEffect,
  useMemo,
  type MouseEvent,
  type ReactNode,
} from "react";
import { getSchema, type JSONContent } from "@tiptap/core";
import { renderToReactElement } from "@tiptap/static-renderer/pm/react";

import type { ArticleDocument, PublicArticleCover } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { useMentionResolver } from "../../app/mentions";
import { useNavigation } from "../../app/navigation";
import { highlightSource } from "../codeHighlight";
import { articleContentExtensions } from "./schema";
import { collectHandles, HANDLE_MENTION, RESERVED_HANDLES } from "./mentions";
import styles from "./Content.module.css";

const extensions = articleContentExtensions();
const schema = getSchema(extensions);
const NO_MEDIA: readonly PublicArticleCover[] = [];

/** A renderer has no selection, transactions, plugins, or Editor instance. */
export function RichContent({
  document,
  media = NO_MEDIA,
  className = "",
  density = "article",
}: {
  document: ArticleDocument | JSONContent;
  media?: readonly PublicArticleCover[];
  className?: string;
  density?: "article" | "compact";
}) {
  const { navigate } = useNavigation();
  const { resolves, consider } = useMentionResolver();
  const node = useMemo(() => schema.nodeFromJSON(document), [document]);
  const handles = useMemo(() => collectHandles(node.textContent), [node]);
  useEffect(() => consider(handles), [consider, handles]);
  const content = useMemo(
    () =>
      renderToReactElement({
        extensions,
        content: node,
        options: {
          nodeMapping: {
            paragraph: ({ children }) => (
              <p>{Children.count(children) ? children : <br />}</p>
            ),
            text: ({ node: text }) => {
              const value = text.text ?? "";
              if (
                text.marks.some(
                  (mark) =>
                    mark.type.name === "link" || mark.type.name === "code",
                )
              )
                return value;
              const parts: ReactNode[] = [];
              let cursor = 0;
              for (const match of value.matchAll(HANDLE_MENTION)) {
                const handle = match[2].toLowerCase();
                if (RESERVED_HANDLES.has(handle) || !resolves(handle)) continue;
                const start = (match.index ?? 0) + match[1].length;
                parts.push(value.slice(cursor, start));
                parts.push(
                  <a
                    key={start}
                    href={`/${handle}`}
                    data-moment-mention={handle}
                  >
                    @{match[2]}
                  </a>,
                );
                cursor = start + match[2].length + 1;
              }
              parts.push(value.slice(cursor));
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
                    className={`${language ? `language-${language}` : ""}${highlighted ? " hljs" : ""}`}
                    {...(highlighted
                      ? { dangerouslySetInnerHTML: { __html: highlighted } }
                      : { children: code.textContent })}
                  />
                </pre>
              );
            },
            image: ({ node: picture }) => {
              const id: string = picture.attrs.media_id;
              const geometry = media.find((item) => item.mediaId === id);
              return (
                <img
                  src={mediaPreviewUrl(id)}
                  data-media-id={id}
                  alt={picture.attrs.alt ?? "文章图片"}
                  width={geometry?.width}
                  height={geometry?.height}
                  loading="lazy"
                  decoding="async"
                />
              );
            },
          },
        },
      }),
    [node, media, resolves],
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
      className={`${styles.root} ${styles[density]} ${className}`}
      data-content-renderer="tiptap"
      onClick={followInternalLink}
    >
      {content}
    </div>
  );
}
