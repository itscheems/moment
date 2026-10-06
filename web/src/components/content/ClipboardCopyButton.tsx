import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import {
  Button,
  type ButtonProps,
} from "@content-notion/components/primitives/button/button";
import { CopyIcon } from "@content-notion/components/icons/copy-icon";
import { CheckIcon } from "@content-notion/components/icons/check-icon";
import { writeToClipboard } from "@content-notion/lib/clipboard";
import { CODE_COPY_TIMEOUT, CODE_COPY_TOASTER } from "./CodeCopyFeedback";

/** Shared clipboard feedback; positioning remains the caller's responsibility. */
export function ClipboardCopyButton({
  source,
  label = "Copy",
  errorLabel = "暂时无法复制所选文字",
  onCopied,
  className,
  ...props
}: Omit<ButtonProps, "onClick"> & {
  source: string | (() => string);
  label?: string;
  errorLabel?: string;
  onCopied?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
    };
  }, []);

  const copy = async () => {
    try {
      await writeToClipboard(typeof source === "function" ? source() : source);
    } catch {
      toast.error(errorLabel, {
        toasterId: CODE_COPY_TOASTER,
        duration: 6_000,
      });
      return;
    }
    toast("已复制到剪贴板", {
      toasterId: CODE_COPY_TOASTER,
      duration: CODE_COPY_TIMEOUT,
    });
    if (mounted.current) {
      clearTimeout(timer.current);
      setCopied(true);
      timer.current = setTimeout(() => setCopied(false), CODE_COPY_TIMEOUT);
      onCopied?.();
    }
  };
  return (
    <Button
      type="button"
      variant="ghost"
      className={`clipboard-copy-feedback ${className ?? ""}`}
      aria-label={copied ? "已复制" : label}
      data-copied={copied || undefined}
      disabled={typeof source === "string" && !source}
      onClick={() => void copy()}
      {...props}
    >
      <span className="code-block-copy-icons" aria-hidden="true">
        <CopyIcon className="tiptap-button-icon code-block-copy-glyph" />
        <CheckIcon className="tiptap-button-icon code-block-check-glyph" />
      </span>
    </Button>
  );
}
