import { describe, expect, it } from "vitest";

import {
  shareCardBlocks,
  shareCardMediaUrl,
  type CodeSpan,
  type ShareCardBlock,
} from "./shareCard";

/** The runs of a fenced block, flattened out of their lines. */
function codeSpans(block: ShareCardBlock | undefined): CodeSpan[] {
  return block?.kind === "code" ? block.lines.flat() : [];
}

/** What a fenced block reads as, one string per line. */
function codeText(block: ShareCardBlock | undefined): string[] {
  return block?.kind === "code"
    ? block.lines.map((line) => line.map((span) => span.text).join(""))
    : [];
}

// The card draws type, not markup. Flattening the body to text loses exactly
// what somebody used markdown to say: a list stops being a list, and a fenced
// block loses the line breaks that were its whole point.
describe("reading a post body into blocks", () => {
  it("keeps a bullet list a list", () => {
    expect(
      shareCardBlocks("<p>标题</p><ul><li>111</li><li>222</li></ul>"),
    ).toEqual([
      { kind: "text", text: "标题" },
      { kind: "item", marker: "•", text: "111" },
      { kind: "item", marker: "•", text: "222" },
    ]);
  });

  it("numbers an ordered list from its own position", () => {
    const blocks = shareCardBlocks(
      "<ol><li>一</li><li>二</li><li>三</li></ol>",
    );
    expect(
      blocks.map((block) => (block.kind === "item" ? block.marker : null)),
    ).toEqual(["1.", "2.", "3."]);
  });

  // The one block whose shape is part of its meaning. Collapsing whitespace
  // the way every other block does turns a function into one long line.
  it("keeps the line breaks inside a fenced block", () => {
    const blocks = shareCardBlocks(
      '<pre><code>def hi():\n    print("hello")\n</code></pre>',
    );
    expect(blocks).toHaveLength(1);
    expect(codeText(blocks[0])).toEqual(["def hi():", '    print("hello")']);
  });

  // The card draws the same post the page does, so it colours code the same
  // way. It cannot read the stylesheet -- a canvas has no cascade -- so the
  // colour has to be decided here, while the fence is still markup.
  it("colours a fence that asked for a language", () => {
    const blocks = shareCardBlocks(
      '<pre><code class="language-python">def hi():\n    return 1</code></pre>',
    );
    const colours = new Set(codeSpans(blocks[0]).map((span) => span.colour));
    expect(colours.size).toBeGreaterThan(1);
    expect(codeSpans(blocks[0])).toContainEqual({
      text: "def",
      colour: "#1b8bd6",
    });
  });

  it("leaves a fence that named no language in one colour", () => {
    const blocks = shareCardBlocks("<pre><code>def hi():</code></pre>");
    expect(new Set(codeSpans(blocks[0]).map((span) => span.colour)).size).toBe(
      1,
    );
  });

  // Colouring reshapes the markup around the text; none of the text itself is
  // allowed to move, or the card shows code that was never written.
  it("changes no character of a coloured fence", () => {
    const source = 'x = {"a": 1}  # 注释\n\nif x:\n    return None';
    const blocks = shareCardBlocks(
      `<pre><code class="language-python">${source.replace(/"/g, "&quot;")}</code></pre>`,
    );
    expect(codeText(blocks[0]).join("\n")).toBe(source);
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
    expect(
      shareCardBlocks("<div><section><p>深处的一段</p></section></div>"),
    ).toEqual([{ kind: "text", text: "深处的一段" }]);
  });

  it("says nothing about an empty body rather than drawing a blank line", () => {
    expect(shareCardBlocks("<p></p><p>   </p>")).toEqual([]);
  });
});

// A video's `/preview` is the video. Drawing frames from a `<video>` element is
// liable to HDR overexposure. The poster route serves a colour-managed SDR JPEG.
describe("choosing the still for a piece of media", () => {
  it("asks a video for its poster", () => {
    const url = shareCardMediaUrl({
      mediaId: "01ABC",
      mimeType: "video/quicktime",
    });
    expect(url).toContain("/poster");
    expect(url).not.toContain("/preview");
  });

  it("asks a still for its preview", () => {
    expect(
      shareCardMediaUrl({ mediaId: "01ABC", mimeType: "image/jpeg" }),
    ).toContain("/preview");
  });

  // The recipe version is part of the browser's cache key. Without it a native
  // media cache can answer with an older rendition at the same id -- the third
  // of the three colour faults in that same record.
  it("carries the recipe version either way", () => {
    for (const mimeType of ["video/mp4", "image/jpeg"]) {
      expect(shareCardMediaUrl({ mediaId: "01ABC", mimeType })).toMatch(
        /[?&]v=/,
      );
    }
  });
});
