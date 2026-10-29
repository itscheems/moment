import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { type Editor } from "@tiptap/react";

// --- Hooks ---
import { useTiptapEditor } from "@notion/hooks/use-tiptap-editor";
import { useUiEditorState } from "@notion/hooks/use-ui-editor-state";
import { useIsBreakpoint } from "@notion/hooks/use-is-breakpoint";
import { useFloatingToolbarVisibility } from "@notion/hooks/use-floating-toolbar-visibility";

// --- Node ---
import { ImageNodeFloating } from "@notion/components/nodes/image-node/image-node-floating";

// --- Icons ---
import { MoreVerticalIcon } from "@notion/components/icons/more-vertical-icon";

// --- UI ---
import { ColorTextPopover } from "@notion/components/ui/color-text-popover";
import { LinkPopover } from "@notion/components/ui/link-popover";
import type { Mark } from "@notion/components/ui/mark-button";
import { canToggleMark, MarkButton } from "@notion/components/ui/mark-button";
import type { TextAlign } from "@notion/components/ui/text-align-button";
import {
  canSetTextAlign,
  TextAlignButton,
} from "@notion/components/ui/text-align-button";
import { TurnIntoDropdown } from "@notion/components/ui/turn-into-dropdown";

// --- Utils ---
import { isSelectionValid } from "@notion/lib/editor-collab-utils";

// --- Primitive UI Components ---
import type { ButtonProps } from "@notion/components/primitives/button";
import { Button } from "@notion/components/primitives/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@notion/components/primitives/popover";
import {
  Toolbar,
  ToolbarGroup,
  ToolbarSeparator,
} from "@notion/components/primitives/toolbar";

// --- UI Utils ---
import { FloatingElement } from "@notion/components/utilities/floating-element";

export function NotionToolbarFloating() {
  const { editor } = useTiptapEditor();
  const isMobile = useIsBreakpoint("max", 481);
  const { lockDragHandle, aiGenerationActive, commentInputVisible } =
    useUiEditorState(editor);

  const { shouldShow } = useFloatingToolbarVisibility({
    editor,
    isSelectionValid,
    extraHideWhen: Boolean(aiGenerationActive || commentInputVisible),
  });

  if (lockDragHandle || isMobile) return null;

  return (
    <FloatingElement shouldShow={shouldShow}>
      <Toolbar variant="floating">
        <ToolbarGroup>
          <TurnIntoDropdown hideWhenUnavailable={true} useCardLayout={false} />
        </ToolbarGroup>

        <ToolbarSeparator />

        <ToolbarGroup>
          <MarkButton type="bold" hideWhenUnavailable={true} />
          <MarkButton type="italic" hideWhenUnavailable={true} />
          <MarkButton type="underline" hideWhenUnavailable={true} />
          <MarkButton type="strike" hideWhenUnavailable={true} />
          <MarkButton type="code" hideWhenUnavailable={true} />
        </ToolbarGroup>

        <ToolbarSeparator />

        <ToolbarGroup>
          <ImageNodeFloating />
        </ToolbarGroup>

        <ToolbarGroup>
          <LinkPopover
            autoOpenOnLinkActive={false}
            hideWhenUnavailable={true}
          />
          <ColorTextPopover hideWhenUnavailable={true} />
        </ToolbarGroup>

        <MoreOptions hideWhenUnavailable={true} />
      </Toolbar>
    </FloatingElement>
  );
}

function canMoreOptions(editor: Editor | null): boolean {
  if (!editor) {
    return false;
  }

  const canTextAlignAny = ["left", "center", "right", "justify"].some((align) =>
    canSetTextAlign(editor, align as TextAlign),
  );

  const canMarkAny = ["superscript", "subscript"].some((type) =>
    canToggleMark(editor, type as Mark),
  );

  return canMarkAny || canTextAlignAny;
}

function shouldShowMoreOptions(params: {
  editor: Editor | null;
  hideWhenUnavailable: boolean;
}): boolean {
  const { editor, hideWhenUnavailable } = params;

  if (!editor) {
    return false;
  }

  if (hideWhenUnavailable && !editor.isActive("code")) {
    return canMoreOptions(editor);
  }

  return Boolean(editor?.isEditable);
}

export interface MoreOptionsProps extends Omit<ButtonProps, "type"> {
  /**
   * The Tiptap editor instance.
   */
  editor?: Editor | null;
  /**
   * Whether to hide the dropdown when no options are available.
   * @default false
   */
  hideWhenUnavailable?: boolean;
}

export function MoreOptions({
  editor: providedEditor,
  hideWhenUnavailable = false,
  ...props
}: MoreOptionsProps) {
  const { t } = useTranslation();

  const { editor } = useTiptapEditor(providedEditor);
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (!editor) return;

    const handleSelectionUpdate = () => {
      setShow(
        shouldShowMoreOptions({
          editor,
          hideWhenUnavailable,
        }),
      );
    };

    handleSelectionUpdate();

    editor.on("selectionUpdate", handleSelectionUpdate);

    return () => {
      editor.off("selectionUpdate", handleSelectionUpdate);
    };
  }, [editor, hideWhenUnavailable]);

  if (!show || !editor || !editor.isEditable) {
    return null;
  }

  return (
    <>
      <ToolbarSeparator />
      <ToolbarGroup>
        <Popover>
          <PopoverTrigger asChild>
            <Button
              type="button"
              data-style="ghost"
              role="button"
              tabIndex={-1}
              tooltip={t("More options")}
              {...props}
            >
              <MoreVerticalIcon className="tiptap-button-icon" />
            </Button>
          </PopoverTrigger>

          <PopoverContent
            side="top"
            align="end"
            alignOffset={4}
            sideOffset={4}
            asChild
          >
            <Toolbar variant="floating" tabIndex={0}>
              <ToolbarGroup>
                <MarkButton type="superscript" />
                <MarkButton type="subscript" />
              </ToolbarGroup>

              <ToolbarSeparator />

              <ToolbarGroup>
                <TextAlignButton align="left" />
                <TextAlignButton align="center" />
                <TextAlignButton align="right" />
                <TextAlignButton align="justify" />
              </ToolbarGroup>

              <ToolbarSeparator />
            </Toolbar>
          </PopoverContent>
        </Popover>
      </ToolbarGroup>
    </>
  );
}
