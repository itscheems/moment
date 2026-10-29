"use client";

import { useTranslation } from "react-i18next";

import { Button } from "@content-notion/components/primitives/button/button";
import { SunIcon } from "@content-notion/components/icons/sun-icon";
import { MoonStarIcon } from "@content-notion/components/icons/moon-star-icon";
import { useTheme } from "../../../../../../theme/ThemeProvider";

/** The template control uses the site's existing persisted theme preference. */
export function ThemeToggle({
  headerAction = false,
}: {
  headerAction?: boolean;
}) {
  const { t } = useTranslation();

  const { resolvedTheme, setPreference } = useTheme();
  const isDarkMode = resolvedTheme !== "light";

  return (
    <Button
      onClick={() => setPreference(isDarkMode ? "light" : "lights-out")}
      aria-label={t("切换到{{value1}}模式", {
        value1: isDarkMode ? "浅色" : t("暗色"),
      })}
      tooltip={t("切换到{{value1}}模式", {
        value1: isDarkMode ? "浅色" : t("暗色"),
      })}
      data-style="ghost"
      type="button"
      data-article-header-action={headerAction || undefined}
      data-article-header-theme={headerAction || undefined}
    >
      {isDarkMode ? (
        <MoonStarIcon className="tiptap-button-icon" />
      ) : (
        <SunIcon className="tiptap-button-icon" />
      )}
    </Button>
  );
}
