import { describe, expect, it } from "vitest";

import shellCss from "../components/AppShell.module.css?inline";
import stateCss from "../components/ShellState.module.css?inline";
import themeMenuCss from "../components/ThemeMenu.module.css?inline";
import interactionsCss from "../components/TimelineInteractions.module.css?inline";
import commentsCss from "../features/comments/Comments.module.css?inline";
import mediaCss from "../features/media/Media.module.css?inline";
import datePickerCss from "../features/memory/DatePicker.module.css?inline";
import ownerCss from "../features/owner/Owner.module.css?inline";
import postCss from "../features/posts/PostPresentation.module.css?inline";
import profileEditorCss from "../features/profile/ProfileEditor.module.css?inline";
import profileCss from "../features/profile/ProfilePage.module.css?inline";
import manifestSource from "../../public/manifest.webmanifest?raw";
import markSource from "../../public/moment-mark.svg?raw";
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
  it("keeps the installed app aligned with the current Moment identity", () => {
    const manifest = JSON.parse(manifestSource) as {
      id: string;
      start_url: string;
      scope: string;
      display: string;
      background_color: string;
      theme_color: string;
      icons: Array<{ sizes: string; purpose: string }>;
    };
    expect(manifest).toMatchObject({
      id: "/",
      start_url: "/",
      scope: "/",
      display: "standalone",
      background_color: "#ffffff",
      theme_color: "#ffffff",
    });
    expect(manifest.icons).toContainEqual(
      expect.objectContaining({ sizes: "any", purpose: "any maskable" }),
    );
    expect(markSource).toContain('stroke="#0f1419"');
    expect(markSource).not.toContain("#2e6b4d");
  });

  it("locks the wide, tablet, and mobile shell boundaries", () => {
    expect(shellCss).toContain("grid-template-columns: 15.5rem 40rem 20rem");
    expect(shellCss).toContain("inline-size: min(100%, 75.5rem)");
    expect(shellCss).toContain(
      "grid-template-columns: 5.5rem minmax(0, 40rem)",
    );
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
    expect(profileEditorCss).toContain(
      "@media (prefers-reduced-motion: reduce)",
    );
  });

  it("keeps the profile edit pill independent from circular icon actions", () => {
    expect(profileCss).toMatch(/\._profileActions_[\w-]+ > button/);
    expect(profileCss).toMatch(/\._profileEditorSlot_[\w-]+/);
    expect(profileCss).not.toMatch(/\._profileActions_[\w-]+ button,/);
  });

  it("keeps profile media menu anchors stable during hover and focus", () => {
    const avatarState = profileEditorCss.match(
      /\._avatarChangeButton_[\w-]+:hover,\s*\._avatarChangeButton_[\w-]+:focus-visible\s*\{([^}]*)\}/,
    )?.[1];
    const coverState = profileEditorCss.match(
      /\._coverActions_[\w-]+ button:hover,\s*\._coverActions_[\w-]+ button:focus-visible\s*\{([^}]*)\}/,
    )?.[1];

    expect(avatarState).toBeDefined();
    expect(coverState).toBeDefined();
    expect(avatarState).not.toContain("transform:");
    expect(coverState).not.toContain("transform:");
  });

  it("defines explicit light and dark theme surfaces and a visible focus token", () => {
    expect(globalCss).toContain(':root[data-theme="dim"]');
    expect(globalCss).toContain(':root[data-theme="lights-out"]');
    expect(globalCss).toContain(':root[data-accent="blue"]');
    expect(globalCss).toContain(":focus-visible");
    expect(globalCss).toContain("--focus-ring:");
  });

  it("gives body, display, and utility type distinct CJK-safe roles", () => {
    expect(globalCss).toContain('"SF Pro Text"');
    expect(globalCss).toContain('"SF Pro Display"');
    expect(globalCss).toContain('"PingFang SC"');
    expect(globalCss).toContain('"Noto Sans CJK SC"');
    expect(globalCss).toContain("--tracking-display: -0.022em");
    expect(globalCss).toContain("--tracking-body: -0.006em");
    expect(globalCss).toContain("--tracking-utility: 0.006em");
  });

  it("keeps lights-out elevated surfaces and secondary actions visible", () => {
    expect(globalCss).toContain("--surface-elevated: #16181c");
    expect(globalCss).toContain("--surface-hover: #181818");
    expect(globalCss).toContain("--text-secondary: #8b98a5");
    expect(contrast("#8b98a5", "#000000")).toBeGreaterThanOrEqual(4.5);
  });

  it("coordinates menu, popover, dialog, and content motion", () => {
    expect(globalCss).toContain("--motion-menu: 120ms");
    expect(globalCss).toContain("--motion-popover: 140ms");
    expect(globalCss).toContain("--motion-dialog: 160ms");
    expect(globalCss).toContain("--motion-content: 240ms");
    expect(themeMenuCss).toContain("var(--motion-menu)");
    expect(interactionsCss).toContain("var(--motion-popover)");
    expect(ownerCss).toContain("var(--motion-dialog)");
    expect(postCss).toContain("var(--motion-content)");
  });

  it("keeps high-frequency action boxes on the shared 44px target", () => {
    expect(globalCss).toContain("--target-size: 2.75rem");
    expect(themeMenuCss).toContain("inline-size: var(--target-size)");
    expect(interactionsCss).toContain("min-block-size: var(--target-size)");
    expect(mediaCss).toContain("min-block-size: var(--target-size)");
    expect(commentsCss).toContain("min-block-size: var(--target-size)");
    expect(ownerCss).toContain("min-block-size: var(--target-size)");
  });

  it("drives primary controls from the selected accent in every theme", () => {
    const accents = ["blue", "yellow", "pink", "purple", "orange", "green"];

    expect(globalCss).toContain("--accent: #1d9bf0");
    expect(globalCss).toContain(
      "--accent-pressed: color-mix(in srgb, var(--accent) 82%, #000000)",
    );
    expect(globalCss).toContain("--button-primary: var(--accent)");
    expect(globalCss).toContain("--button-primary-hover: var(--accent-hover)");
    expect(globalCss).toContain(
      "--button-primary-pressed: var(--accent-pressed)",
    );
    expect(globalCss).toContain("--button-primary-disabled: var(--accent)");
    expect(globalCss).toContain("--button-primary-disabled-opacity: 0.5");
    expect(globalCss).toContain(
      "--button-primary-contrast: var(--accent-contrast)",
    );
    for (const accent of accents) {
      const block = globalCss.match(
        new RegExp(`:root\\[data-accent="${accent}"\\]\\s*\\{([^}]*)\\}`),
      )?.[1];
      expect(block, `${accent} accent tokens`).toContain("--accent:");
      expect(block, `${accent} hover token`).toContain("--accent-hover:");
      expect(block, `${accent} contrast token`).toContain("--accent-contrast:");
      expect(block, `${accent} focus token`).toContain("--focus-ring:");
    }
    expect(globalCss).not.toMatch(
      /@media \(prefers-color-scheme: dark\)[\s\S]*--focus-ring: #1d9bf0/,
    );
    expect(contrast("#ffffff", "#1d9bf0")).toBeGreaterThanOrEqual(3);
    expect(contrast("#0f1419", "#ffd400")).toBeGreaterThanOrEqual(4.5);
  });

  it("uses contrast-safe state tokens on publish and selected-date actions", () => {
    expect(ownerCss).toMatch(
      /\._composerPublishButton_[\w-]+\s*\{[^}]*color: var\(--button-primary-contrast\);[^}]*background: var\(--button-primary\);[^}]*opacity var\(--motion-fast\)/s,
    );
    expect(ownerCss).toMatch(
      /\._composerPublishButton_[\w-]+:active:not\(:disabled\)\s*\{[^}]*background: var\(--button-primary-pressed\)/s,
    );
    expect(ownerCss).toMatch(
      /\._primaryButton_[\w-]+\._composerPublishButton_[\w-]+:disabled\s*\{[^}]*color: var\(--button-primary-contrast\);[^}]*background: var\(--button-primary-disabled\);[^}]*opacity: var\(--button-primary-disabled-opacity\)/s,
    );
    expect(datePickerCss).toMatch(
      /\._days_[\w-]+ button\[aria-pressed="true"\]\s*\{[^}]*color: var\(--button-primary-contrast\);[^}]*background: var\(--button-primary\)/s,
    );
    expect(datePickerCss).toMatch(
      /\._days_[\w-]+ button\[aria-pressed="true"\]:active\s*\{[^}]*background: var\(--button-primary-pressed\)/s,
    );
    expect(ownerCss).toMatch(
      /@media \(prefers-reduced-motion: reduce\)[\s\S]*\._composerPublishButton_[\w-]+\s*\{\s*transition: none/,
    );
  });

  it("keeps confirmations above composer and lightbox layers", () => {
    expect(globalCss).toContain("--layer-lightbox: 110");
    expect(globalCss).toContain("--layer-confirm: 130");
    expect(ownerCss).toContain("z-index: var(--layer-confirm)");
    expect(ownerCss).toContain("z-index: calc(var(--layer-confirm) + 1)");
  });

  it("keeps small tertiary copy readable on every light surface", () => {
    const tertiary = "#536471";
    expect(globalCss).toContain(`--text-tertiary: ${tertiary}`);
    expect(contrast(tertiary, "#ffffff")).toBeGreaterThanOrEqual(4.5);
    expect(contrast(tertiary, "#f7f9f9")).toBeGreaterThanOrEqual(4.5);
  });
});
