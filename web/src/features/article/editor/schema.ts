import type { Extensions } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import {
  CharacterCount,
  Dropcursor,
  Gapcursor,
  UndoRedo,
  Selection,
} from "@tiptap/extensions";
import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import { ListKeymap } from "@tiptap/extension-list";
import {
  TableOfContents,
  getHierarchicalIndexes,
} from "@tiptap/extension-table-of-contents";
import Emoji, { gitHubEmojis } from "@tiptap/extension-emoji";
import Mention from "@tiptap/extension-mention";
import { FindAndReplace } from "@tiptap/extension-find-and-replace";
import Typography from "@tiptap/extension-typography";
import { Markdown } from "tiptap-markdown";
import Placeholder from "@tiptap/extension-placeholder";
import { articleContentExtensions } from "../../../components/content/schema";
import { TocNode } from "@notion/components/nodes/toc-node/extensions/toc-node-extension";
import { ListNormalizationExtension } from "@notion/components/extensions/list-normalization-extension";
import { lowlight } from "./lowlight";
import { TableKit } from "@notion/components/nodes/table-node/extensions/table-node-extension";
import { TableHandleExtension } from "@notion/components/nodes/table-node/extensions/table-handle";
import { UiState } from "@notion/components/extensions/ui-state-extension";
import { ManagedNotionImage } from "./ManagedNotionImage";
import { CodeBlockView } from "./CodeBlockView";
import { MentionView } from "./MentionView";
import {
  ImageUploadNode,
  type UploadFunction,
} from "@notion/components/nodes/image-upload-node/image-upload-node-extension";

export {
  ARTICLE_NODE_ATTRS,
  ARTICLE_MARK_ATTRS,
  ARTICLE_SCHEMA_VERSION,
  ARTICLE_CODE_LANGUAGES,
} from "../../../components/content/articleContract";

/** Writing adds interaction to the same document the reader renders. */
export function articleEditorExtensions(
  options: { placeholder?: string; upload?: UploadFunction } = {},
): Extensions {
  return [
    ...articleContentExtensions()
      .filter(
        (extension) =>
          !["table", "tableRow", "tableCell", "tableHeader", "image"].includes(
            extension.name,
          ),
      )
      .map((extension) =>
        extension.name === "codeBlock"
          ? CodeBlockLowlight.extend({
              addNodeView() {
                return ReactNodeViewRenderer(CodeBlockView, {
                  contentDOMElementTag: "code",
                });
              },
            }).configure({
              lowlight,
              enableTabIndentation: true,
            })
          : extension.name === "tocNode"
            ? TocNode.configure({ topOffset: 92 })
            : extension,
      ),
    UndoRedo,
    Dropcursor.configure({ width: 2 }),
    Gapcursor,
    ListKeymap,
    TableKit.configure({ table: { resizable: true, cellMinWidth: 120 } }),
    TableHandleExtension,
    Selection,
    ListNormalizationExtension,
    UiState,
    ManagedNotionImage,
    ImageUploadNode.configure({
      upload: options.upload,
      accept: "image/*",
      maxSize: 20 * 1024 * 1024,
      limit: 3,
    }),
    TableOfContents.configure({ getIndex: getHierarchicalIndexes }),
    Emoji.configure({
      emojis: gitHubEmojis.filter(
        (emoji) => Boolean(emoji.emoji) && !emoji.name.includes("regional"),
      ),
      forceFallbackImages: false,
    }),
    Mention.extend({
      addNodeView() {
        return ReactNodeViewRenderer(MentionView);
      },
    }).configure({ suggestion: { char: "@" } }),
    FindAndReplace.configure({ injectCSS: false }),
    Typography,
    Markdown.configure({
      html: true,
      transformPastedText: true,
      transformCopiedText: true,
    }),
    CharacterCount.configure({
      textCounter: (text) => Array.from(text.replace(/\s/gu, "")).length,
    }),
    Placeholder.configure({
      placeholder: options.placeholder ?? "输入文字，或按 / 插入内容…",
      showOnlyWhenEditable: true,
      emptyNodeClass: "is-empty with-slash",
    }),
  ];
}
