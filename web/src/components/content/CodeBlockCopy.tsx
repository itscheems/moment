import { useEffect, useRef } from "react";
import { useClipboard } from "@mantine/hooks";
import { toast } from "sonner";
import { Button } from "@content-notion/components/primitives/button/button";
import { CopyIcon } from "@content-notion/components/icons/copy-icon";
import { CheckIcon } from "@content-notion/components/icons/check-icon";
import { CODE_COPY_TIMEOUT, CODE_COPY_TOASTER } from "./CodeCopyFeedback";

/** Share the documentation's clipboard hook and toast engine across both views. */
export function CodeBlockCopy({ source }: { source: string }) {
  const clipboard = useClipboard({ timeout: CODE_COPY_TIMEOUT });
  const toastId = useRef<string | number | undefined>(undefined);
  useEffect(() => {
    if (clipboard.error) {
      if (toastId.current !== undefined) toast.dismiss(toastId.current);
      toast.error("暂时无法复制代码", {
        toasterId: CODE_COPY_TOASTER,
        duration: 6_000,
      });
    }
  }, [clipboard.error]);

  const copy = () => {
    clipboard.copy(source);
    toastId.current = toast("已复制到剪贴板", {
      toasterId: CODE_COPY_TOASTER,
      duration: CODE_COPY_TIMEOUT,
    });
  };
  return (
    <Button
      type="button"
      variant="ghost"
      className="code-block-copy"
      aria-label={clipboard.copied ? "已复制" : "复制代码"}
      data-copied={clipboard.copied || undefined}
      disabled={!source}
      onClick={copy}
    >
      <span className="code-block-copy-icons" aria-hidden="true">
        <CopyIcon className="tiptap-button-icon code-block-copy-glyph" />
        <CheckIcon className="tiptap-button-icon code-block-check-glyph" />
      </span>
    </Button>
  );
}
