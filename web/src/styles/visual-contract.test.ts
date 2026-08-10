import { describe, expect, it } from "vitest";

import shellCss from "../components/AppShell.module.css?inline";
import stateCss from "../components/ShellState.module.css?inline";
import themeMenuCss from "../components/ThemeMenu.module.css?inline";
import globalCss from "./global.css?inline";

function channel(value: number): number {
  const component = value / 255;
  return component <= 0.04045
    ? component / 12.92
    : ((component + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  const channels = hex.match(/[a-f\d]{2}/gi)?.map((value) => {
    return channel(Number.parseInt(value, 16));
  });
  if (!channels || channels.length !== 3) throw new Error("invalid color");
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrast(foreground: string, background: string): number {
  const lighter = Math.max(luminance(foreground), luminance(background));
  const darker = Math.min(luminance(foreground), luminance(background));
  return (lighter + 0.05) / (darker + 0.05);
}

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

  it("keeps small tertiary copy readable on every light surface", () => {
    const tertiary = "#657168";
    expect(globalCss).toContain(`--text-tertiary: ${tertiary}`);
    expect(contrast(tertiary, "#f3f6f3")).toBeGreaterThanOrEqual(4.5);
    expect(contrast(tertiary, "#fbfcfb")).toBeGreaterThanOrEqual(4.5);
    expect(contrast(tertiary, "#ffffff")).toBeGreaterThanOrEqual(4.5);
  });
});
