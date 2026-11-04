"use client";

import { useTranslation } from "react-i18next";

import { Button } from "@content-notion/components/primitives/button/button";
import { SunIcon } from "@content-notion/components/icons/sun-icon";
import { MoonStarIcon } from "@content-notion/components/icons/moon-star-icon";
import { useTheme } from "../../../../../../theme/ThemeProvider";
import { useThemeTransition } from "../../../../../../theme/useThemeTransition";

/** The template control uses the site's existing persisted theme preference. */
export function ThemeToggle({
  headerAction = false,
}: {
  headerAction?: boolean;
}) {
  const { t } = useTranslation();

  const { resolvedTheme, setPreference } = useTheme();
  const transitionTheme = useThemeTransition();
  const isDarkMode = resolvedTheme !== "light";
  const label = isDarkMode ? t("切换到浅色模式") : t("切换到暗色模式");

  return (
    <Button
      onClick={(event) =>
        transitionTheme(event.currentTarget, () =>
          setPreference(isDarkMode ? "light" : "lights-out"),
        )
      }
      aria-label={label}
      tooltip={label}
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
