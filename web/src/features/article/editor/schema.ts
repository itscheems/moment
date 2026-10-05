import type { Extensions } from "@tiptap/core";
import {
  CharacterCount,
  Dropcursor,
  Gapcursor,
  UndoRedo,
  Selection,
} from "@tiptap/extensions";
import CodeBlockLowlight from "@tiptap/extension-code-block-lowlight";
import { ListKeymap } from "@tiptap/extension-list";
import { TableOfContents } from "@tiptap/extension-table-of-contents";
import Placeholder from "@tiptap/extension-placeholder";
import { articleContentExtensions } from "../../../components/content/schema";
import { TocNode } from "@notion/components/nodes/toc-node/extensions/toc-node-extension";
import { ListNormalizationExtension } from "@notion/components/extensions/list-normalization-extension";
import { lowlight } from "./lowlight";
import { TableKit } from "@notion/components/nodes/table-node/extensions/table-node-extension";
import { TableHandleExtension } from "@notion/components/nodes/table-node/extensions/table-handle";
import { UiState } from "@notion/components/extensions/ui-state-extension";
import { ManagedNotionImage } from "./ManagedNotionImage";
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
          ? CodeBlockLowlight.configure({
              lowlight,
              enableTabIndentation: true,
            })
          : extension.name === "tocNode"
            ? TocNode.configure({ topOffset: 92 })
            : extension,
      ),
    UndoRedo,
    Dropcursor,
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
    TableOfContents,
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
