"use client";

import { forwardRef, useCallback } from "react";

// --- Lib ---
import { parseShortcutKeys } from "@notion/lib/editor-utils";

// --- Hooks ---
import { useTiptapEditor } from "@notion/hooks/use-tiptap-editor";

// --- Tiptap UI ---
import type { UseSlashCommandTriggerConfig } from "@notion/components/ui/slash-command-trigger-button";
import {
  SLASH_COMMAND_TRIGGER_SHORTCUT_KEY,
  useSlashCommandTrigger,
} from "@notion/components/ui/slash-command-trigger-button";

// --- UI Primitives ---
import type { ButtonProps } from "@notion/components/primitives/button";
import { Button } from "@notion/components/primitives/button";
import { Badge } from "@notion/components/primitives/badge";

export interface SlashCommandTriggerButtonProps
  extends Omit<ButtonProps, "type">, UseSlashCommandTriggerConfig {
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

export function SlashCommandShortcutBadge({
  shortcutKeys = SLASH_COMMAND_TRIGGER_SHORTCUT_KEY,
}: {
  shortcutKeys?: string;
}) {
  return <Badge>{parseShortcutKeys({ shortcutKeys })}</Badge>;
}

/**
 * Button component for inserting slash commands in a Tiptap editor.
 *
 * For custom button implementations, use the `useSlashCommand` hook instead.
 */
export const SlashCommandTriggerButton = forwardRef<
  HTMLButtonElement,
  SlashCommandTriggerButtonProps
>(
  (
    {
      editor: providedEditor,
      node,
      nodePos,
      text,
      trigger = "/",
      hideWhenUnavailable = false,
      onTriggered,
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
      canInsert,
      handleSlashCommand,
      label,
      shortcutKeys,
      Icon,
    } = useSlashCommandTrigger({
      editor,
      node,
      nodePos,
      trigger,
      hideWhenUnavailable,
      onTriggered,
    });

    const handleClick = useCallback(
      (event: React.MouseEvent<HTMLButtonElement>) => {
        onClick?.(event);
        if (event.defaultPrevented) return;
        handleSlashCommand();
      },
      [handleSlashCommand, onClick],
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
        disabled={!canInsert}
        data-disabled={!canInsert}
        aria-label={label}
        tooltip={label}
        onClick={handleClick}
        {...buttonProps}
        ref={ref}
      >
        {children ?? (
          <>
            <Icon className="tiptap-button-icon" />
            {text && <span className="tiptap-button-text">{text}</span>}
            {showShortcut && (
              <SlashCommandShortcutBadge shortcutKeys={shortcutKeys} />
            )}
          </>
        )}
      </Button>
    );
  },
);

SlashCommandTriggerButton.displayName = "SlashCommandTriggerButton";
