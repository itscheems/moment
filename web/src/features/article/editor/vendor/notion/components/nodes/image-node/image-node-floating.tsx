import { useTranslation } from "react-i18next";
import type { Editor } from "@tiptap/react";

// --- Hooks ---
import { useTiptapEditor } from "@notion/hooks/use-tiptap-editor";

// --- Lib ---
import { isNodeTypeSelected } from "@notion/lib/editor-utils";

// --- Tiptap UI ---
import { DeleteNodeButton } from "@notion/components/ui/delete-node-button";
import { ImageDownloadButton } from "@notion/components/ui/image-download-button";
import { ImageAlignButton } from "@notion/components/ui/image-align-button";

// --- UI Primitive ---
import { Separator } from "@notion/components/primitives/separator";
import { ImageCaptionButton } from "@notion/components/ui/image-caption-button";
import { ImageUploadButton } from "@notion/components/ui/image-upload-button";
import { RefreshCcwIcon } from "@notion/components/icons/refresh-ccw-icon";

export function ImageNodeFloating({
  editor: providedEditor,
}: {
  editor?: Editor | null;
}) {
  const { t } = useTranslation();

  const { editor } = useTiptapEditor(providedEditor);
  const visible = isNodeTypeSelected(editor, ["image"]);

  if (!editor || !visible) {
    return null;
  }

  return (
    <>
      <ImageAlignButton align="left" />
      <ImageAlignButton align="center" />
      <ImageAlignButton align="right" />
      <Separator />
      <ImageCaptionButton />
      <Separator />
      <ImageDownloadButton />
      <ImageUploadButton icon={RefreshCcwIcon} tooltip={t("Replace")} />
      <Separator />
      <DeleteNodeButton />
    </>
  );
}
