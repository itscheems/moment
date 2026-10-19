import { MapNode } from "./MapNode";
import { Mark, getSchema, type Extensions } from "@tiptap/core";
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
import {
  BulletList,
  ListItem,
  OrderedList,
  TaskList,
  TaskItem,
} from "@tiptap/extension-list";
import TextAlign from "@tiptap/extension-text-align";
import { TextStyle, Color } from "@tiptap/extension-text-style";
import Highlight from "@tiptap/extension-highlight";
import Superscript from "@tiptap/extension-superscript";
import Subscript from "@tiptap/extension-subscript";
import UniqueID from "@tiptap/extension-unique-id";
import { NodeBackground } from "@content-notion/components/extensions/node-background-extension";
import { NodeAlignment } from "@content-notion/components/extensions/node-alignment-extension";
import { TocNode } from "@content-notion/components/nodes/toc-node/extensions/toc-node-extension";
import {
  Table,
  TableCell,
  TableHeader,
  TableRow,
} from "@tiptap/extension-table";
import Underline from "@tiptap/extension-underline";
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
    Blockquote,
    BulletList,
    OrderedList,
    ListItem,
    TaskList,
    TaskItem.configure({ nested: true }),
    TextAlign.configure({ types: ["heading", "paragraph"] }),
    TextStyle,
    Color,
    Highlight.configure({ multicolor: true }),
    Superscript,
    Subscript,
    NodeBackground.configure({
      types: [
        "paragraph",
        "heading",
        "blockquote",
        "taskList",
        "bulletList",
        "orderedList",
        "tableCell",
        "tableHeader",
        "tocNode",
      ],
    }),
    NodeAlignment,
    UniqueID.configure({
      types: [
        "table",
        "paragraph",
        "bulletList",
        "orderedList",
        "taskList",
        "heading",
        "blockquote",
        "codeBlock",
        "tocNode",
      ],
    }),
    TocNode,
    Underline,
    Link.configure({
      openOnClick: false,
      autolink: true,
      linkOnPaste: true,
      defaultProtocol: "https",
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
    Table.configure({
      resizable: false,
      renderWrapper: true,
      cellMinWidth: 120,
    }),
    TableRow,
    TableHeader,
    TableCell,
    ManagedImage,
    MapNode,
  ];
}

/** Markdown keeps its existing grammar; only its presentation is shared. */
function markdownContentExtensions(): Extensions {
  const SearchMatch = Mark.create({
    name: "searchMatch",
    parseHTML: () => [{ tag: "mark" }],
    renderHTML: () => ["mark", 0],
  });
  return articleContentExtensions()
    .map((extension) => {
      switch (extension.name) {
        case "heading":
          return Heading.extend({
            addAttributes() {
              return { ...this.parent?.(), id: { default: null } };
            },
          }).configure({ levels: [1, 2, 3, 4, 5, 6] });
        case "blockquote":
          return Blockquote;
        case "listItem":
          return ListItem;
        case "tableCell":
          return TableCell;
        case "tableHeader":
          return TableHeader;
        case "link":
          return Link.configure({
            openOnClick: false,
            autolink: false,
            HTMLAttributes: {
              target: null,
              rel: "nofollow noopener noreferrer",
            },
            isAllowedUri: (url) => isAllowedArticleHref(url),
          });
        default:
          return extension;
      }
    })
    .concat(SearchMatch);
}

export const contentExtensions = {
  article: articleContentExtensions(),
  markdown: markdownContentExtensions(),
};
export const contentSchemas = {
  article: getSchema(contentExtensions.article),
  markdown: getSchema(contentExtensions.markdown),
};
