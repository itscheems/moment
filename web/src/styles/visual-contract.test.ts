import { describe, expect, it } from "vitest";

import shellCss from "../components/AppShell.module.css?inline";
import identityCss from "../components/SiteIdentity.module.css?inline";
import stateCss from "../components/ShellState.module.css?inline";
import themeMenuCss from "../components/ThemeMenu.module.css?inline";
import interactionsCss from "../components/TimelineInteractions.module.css?inline";
import interCss from "inter-ui/inter-variable-latin.css?inline";
import commentsCss from "../features/comments/Comments.module.css?inline";
import mediaCss from "../features/media/Media.module.css?inline";
import datePickerCss from "../features/memory/DatePicker.module.css?inline";
import ownerCss from "../features/owner/Owner.module.css?inline";
import settingsCss from "../features/owner/Settings.module.css?inline";
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
    expect(shellCss).toMatch(
      /\._shell_[\w-]+\._detailShell_[\w-]+\s*\{[^}]*grid-template-columns:\s*15\.5rem 37\.5rem 22\.5rem;/s,
    );
    expect(shellCss).toContain(
      "grid-template-columns: 5.5rem minmax(0, 40rem)",
    );
    expect(shellCss).toMatch(
      /\._shell_[\w-]+\._detailShell_[\w-]+\s*\{[^}]*grid-template-columns:\s*5\.5rem minmax\(0, 37\.5rem\) 2\.5rem;/s,
    );
    expect(shellCss).toContain(
      "@media (min-width: 720px) and (max-width: 1279px)",
    );
    expect(shellCss).toContain("@media (max-width: 719px)");
    expect(shellCss).toContain("env(safe-area-inset-bottom)");
  });

  it("uses the compact centered timeline tab on the home header", () => {
    expect(shellCss).toMatch(
      /\._timelineHeader_[\w-]+\s*\{[^}]*block-size: 3\.3125rem;/s,
    );
    expect(shellCss).toMatch(
      /\._homeHeaderTab_[\w-]+\s*\{[^}]*align-items: center;[^}]*justify-content: center;/s,
    );
    expect(shellCss).toMatch(
      /\._homeHeaderTab_[\w-]+ h1\s*\{[^}]*font-size: 0\.9375rem;[^}]*font-weight: 700;/s,
    );
    expect(shellCss).toMatch(
      /\._timelineHeader_[\w-]+:not\(\._homeHeader_[\w-]+\) h1/,
    );
    expect(shellCss).toMatch(
      /\._homeHeaderTab_[\w-]+ > span\s*\{[^}]*block-size: 0\.25rem;[^}]*background: var\(--accent\);/s,
    );
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

  it("self-hosts an open variable Latin face with CJK-safe system fallback", () => {
    expect(interCss).toContain("font-family: InterVariable");
    expect(interCss).toContain("font-weight: 100 900");
    expect(interCss).toContain("font-display: swap");
    expect(globalCss.match(/InterVariable/g)).toHaveLength(3);
    expect(globalCss).toContain('"PingFang SC"');
    expect(globalCss).toContain('"PingFang TC"');
    expect(globalCss).toContain('"PingFang HK"');
    expect(globalCss).toContain('"Noto Sans CJK SC"');
    expect(globalCss).toMatch(
      /--font-timeline:\s*"TwitterChirp", -apple-system, "system-ui", "Segoe UI", Roboto, Helvetica,/,
    );
    expect(globalCss.match(/font-family: "TwitterChirp"/g)).toHaveLength(4);
    expect(globalCss).toContain(
      'url("/fonts/twitter-chirp-regular.woff") format("woff")',
    );
    expect(globalCss).toContain(
      'url("/fonts/twitter-chirp-medium.woff") format("woff")',
    );
    expect(globalCss).toContain(
      'url("/fonts/twitter-chirp-bold.woff") format("woff")',
    );
    expect(globalCss).toContain(
      'url("/fonts/twitter-chirp-heavy.woff") format("woff")',
    );
    // Four Chirp weights plus the extended-heavy cut the sign-in display line
    // uses. It is its own family rather than a fifth weight, so that asking for
    // 800 anywhere else still gets the normal-width face.
    expect(globalCss.match(/font-display: swap/g)).toHaveLength(5);
    expect(globalCss).toContain('font-family: "TwitterChirpExtendedHeavy"');
    expect(globalCss).toContain("font-optical-sizing: auto");
    expect(globalCss).toContain('"calt" 1');
    expect(globalCss).toContain("--tracking-display: -0.022em");
    expect(globalCss).toContain("--tracking-body: -0.006em");
    expect(globalCss).toContain("--tracking-utility: 0.006em");
  });

  it("keeps the measured detail hierarchy separate from timeline density", () => {
    expect(postCss).toMatch(
      /\._timelineDensity_[\w-]+ \._markdown_[\w-]+\s*\{[^}]*font-size:\s*0\.9375rem;[^}]*line-height:\s*1\.25rem;/s,
    );
    expect(postCss).toMatch(
      /\._detailBody_[\w-]+ \._markdown_[\w-]+\s*\{[^}]*font-size:\s*1\.0625rem;[^}]*line-height:\s*1\.5rem;/s,
    );
    expect(shellCss).toMatch(
      /\._detailHeader_[\w-]+,[\s\S]*?\._detailHeader_[\w-]+ h1\s*\{[^}]*font-family:\s*var\(--font-timeline\);[^}]*font-feature-settings:\s*normal;[^}]*letter-spacing:\s*normal;[^}]*text-rendering:\s*auto;/,
    );
    expect(shellCss).toMatch(
      /\._detailHeader_[\w-]+ h1\s*\{[^}]*font-size:\s*1\.25rem;[^}]*line-height:\s*1\.5rem;/s,
    );
    expect(shellCss).toMatch(
      /@media \(max-width: 719px\)[\s\S]*?\._detailHeader_[\w-]+ h1\s*\{[^}]*font-size:\s*1\.0625rem;[^}]*line-height:\s*1\.25rem;/,
    );
    expect(commentsCss).toMatch(
      /\._commentForm_[\w-]+ textarea\s*\{[^}]*font-family:\s*var\(--font-timeline\);[^}]*font-feature-settings:\s*normal;[^}]*font-size:\s*1\.25rem;[^}]*letter-spacing:\s*normal;[^}]*line-height:\s*1\.5rem;[^}]*text-rendering:\s*auto;/s,
    );
    expect(commentsCss).toMatch(
      /\._commentMarkdown_[\w-]+\s*\{[^}]*font-family:\s*var\(--font-timeline\);[^}]*font-size:\s*0\.9375rem;[^}]*line-height:\s*1\.25rem;[^}]*overflow-wrap:\s*break-word;/s,
    );
  });

  it("keeps detail actions and inline replies separate from list and dialog contracts", () => {
    expect(postCss).toMatch(
      /\._postActions_[\w-]+\s*\{[^}]*grid-template-columns:\s*var\(--post-edge-action-size\)\s+var\(--post-edge-action-size\)\s+minmax\(0, 1fr\)\s+var\(--post-edge-action-size\)/,
    );
    expect(postCss).toMatch(
      /\._postAction_[\w-]+ svg\s*\{[^}]*inline-size:\s*1\.171875rem;[^}]*block-size:\s*1\.171875rem;/s,
    );
    expect(postCss).toMatch(
      /\._detail_[\w-]+ \._postActions_[\w-]+\s*\{[^}]*min-block-size:\s*3rem;[^}]*grid-template-columns:\s*1\.90625rem 1\.90625rem 1\.90625rem 1\.90625rem 1\.40625rem;[^}]*padding-inline:\s*var\(--space-1\);/s,
    );
    expect(commentsCss).toMatch(
      /\._formSurface_[\w-]+\._inlineTopLevelForm_[\w-]+\s*\{[^}]*border-block-end:\s*0;[^}]*padding:\s*0\.25rem var\(--space-4\) 0\.75rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentCollapsed_[\w-]+ \._commentForm_[\w-]+\s*\{[^}]*block-size:\s*4\.25rem;[^}]*padding-block:\s*0\.25rem 0\.5rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._collapsedReplyAction_[\w-]+\s*\{[^}]*block-size:\s*2\.25rem;[^}]*color:\s*var\(--surface\);[^}]*background:\s*var\(--text-primary\);[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:disabled\s*\{[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+\s*\{[^}]*color:\s*var\(--button-primary-contrast\);[^}]*background:\s*var\(--button-primary\);[^}]*transition:\s*box-shadow 0\.2s;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:hover:not\(:disabled\)\s*\{[^}]*background:\s*var\(--button-primary-hover\);/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:disabled\s*\{[^}]*color:\s*var\(--surface\);[^}]*background:\s*var\(--text-primary\);[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:active:not\(:disabled\)\s*\{[^}]*background:\s*var\(--button-primary-pressed\);[^}]*transform:\s*none;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentExpanded_[\w-]+\s*\{[^}]*padding-block:\s*0\.25rem 0\.75rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentExpanded_[\w-]+ \._commentForm_[\w-]+ textarea\s*\{[^}]*min-block-size:\s*3\.5rem;[^}]*border:\s*0\.125rem solid transparent;[^}]*padding-block:\s*0\.75rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+[\s\S]*?\._commentMediaComposer_[\w-]+[\s\S]*?button:hover:not\(:disabled\),[\s\S]*?button:focus-visible\s*\{[^}]*color:\s*#829aab;[^}]*background:\s*rgb\(15 20 25 \/ 10%\);[^}]*transform:\s*scale\(1\.12\);/s,
    );
    expect(commentsCss).toMatch(
      /\._replyDialogPopup_[\w-]+ > \._formSurface_[\w-]+\s*\{[^}]*padding-block-start:\s*var\(--space-2\);/s,
    );
    const mobileRules = commentsCss.split("@media (max-width: 719px)")[1];
    expect(mobileRules).toBeDefined();
    expect(mobileRules).not.toMatch(/\._inlineTopLevelForm_[\w-]+/);
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

  it("lets the profile preview cover reach the rounded card edge", () => {
    expect(interactionsCss).not.toMatch(
      /\._identityCard_[^{]+\{[^}]*\bborder:\s/,
    );
  });

  it("keeps high-frequency action boxes at least as large as the shared target", () => {
    expect(globalCss).toContain("--target-size: 2.75rem");
    expect(themeMenuCss).toContain("inline-size: var(--target-size)");
    expect(interactionsCss).toContain("min-block-size: 3rem");
    expect(mediaCss).toContain("min-block-size: var(--target-size)");
    expect(commentsCss).toContain("min-block-size: var(--target-size)");
    expect(ownerCss).toContain("min-block-size: var(--target-size)");
  });

  it("crops only extra-tall single videos on mobile without letterboxing", () => {
    expect(mediaCss).toMatch(
      /@media \(max-width: 719px\)[\s\S]*?\._imageGrid_[\w-]+\[data-media-count="1"\]\[data-compact-tall-video\][\s\S]*?\._videoTile_[\w-]+\[data-media-shape="tall"\]\s*\{[^}]*aspect-ratio: 2 \/ 3;[^}]*max-block-size: none;/,
    );
    expect(mediaCss).toMatch(
      /\._imageGrid_[\w-]+\[data-media-count="1"\]\[data-media-shape="tall"\]\s*\{[^}]*inline-size: min\(100%, var\(--single-media-max-width\)\);/,
    );
    expect(mediaCss).toMatch(
      /\[data-compact-tall-video\][\s\S]*?\._videoTile_[\w-]+\[data-media-shape="tall"\][\s\S]*?video,[\s\S]*?\._videoPoster_[\w-]+\s*\{[^}]*object-fit: cover;/,
    );
  });

  it("keeps the inline composer avatar on the timeline avatar rail", () => {
    expect(identityCss).toMatch(
      /\._avatar_[\w-]+,\s*\._avatarFallback_[\w-]+\s*\{[^}]*inline-size: 2\.5rem;[^}]*block-size: 2\.5rem;/s,
    );
    expect(ownerCss).toMatch(
      /\._inlineComposer_[\w-]+\s*\{[^}]*grid-template-columns: 3rem minmax\(0, 1fr\)/s,
    );
    expect(ownerCss).toMatch(
      /\._inlineComposer_[\w-]+\s+\._newTick_[\w-]+\s*\{[^}]*align-self: stretch;[^}]*place-items: start center;[^}]*padding-block-start: var\(--space-3\)/s,
    );
    expect(ownerCss).not.toMatch(
      /\._inlineComposer_[\w-]+\s+\._newTick_[\w-]+\s*>[^{]*\{/,
    );
  });

  it("keeps backup media diffs compact without distorting their ratio", () => {
    expect(settingsCss).toMatch(
      /\._reviewMedia_[\w-]+\s*\{[^}]*inline-size: auto;[^}]*block-size: auto;[^}]*max-inline-size: 100%;[^}]*max-block-size: 16rem;[^}]*object-fit: contain;/s,
    );
    expect(settingsCss).not.toMatch(
      /\._reviewMedia_[\w-]+\s*\{[^}]*aspect-ratio:/s,
    );
  });

  it("uses settings-style tabs for import mode instead of a segmented pill", () => {
    expect(settingsCss).toMatch(
      /\._importModeSwitch_[\w-]+\s*\{[^}]*inline-size: 100%;[^}]*border-block-end: 1px solid var\(--border-subtle\)/s,
    );
    expect(settingsCss).toMatch(
      /\._importModeSwitch_[\w-]+ button\[aria-pressed="true"\]\s*\{[^}]*box-shadow: inset 0 -0\.2rem var\(--accent\)/s,
    );
    expect(settingsCss).not.toMatch(
      /\._importModeSwitch_[\w-]+\s*\{[^}]*border-radius: 999px/s,
    );
  });

  it("drives primary controls from the selected accent in every theme", () => {
    const accents = [
      "blue",
      "yellow",
      "pink",
      "purple",
      "orange",
      "green",
      "black",
    ];

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
    expect(contrast("#ffffff", "#000000")).toBeGreaterThanOrEqual(4.5);
    expect(contrast("#0f1419", "#f7f9f9")).toBeGreaterThanOrEqual(4.5);
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

  // The account pill at the foot of the rail is asked for by its hover: a
  // target that big with no response to the pointer reads as a label rather
  // than a control. Losing either rule looks wrong and tests green.
  it("gives the account rail a hover that fades rather than snaps", () => {
    expect(shellCss).toMatch(
      /\._accountRail_[\w-]+\s*\{[^}]*transition:[^;]*background var\(--motion-fast\)/s,
    );
    expect(shellCss).toMatch(
      /\._accountRail_[\w-]+:hover[^{]*\{[^}]*background: var\(--surface-hover-strong\)/s,
    );
    // A pill this size needs more than the nav rows' wash to read as pressed
    // rather than merely tinted, and the value has to exist in every theme --
    // a literal here would be a light grey on a dark canvas.
    expect(globalCss.match(/--surface-hover-strong:/g)).toHaveLength(4);
  });

  // Dragging across a headline this size fills half the panel with selection
  // highlight and copies nothing anybody wanted. Losing this rule looks wrong
  // and tests green, so it is asserted rather than remembered.
  it("refuses selection on the sign-in display line", () => {
    // The lookbehind matters: without it `-webkit-user-select: none` satisfies
    // the match on its own, and the unprefixed property -- the one that
    // actually governs -- could be lost with the assertion still green.
    expect(ownerCss).toMatch(
      /\._displayHeadline_[\w-]+\s*\{[^}]*(?<!-)user-select: none/s,
    );
  });

  // The alternative sign-in methods sit under the provider buttons and read as
  // one of them, so they share their axis. `.loginMethodSwitch` centres itself
  // with justify-self, which does nothing without a grid parent -- the kind of
  // regression that leaves the page looking wrong and every test green.
  it("centres the alternative sign-in methods on the provider buttons' axis", () => {
    expect(ownerCss).toMatch(
      /\._otherMethods_[\w-]+\s*\{[^}]*display: grid;[^}]*justify-items: center;/s,
    );
    expect(ownerCss).toMatch(
      /\._loginMethodSwitch_[\w-]+\s*\{[^}]*justify-self: center/s,
    );
  });

  it("keeps nested pickers below lightbox and confirmation layers", () => {
    expect(globalCss).toContain("--layer-picker: 100");
    expect(globalCss).toContain("--layer-lightbox: 110");
    expect(globalCss).toContain("--layer-confirm: 130");
    expect(ownerCss).toMatch(
      /\._locationBackdrop_[\w-]+\s*\{[^}]*z-index: var\(--layer-picker\)/s,
    );
    expect(ownerCss).toMatch(
      /\._locationViewport_[\w-]+\s*\{[^}]*z-index: calc\(var\(--layer-picker\) \+ 1\)/s,
    );
    expect(ownerCss).toContain("z-index: var(--layer-confirm)");
    expect(ownerCss).toContain("z-index: calc(var(--layer-confirm) + 1)");
  });

  it("aligns profile media menus with the mention suggestion surface", () => {
    expect(profileEditorCss).toMatch(
      /\._mediaMenu_[\w-]+\s*\{[^}]*border: 1px solid var\(--border-subtle\);[^}]*border-radius: var\(--radius-panel\);[^}]*background: var\(--surface-elevated\);[^}]*box-shadow: var\(--shadow-popover\)/s,
    );
    expect(profileEditorCss).toMatch(
      /\._mediaMenuItem_[\w-]+\[data-highlighted\]\s*\{[^}]*background: var\(--surface-hover\)/s,
    );
  });

  it("keeps small tertiary copy readable on every light surface", () => {
    const tertiary = "#536471";
    expect(globalCss).toContain(`--text-tertiary: ${tertiary}`);
    expect(contrast(tertiary, "#ffffff")).toBeGreaterThanOrEqual(4.5);
    expect(contrast(tertiary, "#f7f9f9")).toBeGreaterThanOrEqual(4.5);
  });
});
