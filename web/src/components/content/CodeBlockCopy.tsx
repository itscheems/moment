import { useState } from "react";
import { Button } from "@content-notion/components/primitives/button/button";
import { ClipboardIcon } from "@content-notion/components/icons/clipboard-icon";
import { writeToClipboard } from "@content-notion/lib/clipboard";
import { notify } from "../Feedback";

/** The native clipboard utility also accepts a static reader's node text. */
export function CodeBlockCopy({ source }: { source: string }) {
  const [copying, setCopying] = useState(false);
  const copy = async () => {
    setCopying(true);
    try {
      await writeToClipboard(source);
      notify("代码已复制");
    } catch {
      notify("暂时无法复制代码", { type: "error" });
    } finally {
      setCopying(false);
    }
  };
  return (
    <Button
      type="button"
      variant="ghost"
      aria-label="复制代码"
      tooltip="复制代码"
      disabled={copying || !source}
      aria-busy={copying}
      onClick={() => void copy()}
    >
      <ClipboardIcon className="tiptap-button-icon" />
      <span className="tiptap-button-text">复制</span>
    </Button>
  );
}
