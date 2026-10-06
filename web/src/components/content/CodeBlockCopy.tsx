import { ClipboardCopyButton } from "./ClipboardCopyButton";

export function CodeBlockCopy({ source }: { source: string }) {
  return (
    <ClipboardCopyButton
      source={source}
      className="code-block-copy"
      label="复制代码"
      errorLabel="暂时无法复制代码"
    />
  );
}
