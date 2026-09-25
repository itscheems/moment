import type { Extensions } from "@tiptap/core";
import { Dropcursor, Gapcursor, UndoRedo } from "@tiptap/extensions";
import { ListKeymap } from "@tiptap/extension-list";
import Placeholder from "@tiptap/extension-placeholder";
import { articleContentExtensions } from "../../../components/content/schema";
import { BlockHandles } from "./extensions/BlockHandles";
import { UploadPlaceholder } from "./extensions/UploadPlaceholder";

export {
  ARTICLE_NODE_ATTRS,
  ARTICLE_MARK_ATTRS,
  ARTICLE_SCHEMA_VERSION,
  ARTICLE_CODE_LANGUAGES,
} from "../../../components/content/articleContract";

/** Writing adds interaction to the same document the reader renders. */
export function articleEditorExtensions(
  options: { placeholder?: string } = {},
): Extensions {
  return [
    ...articleContentExtensions(),
    UndoRedo,
    Dropcursor,
    Gapcursor,
    ListKeymap,
    UploadPlaceholder,
    BlockHandles,
    Placeholder.configure({
      placeholder: options.placeholder ?? "输入文字，或按 / 插入内容…",
      showOnlyWhenEditable: true,
    }),
  ];
}
