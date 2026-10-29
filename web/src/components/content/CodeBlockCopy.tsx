import { useTranslation } from "react-i18next";
import { ClipboardCopyButton } from "./ClipboardCopyButton";

export function CodeBlockCopy({ source }: { source: string }) {
  const { t } = useTranslation();

  return (
    <ClipboardCopyButton
      source={source}
      className="code-block-copy"
      label={t("复制代码")}
      errorLabel={t("暂时无法复制代码")}
    />
  );
}
