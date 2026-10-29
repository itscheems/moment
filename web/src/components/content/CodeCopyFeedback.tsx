import { useTranslation } from "react-i18next";
import { Toaster } from "sonner";
import { useMediaQuery } from "@mantine/hooks";

export const CODE_COPY_TIMEOUT = 1_200;
export const CODE_COPY_TOASTER = "code-copy";

export function CodeCopyFeedback() {
  const { t } = useTranslation();

  const aboveBottomNavigation = useMediaQuery("(max-width: 719px)");
  return (
    <Toaster
      id={CODE_COPY_TOASTER}
      position="bottom-right"
      containerAriaLabel={t("复制反馈")}
      offset={
        aboveBottomNavigation
          ? { bottom: "calc(4.5rem + env(safe-area-inset-bottom))" }
          : undefined
      }
      mobileOffset={{
        bottom: "calc(4.5rem + env(safe-area-inset-bottom))",
        left: 16,
        right: 16,
      }}
    />
  );
}
