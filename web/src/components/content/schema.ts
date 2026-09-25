import type { Extensions } from "@tiptap/core";
import Document from "@tiptap/extension-document";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import Bold from "@tiptap/extension-bold";
import Italic from "@tiptap/extension-italic";
import Strike from "@tiptap/extension-strike";
import Code from "@tiptap/extension-code";
import CodeBlock from "@tiptap/extension-code-block";
import Heading from "@tiptap/extension-heading";
import HardBreak from "@tiptap/extension-hard-break";
import HorizontalRule from "@tiptap/extension-horizontal-rule";
import Blockquote from "@tiptap/extension-blockquote";
import Link from "@tiptap/extension-link";
import { BulletList, ListItem, OrderedList } from "@tiptap/extension-list";
import {
  Table,
  TableCell,
  TableHeader,
  TableRow,
} from "@tiptap/extension-table";
import Underline from "@tiptap/extension-underline";
import { BLOCKS_IN_CELL, BLOCKS_IN_QUOTE } from "./articleContract";
import { isAllowedArticleHref } from "./links";
import { ManagedImage } from "./ManagedImage";

/** One grammar and renderHTML definition for writing and reading articles. */
export function articleContentExtensions(): Extensions {
  return [
    Document,
    Paragraph,
    Text,
    Bold,
    Italic,
    Strike,
    Code,
    HardBreak,
    HorizontalRule,
    Heading.configure({ levels: [1, 2, 3] }),
    CodeBlock.configure({ languageClassPrefix: "language-" }),
    Blockquote.extend({ content: `(${BLOCKS_IN_QUOTE})+` }),
    BulletList,
    OrderedList.extend({
      // Tiptap 3 also carries `type`, for `<ol type="a">`. The server has no
      // such attribute, so the node is rebuilt with `start` alone.
      addAttributes() {
        return {
          start: {
            default: 1,
            parseHTML: (element) => {
              const start = element.getAttribute("start");
              return start ? Number.parseInt(start, 10) : 1;
            },
          },
        };
      },
    }),
    ListItem.extend({ content: `paragraph (${BLOCKS_IN_QUOTE})*` }),
    Underline,
    Link.configure({
      openOnClick: false,
      autolink: true,
      linkOnPaste: true,
      defaultProtocol: "https",
      protocols: ["http", "https", "mailto"],
      // Rendered, not stored: the document carries only `href`.
      HTMLAttributes: { rel: "nofollow noopener noreferrer", target: null },
      isAllowedUri: (url) => isAllowedArticleHref(url),
    }).extend({
      addAttributes() {
        return {
          href: { default: null },
        };
      },
    }),
    Table.configure({ resizable: false, renderWrapper: true }),
    TableRow.extend({ content: "(tableCell | tableHeader)+" }),
    TableHeader.extend({
      content: `(${BLOCKS_IN_CELL})+`,
      addAttributes() {
        return {
          colspan: { default: 1 },
          rowspan: { default: 1 },
        };
      },
    }),
    TableCell.extend({
      content: `(${BLOCKS_IN_CELL})+`,
      addAttributes() {
        return {
          colspan: { default: 1 },
          rowspan: { default: 1 },
        };
      },
    }),
    ManagedImage,
  ];
}
