import { getSchema, type JSONContent } from "@tiptap/core";
import {
  TableOfContents,
  generateTocIds,
} from "@tiptap/extension-table-of-contents";
import { contentExtensions } from "./schema";

export const articleOutlineExtensions = [
  ...contentExtensions.article,
  TableOfContents,
];
const outlineSchema = getSchema(articleOutlineExtensions);

/** Upstream's static helper adds anchors without constructing an Editor or mutating storage. */
export function articleWithOutline(document: JSONContent) {
  let index = 0;
  const extensions = [
    ...contentExtensions.article,
    TableOfContents.configure({ getId: () => `article-heading-${++index}` }),
  ];
  return outlineSchema.nodeFromJSON(generateTocIds(document, extensions));
}
