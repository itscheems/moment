import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderMapSnapshot } from "../maps/mapSnapshot";

vi.mock("../maps/mapSnapshot", () => ({
  renderMapSnapshot: vi.fn(async () => document.createElement("canvas")),
}));

import {
  recordCanvas,
  resolveImages,
  type Recording,
} from "../../test/recordingCanvas";

import {
  renderShareCard,
  ShareCardTooLongError,
  shareCardBlocks,
  shareCardLink,
  shareCardMediaUrl,
  type CodeSpan,
  type ShareCardBlock,
  type ShareCardInput,
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

  it("keeps table rows, header cells, blank cells and column alignment", () => {
    expect(
      shareCardBlocks(
        `<p>Before</p><table><thead><tr><th>Left</th><th align="center">Center</th><th style="text-align:right">Right</th></tr></thead><tbody><tr><td></td><td>After the fix</td><td>Color-managed reference</td></tr></tbody></table><p>After</p>`,
      ),
    ).toEqual([
      { kind: "text", text: "Before" },
      {
        kind: "table",
        rows: [
          [
            { text: "Left", header: true, align: "left" },
            { text: "Center", header: true, align: "center" },
            { text: "Right", header: true, align: "right" },
          ],
          [
            { text: "", header: false, align: "left" },
            { text: "After the fix", header: false, align: "left" },
            { text: "Color-managed reference", header: false, align: "left" },
          ],
        ],
      },
      { kind: "text", text: "After" },
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

// A canvas draws; it does not describe. These read the calls the renderer made
// rather than the pixels they would have produced -- enough to say a badge was
// put on the video and not on the photo beside it, which is what the card is
// being asked to get right.
describe("drawing the card", () => {
  let recording: Recording;
  let restoreCanvas: () => void;
  let restoreImages: () => void;

  beforeEach(() => {
    vi.mocked(renderMapSnapshot).mockClear();
    vi.stubGlobal("Path2D", class {});
    const canvas = recordCanvas();
    recording = canvas.recording;
    restoreCanvas = canvas.restore;
    restoreImages = resolveImages();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    restoreCanvas();
    restoreImages();
  });

  const input = (over: Partial<ShareCardInput> = {}): ShareCardInput => ({
    displayName: "Cheems",
    username: "itscheems",
    avatarUrl: null,
    publishedLabel: "2026年8月24日 19:02",
    blocks: [{ kind: "text", text: "在场" }],
    media: [],
    location: null,
    link: null,
    ...over,
  });

  /** The badge's disc is the only thing filled in that particular dark. */
  const badges = () =>
    recording.of("fill").filter((call) => call.fill === "rgb(15 20 25 / 78%)")
      .length;

  const mapCard = {
    connectPoints: false,
    places: [
      {
        name: "东京",
        displayName: "东京，日本",
        latitude: 35.68,
        longitude: 139.76,
        coordinateSystem: "wgs84" as const,
      },
    ],
  };

  it("draws a rounded map image instead of a repeated location label", async () => {
    const controller = new AbortController();
    await renderShareCard(
      input({ mapCard, location: "东京" }),
      controller.signal,
    );
    expect(renderMapSnapshot).toHaveBeenCalledWith(
      mapCard,
      516,
      340,
      controller.signal,
    );
    expect(
      recording
        .of("drawImage")
        .some((call) => call.args[0] instanceof HTMLCanvasElement),
    ).toBe(true);
    expect(recording.text()).not.toContain("东京");
    expect(recording.text()).toContain("Read More on Moment");
  });

  it("preserves an ordinary location when there is no map", async () => {
    await renderShareCard(input({ location: "东京" }));
    expect(renderMapSnapshot).not.toHaveBeenCalled();
    expect(recording.text()).toContain("东京");
  });

  it("keeps ordinary photos alongside the map", async () => {
    await renderShareCard(
      input({ mapCard, media: [{ mediaId: "01A", mimeType: "image/jpeg" }] }),
    );
    const pictures = recording.of("drawImage").map((call) => call.args[0]);
    expect(pictures.some((image) => image instanceof HTMLImageElement)).toBe(
      true,
    );
    expect(pictures.some((image) => image instanceof HTMLCanvasElement)).toBe(
      true,
    );
  });

  it("does not export a partial image when the map fails", async () => {
    vi.mocked(renderMapSnapshot).mockRejectedValueOnce(
      new Error("map unavailable"),
    );
    await expect(renderShareCard(input({ mapCard }))).rejects.toThrow(
      "map unavailable",
    );
  });

  it("badges the video and leaves the photo beside it alone", async () => {
    await renderShareCard(
      input({
        media: [
          { mediaId: "01A", mimeType: "video/quicktime" },
          { mediaId: "01B", mimeType: "image/jpeg" },
        ],
      }),
    );

    expect(badges()).toBe(1);
  });

  it("badges nothing when every attachment is a still", async () => {
    await renderShareCard(
      input({
        media: [
          { mediaId: "01A", mimeType: "image/jpeg" },
          { mediaId: "01B", mimeType: "image/png" },
        ],
      }),
    );

    expect(badges()).toBe(0);
  });

  // The still a card carries for a video is the poster route, never the video
  // itself -- the same rule the URL helper keeps, checked here on the whole
  // render so a future rewrite of the loading cannot quietly drop it.
  it("asks the poster route for a video's still", async () => {
    await renderShareCard(
      input({ media: [{ mediaId: "01A", mimeType: "video/mp4" }] }),
    );

    const sources = recording
      .of("drawImage")
      .map(
        (call) => (call.args[0] as HTMLImageElement).dataset.recordedSrc ?? "",
      );
    expect(sources.some((source) => source.includes("/poster"))).toBe(true);
    expect(sources.some((source) => source.includes("/preview"))).toBe(false);
  });

  it.each([
    { width: 1440, height: 797 },
    { width: 900, height: 1800 },
    { width: 2400, height: 400 },
    { width: 900, height: 900 },
  ])(
    "draws a single attachment using its complete $width × $height proportions",
    async (dimensions) => {
      restoreImages();
      restoreImages = resolveImages(dimensions);
      await renderShareCard(
        input({ media: [{ mediaId: "01A", mimeType: "image/png" }] }),
      );
      const image = recording
        .of("drawImage")
        .find((call) => call.args[0] instanceof HTMLImageElement)!;
      const [, x, y, width, height] = image.args as [
        HTMLImageElement,
        number,
        number,
        number,
        number,
      ];
      expect(width / height).toBeCloseTo(
        dimensions.width / dimensions.height,
        6,
      );
      expect(width).toBeLessThanOrEqual(516 + 1e-6);
      expect(height).toBeLessThanOrEqual(544 + 1e-6);
      // The clip frame and the drawn image use the same bounds: no cover crop.
      expect(
        recording
          .of("roundRect")
          .some((call) =>
            call.args
              .slice(0, 4)
              .every(
                (value, index) =>
                  Math.abs(Number(value) - [x, y, width, height][index]!) <
                  0.001,
              ),
          ),
      ).toBe(true);
    },
  );

  it("draws table headers on one row, wraps long cells and leaves room for the following paragraph", async () => {
    const long = "UnbrokenCellContentThatMustStayInsideItsColumn";
    await renderShareCard(
      input({
        blocks: shareCardBlocks(
          `<table><tr><th>Left</th><th align="center">Center</th><th align="right">Right</th></tr><tr><td>Before</td><td>After</td><td>${long}</td></tr></table><p>AFTER_TABLE</p>`,
        ),
      }),
    );
    const calls = recording.of("fillText");
    const headings = ["Left", "Center", "Right"].map((text) =>
      calls.find((call) => call.args[0] === text)!,
    );
    expect(new Set(headings.map((call) => call.args[2])).size).toBe(1);
    expect(headings.every((call) => call.font.startsWith("700 "))).toBe(true);
    expect(Number(headings[0].args[1])).toBeLessThan(
      Number(headings[1].args[1]),
    );
    expect(Number(headings[1].args[1])).toBeLessThan(
      Number(headings[2].args[1]),
    );
    expect(recording.text()).toContain(long);
    const trailing = calls.find((call) => call.args[0] === "AFTER_TABLE")!;
    const lastTableLine = calls[calls.indexOf(trailing) - 1]!;
    expect(Number(trailing.args[2])).toBeGreaterThan(
      Number(lastTableLine.args[2]),
    );
  });

  it("marks a clipped post table and refuses to truncate an exact article excerpt", async () => {
    const blocks = shareCardBlocks(
      `<table>${Array.from({ length: 70 }, (_, index) => `<tr><td>ROW_${index}</td></tr>`).join("")}</table>`,
    );
    await renderShareCard(input({ blocks }));
    expect(recording.text()).toContain("ROW_0");
    expect(recording.text()).toContain("…");
    expect(recording.text()).not.toContain("ROW_69");
    await expect(
      renderShareCard(input({ blocks, articleTitle: "Source" })),
    ).rejects.toThrow(ShareCardTooLongError);
  });

  it("keeps the byline's two lines at the same size with a bold name", async () => {
    await renderShareCard(input());

    const sizeOf = (text: string) => {
      const call = recording
        .of("fillText")
        .find((drawn) => String(drawn.args[0]) === text);
      return Number(/(\d+(?:\.\d+)?)px/.exec(call?.font ?? "")?.[1] ?? 0);
    };
    const name = sizeOf("Cheems");
    const handle = sizeOf("@itscheems");

    expect(name).toBe(handle);
    expect(
      recording.of("fillText").find((call) => call.args[0] === "Cheems")?.font,
    ).toMatch(/^700 /);
  });

  it("draws what a link preview is and what it says", async () => {
    await renderShareCard(
      input({
        link: {
          title: "永不放弃",
          siteName: "YouTube",
          thumbnailUrl: "/api/v1/media/01C/preview",
          video: true,
        },
      }),
    );

    expect(recording.text()).toContain("YouTube");
    expect(recording.text()).toContain("永不放弃");
    // A video preview wears the same badge its thumbnail would on the page.
    expect(badges()).toBe(1);
  });

  it("keeps a link preview that has no picture", async () => {
    await renderShareCard(
      input({
        link: {
          title: "只有标题",
          siteName: "example.com",
          thumbnailUrl: null,
          video: false,
        },
      }),
    );

    expect(recording.text()).toContain("只有标题");
    expect(recording.text()).toContain("example.com");
    expect(badges()).toBe(0);
  });

  it("separates the article title from its excerpt without altering default cards", async () => {
    await renderShareCard(
      input({
        articleTitle: "来源文章",
      }),
    );
    expect(recording.text()).toContain("来源文章");
    expect(recording.of("fill").some((call) => call.fill === "#ffffff")).toBe(
      true,
    );
    expect(
      recording.of("fillText").find((call) => call.args[0] === "在场")?.fill,
    ).toBe("#0f1419");
    expect(recording.of("stroke")).toHaveLength(4);
    expect(recording.of("stroke")[0].stroke).toBe("#cfd9de");
    const before = recording.calls.length;
    const previousFills = recording.of("fill").length;
    await renderShareCard(input());
    expect(recording.of("fill")[previousFills]?.fill).toBe("#ffffff");
    expect(
      recording.calls.slice(before).filter((call) => call.op === "stroke"),
    ).toHaveLength(3);
  });

  it("exports an excerpt with the existing avatar fallback when its image fails to load", async () => {
    Object.defineProperty(HTMLImageElement.prototype, "src", {
      configurable: true,
      set(this: HTMLImageElement) {
        queueMicrotask(() => this.dispatchEvent(new Event("error")));
      },
    });
    await expect(
      renderShareCard(
        input({ articleTitle: "来源", avatarUrl: "/missing.png" }),
      ),
    ).resolves.toBeInstanceOf(Blob);
    expect(recording.of("drawImage")).toHaveLength(0);
    expect(recording.text()).toContain("CCheems@itscheems·关注来源在场");
  });

  it("wraps a long article title completely rather than truncating it", async () => {
    const articleTitle = "一个很长的来源文章标题".repeat(8);
    await renderShareCard(input({ articleTitle }));
    const titleLines = recording
      .of("fillText")
      .filter((call) => call.font.startsWith("700 24px"));
    expect(titleLines.length).toBeGreaterThan(1);
    expect(titleLines.map((call) => call.args[0]).join("")).toBe(articleTitle);
  });

  it("keeps styled runs, whitespace and hard breaks through wrapping", async () => {
    await renderShareCard(
      input({
        articleTitle: "来源",
        blocks: [
          {
            kind: "text",
            text: "前  标记\n后",
            runs: [
              { text: "前  " },
              { text: "标记", colour: "#cc0000", background: "#ffff00" },
              { text: "\n后" },
            ],
          },
        ],
      }),
    );
    const marked = recording
      .of("fillText")
      .find((call) => call.args[0] === "标记");
    expect(marked?.fill).toBe("#cc0000");
    expect(
      recording.of("fillRect").some((call) => call.fill === "#ffff00"),
    ).toBe(true);
    expect(
      recording.of("fillText").some((call) => call.args[0] === "前  "),
    ).toBe(true);
    expect(recording.of("fillText").some((call) => call.args[0] === "后")).toBe(
      true,
    );
    expect(
      recording
        .of("fillText")
        .some((call) => String(call.args[0]).includes("\n")),
    ).toBe(false);
  });

  it("accepts exactly 60 excerpt lines and rejects overflow without emitting a partial image", async () => {
    const excerpt = (count: number) =>
      input({
        articleTitle: "标题",
        blocks: [
          {
            kind: "text",
            text: "行\n".repeat(count - 1) + "行",
            runs: [{ text: "行\n".repeat(count - 1) + "行" }],
          },
        ],
      });
    await expect(renderShareCard(excerpt(60))).resolves.toBeInstanceOf(Blob);
    await expect(renderShareCard(excerpt(61))).rejects.toBeInstanceOf(
      ShareCardTooLongError,
    );
    // A legacy card still uses its established truncation rather than failing.
    await expect(
      renderShareCard(
        input({ blocks: [{ kind: "text", text: "甲".repeat(3000) }] }),
      ),
    ).resolves.toBeInstanceOf(Blob);
  });

  it("wraps long excerpt words without splitting an emoji grapheme or changing text", async () => {
    const text = "word".repeat(35) + "👨‍👩‍👧‍👦".repeat(10);
    await renderShareCard(
      input({
        articleTitle: "来源",
        blocks: [
          { kind: "text", text, runs: [{ text, background: "#ffff00" }] },
        ],
      }),
    );
    const excerpt = recording
      .of("fillText")
      .filter((call) => call.font.startsWith("400 20px"));
    expect(excerpt.map((call) => call.args[0]).join("")).toBe(text);
    for (const call of excerpt)
      expect(String(call.args[0]).replace(/👨‍👩‍👧‍👦/g, "")).not.toMatch(
        /[\u200d👨👩👧👦]/u,
      );
  });

  it("keeps excerpt closing punctuation with the preceding character at a wrap boundary", async () => {
    const text = "甲".repeat(26) + "。乙";
    await renderShareCard(
      input({
        articleTitle: "来源",
        blocks: [{ kind: "text", text, runs: [{ text }] }],
      }),
    );
    const lines = recording
      .of("fillText")
      .filter((call) => call.font.startsWith("400 20px"));
    expect(lines.map((call) => call.args[0]).join("")).toBe(text);
    expect(lines.every((call) => !String(call.args[0]).startsWith("。"))).toBe(
      true,
    );
  });
});

// The link preview comes off the wire with six fields the card has no room for
// and one -- the site's name -- that is often empty.
describe("reading a link preview", () => {
  const wire = (over = {}) =>
    ({
      position: 0,
      canonicalUrl: "https://www.bilibili.com/video/BV1",
      cardKind: "video" as const,
      title: "标题",
      description: "描述",
      siteName: "哔哩哔哩",
      authorName: "作者",
      durationSeconds: 61,
      viewCount: 10,
      commentCount: 2,
      likeCount: 3,
      faviconMediaId: null,
      thumbnailMediaId: "01THUMB",
      thumbnailMimeType: "image/jpeg",
      thumbnailWidth: 1120,
      thumbnailHeight: 630,
      embedUrl: null,
      ...over,
    }) as Parameters<typeof shareCardLink>[0];

  it("takes the thumbnail from this instance, not from the far end", () => {
    const link = shareCardLink(wire());
    expect(link.thumbnailUrl).toContain("/api/v1/media/01THUMB/preview");
    expect(link.thumbnailUrl).toMatch(/[?&]v=/);
  });

  it("falls back to the host when the site has no name", () => {
    expect(shareCardLink(wire({ siteName: "  " })).siteName).toBe(
      "bilibili.com",
    );
  });

  it("says nothing rather than guessing at an unreadable address", () => {
    expect(
      shareCardLink(wire({ siteName: "", canonicalUrl: "not a url" })).siteName,
    ).toBe("");
  });

  it("marks a video preview so it can be badged", () => {
    expect(shareCardLink(wire()).video).toBe(true);
    expect(shareCardLink(wire({ cardKind: "link" })).video).toBe(false);
  });

  it("has no thumbnail when the preview had none", () => {
    expect(
      shareCardLink(wire({ thumbnailMediaId: null })).thumbnailUrl,
    ).toBeNull();
  });
});
