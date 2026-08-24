import { describe, expect, it } from "vitest";

import { shareCardBlocks, shareCardMediaUrl } from "./shareCard";

// The card draws type, not markup. Flattening the body to text loses exactly
// what somebody used markdown to say: a list stops being a list, and a fenced
// block loses the line breaks that were its whole point.
describe("reading a post body into blocks", () => {
  it("keeps a bullet list a list", () => {
    expect(shareCardBlocks("<p>标题</p><ul><li>111</li><li>222</li></ul>")).toEqual([
      { kind: "text", text: "标题" },
      { kind: "item", marker: "•", text: "111" },
      { kind: "item", marker: "•", text: "222" },
    ]);
  });

  it("numbers an ordered list from its own position", () => {
    const blocks = shareCardBlocks("<ol><li>一</li><li>二</li><li>三</li></ol>");
    expect(blocks.map((block) => (block.kind === "item" ? block.marker : null))).toEqual([
      "1.",
      "2.",
      "3.",
    ]);
  });

  // The one block whose shape is part of its meaning. Collapsing whitespace
  // the way every other block does turns a function into one long line.
  it("keeps the line breaks inside a fenced block", () => {
    expect(shareCardBlocks('<pre><code>def hi():\n    print("hello")\n</code></pre>')).toEqual([
      { kind: "code", lines: ["def hi():", '    print("hello")'] },
    ]);
  });

  it("carries a heading's level, and stops counting past the third", () => {
    const blocks = shareCardBlocks("<h1>甲</h1><h2>乙</h2><h5>丙</h5>");
    expect(blocks).toEqual([
      { kind: "text", text: "甲", heading: 1 },
      { kind: "text", text: "乙", heading: 2 },
      { kind: "text", text: "丙", heading: 3 },
    ]);
  });

  it("marks a quote so it can be drawn as one", () => {
    expect(shareCardBlocks("<blockquote><p>引用</p></blockquote>")).toEqual([
      { kind: "quote", text: "引用" },
    ]);
  });

  it("reaches the text inside a wrapper it has no opinion about", () => {
    expect(shareCardBlocks("<div><section><p>深处的一段</p></section></div>")).toEqual([
      { kind: "text", text: "深处的一段" },
    ]);
  });

  it("says nothing about an empty body rather than drawing a blank line", () => {
    expect(shareCardBlocks("<p></p><p>   </p>")).toEqual([]);
  });
});

// A video's `/preview` is the video. Drawing frames from a `<video>` element is
// liable to HDR overexposure. The poster route serves a colour-managed SDR JPEG.
describe("choosing the still for a piece of media", () => {
  it("asks a video for its poster", () => {
    const url = shareCardMediaUrl({ mediaId: "01ABC", mimeType: "video/quicktime" });
    expect(url).toContain("/poster");
    expect(url).not.toContain("/preview");
  });

  it("asks a still for its preview", () => {
    expect(shareCardMediaUrl({ mediaId: "01ABC", mimeType: "image/jpeg" })).toContain("/preview");
  });

  // The recipe version is part of the browser's cache key. Without it a native
  // media cache can answer with an older rendition at the same id -- the third
  // of the three colour faults in that same record.
  it("carries the recipe version either way", () => {
    for (const mimeType of ["video/mp4", "image/jpeg"]) {
      expect(shareCardMediaUrl({ mediaId: "01ABC", mimeType })).toMatch(/[?&]v=/);
    }
  });
});
