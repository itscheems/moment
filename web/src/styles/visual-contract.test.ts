import { describe, expect, it } from "vitest";

import shellCss from "../components/AppShell.module.css?inline";
import stateCss from "../components/ShellState.module.css?inline";
import themeMenuCss from "../components/ThemeMenu.module.css?inline";
import globalCss from "./global.css?inline";

describe("visual foundation contract", () => {
  it("locks the wide, tablet, and mobile shell boundaries", () => {
    expect(shellCss).toContain("minmax(38.75rem, 41.25rem)");
    expect(shellCss).toContain(
      "@media (min-width: 720px) and (max-width: 1279px)",
    );
    expect(shellCss).toContain("@media (max-width: 719px)");
    expect(shellCss).toContain("env(safe-area-inset-bottom)");
  });

  it("removes nonessential motion when the operating system requests it", () => {
    expect(globalCss).toContain("@media (prefers-reduced-motion: reduce)");
    expect(stateCss).toContain("@media (prefers-reduced-motion: reduce)");
    expect(themeMenuCss).toContain("@media (prefers-reduced-motion: reduce)");
  });

  it("defines explicit light and dark theme surfaces and a visible focus token", () => {
    expect(globalCss).toContain(':root[data-theme="dark"]');
    expect(globalCss).toContain(":focus-visible");
    expect(globalCss).toContain("--focus-ring:");
  });
});
