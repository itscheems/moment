"use client";

import { t } from "../../../../../../../../i18n";

import { useCallback } from "react";
import type { Editor } from "@tiptap/react";

// --- Icons ---
import { CodeBlockIcon } from "@notion/components/icons/code-block-icon";
import { HeadingOneIcon } from "@notion/components/icons/heading-one-icon";
import { HeadingTwoIcon } from "@notion/components/icons/heading-two-icon";
import { HeadingThreeIcon } from "@notion/components/icons/heading-three-icon";
import { ImageIcon } from "@notion/components/icons/image-icon";
import { ListIcon } from "@notion/components/icons/list-icon";
import { ListOrderedIcon } from "@notion/components/icons/list-ordered-icon";
import { BlockquoteIcon } from "@notion/components/icons/blockquote-icon";
import { ListTodoIcon } from "@notion/components/icons/list-todo-icon";
import { MinusIcon } from "@notion/components/icons/minus-icon";
import { TypeIcon } from "@notion/components/icons/type-icon";
import { AtSignIcon } from "@notion/components/icons/at-sign-icon";
import { SmilePlusIcon } from "@notion/components/icons/smile-plus-icon";
import { TableIcon } from "@notion/components/icons/table-icon";
import { ListIndentedIcon } from "@notion/components/icons/list-indented-icon";

// --- Lib ---
import { isExtensionAvailable, isNodeInSchema } from "@notion/lib/editor-utils";

// --- Tiptap UI ---
import type { SuggestionItem } from "@notion/components/utilities/suggestion-menu";
import { addEmojiTrigger } from "@notion/components/ui/emoji-trigger-button";
import { addMentionTrigger } from "@notion/components/ui/mention-trigger-button";

export interface SlashMenuConfig {
  enabledItems?: SlashMenuItemType[];
  customItems?: SuggestionItem[];
  itemGroups?: {
    [key in SlashMenuItemType]?: string;
  };
  showGroups?: boolean;
}

const texts = {
  // Style
  text: {
    get title() {
      return t("Text");
    },
    get subtext() {
      return t("Regular text paragraph");
    },
    keywords: ["p", "paragraph", "text"],
    badge: TypeIcon,
    get group() {
      return t("Style");
    },
  },
  heading_1: {
    get title() {
      return t("Heading 1");
    },
    get subtext() {
      return t("Top-level heading");
    },
    keywords: ["h", "heading1", "h1"],
    badge: HeadingOneIcon,
    get group() {
      return t("Style");
    },
  },
  heading_2: {
    get title() {
      return t("Heading 2");
    },
    get subtext() {
      return t("Key section heading");
    },
    keywords: ["h2", "heading2", "subheading"],
    badge: HeadingTwoIcon,
    get group() {
      return t("Style");
    },
  },
  heading_3: {
    get title() {
      return t("Heading 3");
    },
    get subtext() {
      return t("Subsection and group heading");
    },
    keywords: ["h3", "heading3", "subheading"],
    badge: HeadingThreeIcon,
    get group() {
      return t("Style");
    },
  },
  bullet_list: {
    get title() {
      return t("Bullet List");
    },
    get subtext() {
      return t("List with unordered items");
    },
    keywords: ["ul", "li", "list", "bulletlist", "bullet list"],
    badge: ListIcon,
    get group() {
      return t("Style");
    },
  },
  ordered_list: {
    get title() {
      return t("Numbered List");
    },
    get subtext() {
      return t("List with ordered items");
    },
    keywords: ["ol", "li", "list", "numberedlist", "numbered list"],
    badge: ListOrderedIcon,
    get group() {
      return t("Style");
    },
  },
  task_list: {
    get title() {
      return t("To-do list");
    },
    get subtext() {
      return t("List with tasks");
    },
    keywords: ["tasklist", "task list", "todo", "checklist"],
    badge: ListTodoIcon,
    get group() {
      return t("Style");
    },
  },
  quote: {
    get title() {
      return t("Blockquote");
    },
    get subtext() {
      return t("Blockquote block");
    },
    keywords: ["quote", "blockquote"],
    badge: BlockquoteIcon,
    get group() {
      return t("Style");
    },
  },
  code_block: {
    get title() {
      return t("Code Block");
    },
    get subtext() {
      return t("Code block with syntax highlighting");
    },
    keywords: ["code", "pre"],
    badge: CodeBlockIcon,
    get group() {
      return t("Style");
    },
  },

  // Insert
  mention: {
    get title() {
      return t("Mention");
    },
    get subtext() {
      return t("Mention a user or item");
    },
    keywords: ["mention", "user", "item", "tag"],
    badge: AtSignIcon,
    get group() {
      return t("Insert");
    },
  },
  emoji: {
    get title() {
      return t("Emoji");
    },
    get subtext() {
      return t("Insert an emoji");
    },
    keywords: ["emoji", "emoticon", "smiley"],
    badge: SmilePlusIcon,
    get group() {
      return t("Insert");
    },
  },
  table: {
    get title() {
      return t("Table");
    },
    get subtext() {
      return t("Insert a table");
    },
    aliases: ["table", "insertTable"],
    badge: TableIcon,
    get group() {
      return t("Insert");
    },
  },
  divider: {
    get title() {
      return t("Separator");
    },
    get subtext() {
      return t("Horizontal line to separate content");
    },
    keywords: ["hr", "horizontalRule", "line", "separator"],
    badge: MinusIcon,
    get group() {
      return t("Insert");
    },
  },
  toc: {
    get title() {
      return t("Table of contents");
    },
    get subtext() {
      return t("Insert a table of contents");
    },
    keywords: ["toc", "tableofcontents", "table of contents"],
    badge: ListIndentedIcon,
    get group() {
      return t("Insert");
    },
  },

  // Upload
  image: {
    get title() {
      return t("Image");
    },
    get subtext() {
      return t("Resizable image with caption");
    },
    keywords: [
      "image",
      "imageUpload",
      "upload",
      "img",
      "picture",
      "media",
      "url",
    ],
    badge: ImageIcon,
    get group() {
      return t("Upload");
    },
  },
};

export type SlashMenuItemType = keyof typeof texts;

const getItemImplementations = () => {
  return {
    // Style
    text: {
      check: (editor: Editor) => isNodeInSchema("paragraph", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().setParagraph().run();
      },
    },
    heading_1: {
      check: (editor: Editor) => isNodeInSchema("heading", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleHeading({ level: 1 }).run();
      },
    },
    heading_2: {
      check: (editor: Editor) => isNodeInSchema("heading", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleHeading({ level: 2 }).run();
      },
    },
    heading_3: {
      check: (editor: Editor) => isNodeInSchema("heading", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleHeading({ level: 3 }).run();
      },
    },
    bullet_list: {
      check: (editor: Editor) => isNodeInSchema("bulletList", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleBulletList().run();
      },
    },
    ordered_list: {
      check: (editor: Editor) => isNodeInSchema("orderedList", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleOrderedList().run();
      },
    },
    task_list: {
      check: (editor: Editor) => isNodeInSchema("taskList", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleTaskList().run();
      },
    },
    quote: {
      check: (editor: Editor) => isNodeInSchema("blockquote", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleBlockquote().run();
      },
    },
    code_block: {
      check: (editor: Editor) => isNodeInSchema("codeBlock", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().toggleNode("codeBlock", "paragraph").run();
      },
    },

    // Insert
    mention: {
      check: (editor: Editor) =>
        isExtensionAvailable(editor, ["mention", "mentionAdvanced"]),
      action: ({ editor }: { editor: Editor }) => addMentionTrigger(editor),
    },
    emoji: {
      check: (editor: Editor) =>
        isExtensionAvailable(editor, ["emoji", "emojiPicker"]),
      action: ({ editor }: { editor: Editor }) => addEmojiTrigger(editor),
    },
    divider: {
      check: (editor: Editor) => isNodeInSchema("horizontalRule", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().setHorizontalRule().run();
      },
    },
    toc: {
      check: (editor: Editor) => isNodeInSchema("tocNode", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor.chain().focus().insertTocNode().run();
      },
    },
    table: {
      check: (editor: Editor) => isNodeInSchema("table", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor
          .chain()
          .focus()
          .insertTable({
            rows: 3,
            cols: 3,
            withHeaderRow: false,
          })
          .run();
      },
    },

    // Upload
    image: {
      check: (editor: Editor) => isNodeInSchema("image", editor),
      action: ({ editor }: { editor: Editor }) => {
        editor
          .chain()
          .focus()
          .insertContent({
            type: "imageUpload",
          })
          .run();
      },
    },
  };
};

function organizeItemsByGroups(
  items: SuggestionItem[],
  showGroups: boolean,
): SuggestionItem[] {
  if (!showGroups) {
    return items.map((item) => ({ ...item, group: "" }));
  }

  const groups: { [groupLabel: string]: SuggestionItem[] } = {};

  // Group items
  items.forEach((item) => {
    const groupLabel = item.group || "";
    if (!groups[groupLabel]) {
      groups[groupLabel] = [];
    }
    groups[groupLabel].push(item);
  });

  // Flatten groups in order (this maintains the visual order for keyboard navigation)
  const organizedItems: SuggestionItem[] = [];
  Object.entries(groups).forEach(([, groupItems]) => {
    organizedItems.push(...groupItems);
  });

  return organizedItems;
}

/**
 * Custom hook for slash dropdown menu functionality
 */
export function useSlashDropdownMenu(config?: SlashMenuConfig) {
  const getSlashMenuItems = useCallback(
    (editor: Editor) => {
      const items: SuggestionItem[] = [];

      const enabledItems =
        config?.enabledItems || (Object.keys(texts) as SlashMenuItemType[]);
      const showGroups = config?.showGroups !== false;

      const itemImplementations = getItemImplementations();

      enabledItems.forEach((itemType) => {
        const itemImpl = itemImplementations[itemType];
        const itemText = texts[itemType];

        if (itemImpl && itemText && itemImpl.check(editor)) {
          const item: SuggestionItem = {
            onSelect: ({ editor }) => itemImpl.action({ editor }),
            ...itemText,
          };

          if (config?.itemGroups?.[itemType]) {
            item.group = config.itemGroups[itemType];
          } else if (!showGroups) {
            item.group = "";
          }

          items.push(item);
        }
      });

      if (config?.customItems) {
        items.push(...config.customItems);
      }

      // Reorganize items by groups to ensure keyboard navigation works correctly
      return organizeItemsByGroups(items, showGroups);
    },
    [config],
  );

  return {
    getSlashMenuItems,
    config,
  };
}
