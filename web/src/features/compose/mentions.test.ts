import { describe, expect, it } from "vitest";

import { mentionPanelPosition, mentionSegments } from "./mentions";

describe("mentionSegments", () => {
  it("highlights only the complete owner username at token boundaries", () => {
    expect(mentionSegments("hi @owner，再见 @owner_2", "owner")).toEqual([
      { text: "hi ", mention: false },
      { text: "@owner", mention: true },
      { text: "，再见 @owner_2", mention: false },
    ]);
  });

  it("matches case-insensitively and preserves line breaks", () => {
    expect(mentionSegments("第一行\n@OWNER", "owner")).toEqual([
      { text: "第一行\n", mention: false },
      { text: "@OWNER", mention: true },
    ]);
  });
});

describe("mentionPanelPosition", () => {
  it("anchors below the caret and stays inside the editor", () => {
    expect(
      mentionPanelPosition({
        anchorLeft: 680,
        anchorTop: 140,
        editorLeft: 100,
        editorTop: 100,
        editorWidth: 600,
        itemCount: 1,
        lineHeight: 28,
        scrollLeft: 0,
        scrollTop: 0,
        viewportHeight: 800,
      }),
    ).toEqual({ left: 248, top: 74, width: 352 });
  });

  it("flips above the caret when the viewport has no room below", () => {
    expect(
      mentionPanelPosition({
        anchorLeft: 120,
        anchorTop: 150,
        editorLeft: 100,
        editorTop: 20,
        editorWidth: 320,
        itemCount: 3,
        lineHeight: 28,
        scrollLeft: 0,
        scrollTop: 0,
        viewportHeight: 200,
      }),
    ).toEqual({ left: 0, top: 40, width: 320 });
  });

  it("keeps the anchor aligned while the editor scrolls", () => {
    expect(
      mentionPanelPosition({
        anchorLeft: 240,
        anchorTop: 260,
        editorLeft: 100,
        editorTop: 100,
        editorWidth: 500,
        itemCount: 1,
        lineHeight: 28,
        scrollLeft: 20,
        scrollTop: 120,
        viewportHeight: 800,
      }),
    ).toEqual({ left: 120, top: 74, width: 352 });
  });
});
