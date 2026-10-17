import { describe, expect, it } from "vitest";

import { detectLinkUrl } from "./useLinkPreview";

describe("detectLinkUrl", () => {
  it("picks the last URL, which is the one the renderer may hide", () => {
    expect(
      detectLinkUrl("先看 https://example.com/a 再看 https://example.com/b"),
    ).toBe("https://example.com/b");
  });

  it("strips trailing sentence punctuation, including full-width marks", () => {
    // The server's linkifier stops at the first non-ASCII character, so a
    // full-width period must not end up inside the URL the composer resolves.
    expect(detectLinkUrl("推荐 https://example.com/a。")).toBe(
      "https://example.com/a",
    );
    expect(detectLinkUrl("推荐 https://example.com/a).")).toBe(
      "https://example.com/a",
    );
    expect(detectLinkUrl("看 https://example.com/a，然后")).toBe(
      "https://example.com/a",
    );
  });

  it("refuses anything that is not http(s)", () => {
    expect(detectLinkUrl("file:///etc/passwd")).toBeNull();
    expect(detectLinkUrl("看看 www.example.com")).toBeNull();
    expect(detectLinkUrl("没有链接的一段话")).toBeNull();
  });
});
