"use client";

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
  const { resolvedTheme, setPreference } = useTheme();
  const isDarkMode = resolvedTheme !== "light";

  return (
    <Button
      onClick={() => setPreference(isDarkMode ? "light" : "lights-out")}
      aria-label={`切换到${isDarkMode ? "浅色" : "暗色"}模式`}
      tooltip={`切换到${isDarkMode ? "浅色" : "暗色"}模式`}
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
