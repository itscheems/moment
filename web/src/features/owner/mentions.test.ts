import { describe, expect, it } from "vitest";

import { mentionSegments } from "./mentions";

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
