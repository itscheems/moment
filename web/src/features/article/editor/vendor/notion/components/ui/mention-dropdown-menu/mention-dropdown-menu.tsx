"use client";

import { useEffect, useMemo, useRef } from "react";
import { PublicApi } from "../../../../../../../../api/public";
import { mediaPreviewUrl } from "../../../../../../../../api/mediaUrls";
import type { Editor, Range } from "@tiptap/react";

// --- Lib ---
import { getElementOverflowPosition } from "@notion/lib/editor-collab-utils";

// --- Tiptap UI ---
import type {
  SuggestionItem,
  SuggestionMenuProps,
  SuggestionMenuRenderProps,
} from "@notion/components/utilities/suggestion-menu";
import { SuggestionMenu } from "@notion/components/utilities/suggestion-menu";

// --- UI Primitives ---
import {
  Avatar,
  AvatarImage,
  AvatarFallback,
} from "@notion/components/primitives/avatar";
import {
  Button,
  ButtonGroup,
} from "@notion/components/primitives/button";
import { Card, CardBody } from "@notion/components/primitives/card";

interface User {
  id: string;
  name: string;
  position: string;
  avatarUrl: string;
}

type MentionDropdownMenuProps = Omit<SuggestionMenuProps, "items" | "children">;

interface MentionItemProps {
  item: SuggestionItem<User>;
  isSelected: boolean;
  onSelect: () => void;
}

const fetchUsers = async (query: string): Promise<User[]> => {
  const users = await new PublicApi().mentionSuggestions(query);
  return users.map((user) => ({
    id: user.username,
    name: `${user.displayName} · @${user.username}`,
    position: user.username,
    avatarUrl: user.avatarMediaId ? mediaPreviewUrl(user.avatarMediaId) : "",
  }));
};

export const MentionDropdownMenu = (props: MentionDropdownMenuProps) => {
  const handleItemSelect = (props: {
    editor: Editor;
    range: Range;
    context?: User;
  }) => {
    if (!props.editor || !props.range || !props.context) return;

    props.editor
      .chain()
      .focus()
      .insertContentAt(props.range, [
        {
          type: "mention",
          attrs: {
            id: props.context.id.toString(),
            label: props.context.id,
          },
        },
        {
          type: "text",
          text: " ",
        },
      ])
      .run();
  };

  const getSuggestionItems = async (props: { query: string }) => {
    const users = await fetchUsers(props.query);

    return users.map((user) => ({
      title: user.name,
      subtext: user.name,
      context: user,
      onSelect: handleItemSelect,
    }));
  };

  return (
    <SuggestionMenu
      char="@"
      pluginKey="mentionDropdownMenu"
      decorationClass="tiptap-mention-decoration"
      selector="tiptap-mention-dropdown-menu"
      items={getSuggestionItems}
      {...props}
    >
      {(props) => <MentionList {...props} />}
    </SuggestionMenu>
  );
};

const MentionItem = ({ item, isSelected, onSelect }: MentionItemProps) => {
  const itemRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const menuElement = document.querySelector(
      '[data-selector="tiptap-mention-dropdown-menu"]',
    ) as HTMLElement;
    if (!itemRef.current || !isSelected || !menuElement) return;

    const overflow = getElementOverflowPosition(itemRef.current, menuElement);
    if (overflow === "top") {
      itemRef.current.scrollIntoView(true);
    } else if (overflow === "bottom") {
      itemRef.current.scrollIntoView(false);
    }
  }, [isSelected]);

  return (
    <Button
      ref={itemRef}
      data-style="ghost"
      data-active-state={isSelected ? "on" : "off"}
      onClick={onSelect}
      data-user-id={item.context?.id}
    >
      <Avatar>
        <AvatarImage src={item.context?.avatarUrl} alt={item.title} />
        <AvatarFallback>{item.title[0]?.toUpperCase()}</AvatarFallback>
      </Avatar>

      <span className="tiptap-button-text">{item.title}</span>
    </Button>
  );
};

const MentionList = ({
  items,
  selectedIndex,
  onSelect,
}: SuggestionMenuRenderProps<User>) => {
  const renderedItems = useMemo(() => {
    const rendered: React.ReactElement[] = [];

    items.forEach((item, index) => {
      rendered.push(
        <MentionItem
          key={item.context?.id || item.title}
          item={item}
          isSelected={index === selectedIndex}
          onSelect={() => onSelect(item)}
        />,
      );
    });

    return rendered;
  }, [items, selectedIndex, onSelect]);

  if (!renderedItems.length) {
    return null;
  }

  return (
    <Card
      style={{
        maxHeight: "var(--suggestion-menu-max-height)",
      }}
    >
      <CardBody>
        <ButtonGroup>{renderedItems}</ButtonGroup>
      </CardBody>
    </Card>
  );
};
