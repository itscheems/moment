"use client";

import { forwardRef, useCallback } from "react";

// --- Lib ---
import { parseShortcutKeys } from "@notion/lib/editor-utils";

// --- Hooks ---
import { useTiptapEditor } from "@notion/hooks/use-tiptap-editor";

// --- Tiptap UI ---
import type { UseEmojiTriggerConfig } from "@notion/components/ui/emoji-trigger-button";
import {
  EMOJI_TRIGGER_SHORTCUT_KEY,
  useEmojiTrigger,
} from "@notion/components/ui/emoji-trigger-button";

// --- UI Primitives ---
import type { ButtonProps } from "@notion/components/primitives/button";
import { Button } from "@notion/components/primitives/button";
import { Badge } from "@notion/components/primitives/badge";

export interface EmojiTriggerButtonProps
  extends Omit<ButtonProps, "type">, UseEmojiTriggerConfig {
  /**
   * Optional text to display alongside the icon.
   */
  text?: string;
  /**
   * Optional show shortcut keys in the button.
   * @default false
   */
  showShortcut?: boolean;
}

export function EmojiTriggerShortcutBadge({
  shortcutKeys = EMOJI_TRIGGER_SHORTCUT_KEY,
}: {
  shortcutKeys?: string;
}) {
  return <Badge>{parseShortcutKeys({ shortcutKeys })}</Badge>;
}

/**
 * Button component for adding emoji trigger in a Tiptap editor.
 *
 * For custom button implementations, use the `useEmojiTrigger` hook instead.
 */
export const EmojiTriggerButton = forwardRef<
  HTMLButtonElement,
  EmojiTriggerButtonProps
>(
  (
    {
      editor: providedEditor,
      node,
      nodePos,
      text,
      trigger = ":",
      hideWhenUnavailable = false,
      onTriggerApplied,
      showShortcut = false,
      onClick,
      children,
      ...buttonProps
    },
    ref,
  ) => {
    const { editor } = useTiptapEditor(providedEditor);
    const {
      isVisible,
      canAddTrigger,
      handleAddTrigger,
      label,
      shortcutKeys,
      Icon,
    } = useEmojiTrigger({
      editor,
      node,
      nodePos,
      trigger,
      hideWhenUnavailable,
      onTriggerApplied,
    });

    const handleClick = useCallback(
      (event: React.MouseEvent<HTMLButtonElement>) => {
        onClick?.(event);
        if (event.defaultPrevented) return;
        handleAddTrigger();
      },
      [handleAddTrigger, onClick],
    );

    if (!isVisible) {
      return null;
    }

    return (
      <Button
        type="button"
        data-style="ghost"
        role="button"
        tabIndex={-1}
        disabled={!canAddTrigger}
        data-disabled={!canAddTrigger}
        aria-label={label}
        tooltip="Add emoji"
        onClick={handleClick}
        {...buttonProps}
        ref={ref}
      >
        {children ?? (
          <>
            <Icon className="tiptap-button-icon" />
            {text && <span className="tiptap-button-text">{text}</span>}
            {showShortcut && (
              <EmojiTriggerShortcutBadge shortcutKeys={shortcutKeys} />
            )}
          </>
        )}
      </Button>
    );
  },
);

EmojiTriggerButton.displayName = "EmojiTriggerButton";
