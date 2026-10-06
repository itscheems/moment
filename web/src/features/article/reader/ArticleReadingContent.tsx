import { useEffect, useMemo } from "react";
import { createPortal } from "react-dom";
import { Mark, type JSONContent, type MarkViewProps } from "@tiptap/core";
import {
  EditorContent,
  EditorContext,
  MarkViewContent,
  ReactMarkViewRenderer,
  ReactNodeViewRenderer,
  useEditor,
} from "@tiptap/react";
import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import { TableOfContents } from "@tiptap/extension-table-of-contents";
import { FindAndReplace } from "@tiptap/extension-find-and-replace";
import { SearchAndReplaceButton } from "@notion/components/ui/search-and-replace";
import { TocNode } from "@notion/components/nodes/toc-node/extensions/toc-node-extension";
import { CodeBlockView } from "../editor/CodeBlockView";
import { ManagedNotionImage } from "../editor/ManagedNotionImage";
import { lowlight } from "../editor/lowlight";
import type { ArticleDocument, PublicArticleCover } from "../../../api/public";
import { mediaPreviewUrl } from "../../../api/mediaUrls";
import { useMentionResolver } from "../../../app/mentions";
import { useNavigation } from "../../../app/navigation";
import { HeaderActions } from "../../../components/headerActions";
import { MentionLink } from "../../../components/content/MentionLink";
import { ArticleSearch } from "../ArticleSearch";
import { articleContentExtensions } from "../../../components/content/schema";
import { articleWithOutline } from "../../../components/content/outlineDocument";
import {
  collectHandles,
  HANDLE_MENTION,
  RESERVED_HANDLES,
} from "../../../components/content/mentions";
import styles from "../../../components/content/Content.module.css";
import "../../../components/content/notion.scss";

const NO_MEDIA: readonly PublicArticleCover[] = [];

function ReadingMention({ mark }: MarkViewProps) {
  return (
    <MentionLink handle={mark.attrs.handle}>
      <MarkViewContent />
    </MentionLink>
  );
}

// Reader-only annotations keep mention text searchable and use the existing
// account preview. They never enter the stored article or an editable document.
const ReadingMentionMark = Mark.create({
  name: "readingMention",
  addAttributes: () => ({ handle: { default: "" } }),
  renderHTML: () => ["span", 0],
  addMarkView: () => ReactMarkViewRenderer(ReadingMention),
});

const readingExtensions = articleContentExtensions()
  .map((extension) => {
    if (extension.name === "image") return ManagedNotionImage;
    if (extension.name === "codeBlock") {
      return CodeBlockLowlight.extend({
        addNodeView() {
          return ReactNodeViewRenderer(CodeBlockView, {
            contentDOMElementTag: "code",
          });
        },
      }).configure({ lowlight });
    }
    if (extension.name === "tocNode") return TocNode;
    return extension;
  })
  .concat(
    ReadingMentionMark,
    TableOfContents,
    FindAndReplace.configure({ injectCSS: false }),
  );

/** Native, read-only document view: the same search decorations as writing. */
export function ArticleReadingContent({
  document,
  media = NO_MEDIA,
  searchSlot,
}: {
  document: ArticleDocument;
  media?: readonly PublicArticleCover[];
  searchSlot?: HTMLElement | null;
}) {
  const { consider } = useMentionResolver();
  const { navigate } = useNavigation();
  const content = useMemo(() => {
    const adapt = (node: JSONContent, inCode = false): JSONContent[] => {
      if (
        !inCode &&
        node.type === "text" &&
        !node.marks?.some((mark) => ["link", "code"].includes(mark.type))
      ) {
        const text = node.text ?? "";
        const parts: JSONContent[] = [];
        let cursor = 0;
        for (const match of text.matchAll(HANDLE_MENTION)) {
          const handle = match[2].toLowerCase();
          if (RESERVED_HANDLES.has(handle)) continue;
          const start = (match.index ?? 0) + match[1].length;
          if (start > cursor)
            parts.push({ ...node, text: text.slice(cursor, start) });
          parts.push({
            ...node,
            text: `@${match[2]}`,
            marks: [
              ...(node.marks ?? []),
              { type: "readingMention", attrs: { handle } },
            ],
          });
          cursor = start + match[2].length + 1;
        }
        if (!parts.length) return [node];
        if (cursor < text.length)
          parts.push({ ...node, text: text.slice(cursor) });
        return parts;
      }
      const attrs = { ...node.attrs };
      if (node.type === "image" && typeof attrs.media_id === "string") {
        const geometry = media.find((item) => item.mediaId === attrs.media_id);
        attrs.src = mediaPreviewUrl(attrs.media_id);
        if (!attrs.width && geometry?.width) attrs.width = geometry.width;
        if (!attrs.height && geometry?.height) attrs.height = geometry.height;
      }
      return [
        {
          ...node,
          attrs,
          ...(node.content
            ? {
                content: node.content.flatMap((child) =>
                  adapt(child, inCode || node.type === "codeBlock"),
                ),
              }
            : {}),
        },
      ];
    };
    const outlined = articleWithOutline(document);
    return {
      json: adapt(outlined.toJSON())[0],
      handles: collectHandles(outlined.textContent),
    };
  }, [document, media]);
  useEffect(() => consider(content.handles), [consider, content]);

  const editor = useEditor(
    {
      extensions: readingExtensions,
      content: content.json,
      editable: false,
      immediatelyRender: true,
      shouldRerenderOnTransaction: false,
      editorProps: {
        attributes: {
          class: `${styles.root} ${styles.article}`,
          "data-content-renderer": "tiptap",
          tabindex: "-1",
        },
        handleClick: (_view, _pos, event) => {
          if (
            event.defaultPrevented ||
            event.button !== 0 ||
            event.metaKey ||
            event.ctrlKey ||
            event.shiftKey ||
            event.altKey
          )
            return false;
          const anchor = (event.target as Element | null)?.closest?.("a");
          if (
            !(anchor instanceof HTMLAnchorElement) ||
            anchor.hasAttribute("download") ||
            (anchor.target && anchor.target !== "_self") ||
            anchor.origin !== window.location.origin ||
            (anchor.hash &&
              anchor.pathname === window.location.pathname &&
              anchor.search === window.location.search)
          )
            return false;
          event.preventDefault();
          navigate(`${anchor.pathname}${anchor.search}${anchor.hash}`);
          return true;
        },
      },
    },
    [content],
  );

  if (!editor || editor.isDestroyed) return null;
  return (
    <EditorContext.Provider value={{ editor }}>
      <ArticleSearch editor={editor}>
        {({ open, toggle, buttonRef }) => {
          const trigger = (
            <SearchAndReplaceButton
              ref={buttonRef}
              tabIndex={0}
              aria-expanded={open}
              data-active-state={open ? "on" : "off"}
              onClick={toggle}
            />
          );
          return searchSlot === undefined ? (
            <HeaderActions>{trigger}</HeaderActions>
          ) : searchSlot ? (
            createPortal(trigger, searchSlot)
          ) : null;
        }}
      </ArticleSearch>
      <EditorContent editor={editor} />
    </EditorContext.Provider>
  );
}
