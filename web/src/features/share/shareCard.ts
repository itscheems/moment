/**
 * Draw a moment as an image somebody can send.
 *
 * A link into this instance is worth nothing to a reader who cannot reach it,
 * and the chat apps that would otherwise render a card from one ask for a
 * public domain, a filing, and in WeChat's case an official account before they
 * will. A picture asks for none of that.
 *
 * It draws rather than screenshots because the fonts and the media are already
 * in the page: the card can be composed on the reader's own device with no
 * server, no browser on the server, and nothing to deploy. What that costs is
 * line breaking and block layout, which are written out below.
 *
 */

import type { PublicLinkCard } from "../../api/public";
import { mediaPosterUrl, mediaPreviewUrl } from "../../api/mediaUrls";
import { highlightCodeBlocks } from "../../components/codeHighlight";
import { locationIconArtwork } from "../../components/locationIcon";
import logoSvg from "../../assets/logo.svg?raw";
import type { MapCardData } from "../maps/types";
import type { MapViewport } from "../maps/MapCanvas";

const FACE =
  '-apple-system, "system-ui", "Segoe UI", Roboto, Helvetica, Arial, sans-serif';
const MONO = '"SFMono-Regular", Consolas, "Liberation Mono", monospace';

/**
 * The card is drawn in these units and rasterised at twice them.
 *
 * The preview and exported file share one layout; doubling its resolution
 * keeps the text sharp when the image is opened full-screen.
 */
const WIDTH = 550;
const SCALE = 2;
const PAD = 17;
/** The card's own corner. Outside it the image is transparent. */
const CARD_RADIUS = 12;

/**
 * A block of the body, in the shapes this product's markdown produces.
 *
 * Not a list of strings: a bullet list flattened into paragraphs loses its
 * bullets, and a fenced block loses its line breaks and its monospace, which is
 * most of what made somebody write it as a fenced block.
 *
 * Inline emphasis is deliberately absent. Drawing bold inside a line means
 * measuring and placing runs rather than lines; a bold word rendered plain
 * still reads, where a list without bullets does not.
 */
export type ShareCardBlock =
  | {
      kind: "text";
      text: string;
      heading?: 1 | 2 | 3;
      runs?: ShareCardTextRun[];
    }
  | { kind: "item"; marker: string; text: string; runs?: ShareCardTextRun[] }
  | { kind: "quote"; text: string; runs?: ShareCardTextRun[] }
  | { kind: "code"; lines: CodeSpan[][] }
  | { kind: "table"; rows: ShareCardTableCell[][] };

export interface ShareCardTableCell {
  text: string;
  header: boolean;
  align: "left" | "center" | "right";
}

/** Display-only reading colours, resolved before the card opens. */
export interface ShareCardTextRun {
  text: string;
  colour?: string;
  background?: string;
}

export class ShareCardTooLongError extends Error {
  constructor() {
    super("摘录超过分享卡片的 60 行上限，请关闭弹窗并缩短选区后重试。");
    this.name = "ShareCardTooLongError";
  }
}

/** One run of one colour inside a fenced line. */
export interface CodeSpan {
  text: string;
  colour: string;
}

/** One attachment, as the API describes it. */
export interface ShareCardMedia {
  mediaId: string;
  mimeType: string;
}

/**
 * The still this instance serves for a piece of media.
 *
 * Through the shared helpers, so the card asks for the same bytes under the
 * same cache key the page does. Hand-building the path drops the recipe
 * version, so a native media cache may keep an older rendition at that id.
 *
 * A video's `/preview` is the video -- megabytes of MP4 where an image was
 * wanted -- and drawing frames from a `<video>` element can reintroduce HDR
 * overexposure. The poster route already serves a colour-managed SDR JPEG.
 */
export function shareCardMediaUrl(media: ShareCardMedia): string {
  return media.mimeType.startsWith("video/")
    ? mediaPosterUrl(media.mediaId)
    : mediaPreviewUrl(media.mediaId);
}

/** Everything the card draws, already resolved by the caller. */
/** A link preview, flattened to what the card draws. */
export interface ShareCardLink {
  title: string;
  siteName: string;
  thumbnailUrl: string | null;
  video: boolean;
}

/**
 * The link preview as the card wants it.
 *
 * The page's card carries a description and a row of counts as well. They are
 * left off here: a share image is looked at once, at whatever size a chat app
 * decides, and the two lines that say what the link is survive that where six
 * do not.
 */
export function shareCardLink(card: PublicLinkCard): ShareCardLink {
  return {
    title: card.title.trim(),
    siteName: card.siteName.trim() || hostOf(card.canonicalUrl),
    thumbnailUrl: card.thumbnailMediaId
      ? mediaPreviewUrl(card.thumbnailMediaId)
      : null,
    video: card.cardKind === "video",
  };
}

/** The host a link points at, as the page shows it when a site has no name. */
function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

export interface ShareCardInput {
  displayName: string;
  username: string;
  /** Same-origin URL, or null to draw the initial instead. */
  avatarUrl: string | null;
  /** Already formatted for the reader's locale and timezone. */
  publishedLabel: string;
  blocks: ShareCardBlock[];
  /** At most four are drawn, in order. */
  media: ShareCardMedia[];
  mapCard?: MapCardData | null;
  mapViewport?: MapViewport;
  /**
   * The place, named the way the page names it.
   *
   * The place, not the address: a card reading the street and the unit number
   * where the page reads the venue has answered a question nobody asked. The
   * address exists to tell two places of the same name apart, which is why the
   * page keeps it behind the name rather than instead of it.
   */
  location: string | null;
  /** The link preview the post carries, if it carries one. */
  link: ShareCardLink | null;
  /** Present only for an exact article excerpt; never silently truncate it. */
  articleTitle?: string;
}

export type ShareCardSource =
  ShareCardInput | ((signal: AbortSignal) => Promise<ShareCardInput>);

const INK = "#0f1419";
const MUTED = "#536471";
const HAIRLINE = "#eff3f4";
const GROUND = "#ffffff";
const ACCENT = "#1d9bf0";

const NAME_SIZE = 15;
const HANDLE_SIZE = 15;
/** The place line, which stays a caption whatever the byline does. */
const PLACE_SIZE = 15;
const BODY_SIZE = 20;
const BODY_LINE = 24;
const FOOT_SIZE = 15;
const CODE_SIZE = 15;
const CODE_LINE = 23;
const HEADING_SIZES: Record<1 | 2 | 3, number> = { 1: 24, 2: 21, 3: 19 };
const AVATAR = 48;
const MEDIA_RADIUS = 14;
const ITEM_INDENT = 22;
const QUOTE_INDENT = 16;
const CODE_PAD = 14;
const BLOCK_GAP = 14;
const TABLE_SIZE = 16;
const TABLE_LINE = 22;
const TABLE_PAD = 10;
// The reader also bounds a single picture at 34rem without cropping it.
const SINGLE_MEDIA_MAX_HEIGHT = 544;
/*
 * Two items of one list are not two paragraphs.
 *
 * Given a paragraph's air between them they read as three separate thoughts,
 * which is the opposite of what a list says: these belong together and their
 * order matters.
 */
const ITEM_GAP = 4;

/*
 * Where the card stops growing.
 *
 * It is an image to be sent, not a preview to be clicked through, so there is
 * no page behind it holding the rest -- running long is better than cutting a
 * thought in half. Only a genuine essay reaches this.
 */
const MAX_BODY_LINES = 60;

const HEADER_TOP = 13;
const FOOTER_HEIGHT = 128;
const LINK_INK = "#006fd6";

const DECORATIONS = {
  info: "M13.5 8.5c0 .83-.67 1.5-1.5 1.5s-1.5-.67-1.5-1.5S11.17 7 12 7s1.5.67 1.5 1.5zM13 17v-5h-2v5h2zm-1 5.25c5.66 0 10.25-4.59 10.25-10.25S17.66 1.75 12 1.75 1.75 6.34 1.75 12 6.34 22.25 12 22.25zM20.25 12c0 4.56-3.69 8.25-8.25 8.25S3.75 16.56 3.75 12 7.44 3.75 12 3.75s8.25 3.69 8.25 8.25z",
  heart:
    "M20.884 13.19c-1.351 2.48-4.001 5.12-8.379 7.67l-.503.3-.504-.3c-4.379-2.55-7.029-5.19-8.382-7.67-1.36-2.5-1.41-4.86-.514-6.67.887-1.79 2.647-2.91 4.601-3.01 1.651-.09 3.368.56 4.798 2.01 1.429-1.45 3.146-2.1 4.796-2.01 1.954.1 3.714 1.22 4.601 3.01.896 1.81.846 4.17-.514 6.67z",
  reply:
    "M1.751 10c0-4.42 3.584-8 8.005-8h4.366c4.49 0 8.129 3.64 8.129 8.13 0 2.96-1.607 5.68-4.196 7.11l-8.054 4.46v-3.69h-.067c-4.49.1-8.183-3.51-8.183-8.01z",
  link: "M18.36 5.64c-1.95-1.96-5.11-1.96-7.07 0L9.88 7.05 8.46 5.64l1.42-1.42c2.73-2.73 7.16-2.73 9.9 0 2.73 2.74 2.73 7.17 0 9.9l-1.42 1.42-1.41-1.42 1.41-1.41c1.96-1.96 1.96-5.12 0-7.07zm-2.12 3.53l-7.07 7.07-1.41-1.41 7.07-7.07 1.41 1.41zm-12.02.71l1.42-1.42 1.41 1.42-1.41 1.41c-1.96 1.96-1.96 5.12 0 7.07 1.95 1.96 5.11 1.96 7.07 0l1.41-1.41 1.42 1.41-1.42 1.42c-2.73 2.73-7.16 2.73-9.9 0-2.73-2.74-2.73-7.17 0-9.9z",
};

const LINK_RADIUS = 14;
const LINK_BORDER = "#cfd9de";
const LINK_PAD = 14;
const LINK_SITE_SIZE = 13;
const LINK_SITE_LINE = 17;
const LINK_TITLE_SIZE = 16;
const LINK_TITLE_LINE = 22;
const LINK_TITLE_LINES = 2;

const font = (weight: number, size: number) => `${weight} ${size}px ${FACE}`;
const monoFont = (size: number) => `400 ${size}px ${MONO}`;

/**
 * Where a line may end.
 *
 * Latin needs its words kept whole, and CJK has no spaces to break at -- a
 * measure-per-character loop would tear "moment" into "mome / nt", and a
 * split-on-spaces loop would make one unbreakable run out of a Chinese
 * paragraph. So the text is cut into pieces that may be separated, and only the
 * pieces are measured.
 *
 * Closing punctuation joins the piece before it, because a line that starts
 * with a full stop or a closing bracket is the one thing every Chinese
 * typesetter refuses.
 */
const CJK =
  /[\u3000-\u303f\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff00-\uffef]/;
const CLOSING =
  /[\u3002\uff0c\u3001\uff0e,.!?\uff01\uff1f;\uff1b:\uff1a\uff09)\]\u3011\u300b\u300d\u300f\u3009\u201d\u2019%\u2026]/;

function breakPieces(text: string, keepClosing = false): string[] {
  const pieces: string[] = [];
  let current = "";
  for (const character of text) {
    if (
      keepClosing &&
      current === "" &&
      CLOSING.test(character) &&
      pieces.length
    ) {
      pieces[pieces.length - 1] += character;
      continue;
    }
    if (current !== "" && CLOSING.test(character)) {
      current += character;
      if (keepClosing) {
        pieces.push(current);
        current = "";
      }
      continue;
    }
    if (character === " ") {
      current += character;
      pieces.push(current);
      current = "";
      continue;
    }
    if (CJK.test(character)) {
      if (current !== "") pieces.push(current);
      pieces.push(character);
      current = "";
      continue;
    }
    current += character;
  }
  if (current !== "") pieces.push(current);
  return pieces;
}

function wrap(
  ctx: CanvasRenderingContext2D,
  text: string,
  maxWidth: number,
  preserveWhitespace = false,
): string[] {
  const lines: string[] = [];
  let line = "";
  const pieces = breakPieces(text, preserveWhitespace).flatMap((piece) =>
    preserveWhitespace && ctx.measureText(piece).width > maxWidth
      ? Array.from(
          new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(
            piece,
          ),
          ({ segment }) => segment,
        )
      : [piece],
  );
  for (const piece of pieces) {
    const candidate = line + piece;
    const measured = preserveWhitespace ? candidate : candidate.trimEnd();
    if (line !== "" && ctx.measureText(measured).width > maxWidth) {
      lines.push(preserveWhitespace ? line : line.trimEnd());
      line = preserveWhitespace ? piece : piece.trimStart();
      continue;
    }
    line = candidate;
  }
  if (preserveWhitespace || line.trim() !== "")
    lines.push(preserveWhitespace ? line : line.trimEnd());
  return lines;
}

/** Project the same measured lines onto their original colour runs. */
function wrapRuns(
  ctx: CanvasRenderingContext2D,
  runs: ShareCardTextRun[],
  width: number,
): ShareCardTextRun[][] {
  const text = runs.map((run) => run.text).join("");
  const lines: ShareCardTextRun[][] = [];
  let cursor = 0;
  for (const authoredLine of text.split("\n")) {
    for (const line of wrap(ctx, authoredLine, width, true)) {
      let start = 0;
      const end = cursor + line.length;
      lines.push(
        runs.flatMap((run) => {
          const runStart = start;
          start += run.text.length;
          const from = Math.max(cursor, runStart);
          const to = Math.min(end, start);
          return from < to
            ? [{ ...run, text: run.text.slice(from - runStart, to - runStart) }]
            : [];
        }),
      );
      cursor = end;
    }
    cursor += 1;
  }
  return lines;
}

/** One line, cut to fit and closed with an ellipsis. */
/**
 * The card's palette for highlighted code, mirroring the light theme in
 * MarkdownContent.module.css.
 *
 * The stylesheet reaches its colours through theme tokens; a canvas has no
 * cascade to read them from, and the card is white whatever theme the reader
 * is in, so the light values are the ones written out here. Keep the two in
 * step -- a card that colours a keyword differently from the post it came from
 * reads as a different renderer, which is what it is.
 */
const CODE_INK: Record<string, string> = {
  comment: MUTED,
  quote: MUTED,
  keyword: "#1b8bd6",
  "selector-tag": "#1b8bd6",
  literal: "#1b8bd6",
  section: "#1b8bd6",
  link: "#1b8bd6",
  string: "#008060",
  title: "#008060",
  name: "#008060",
  type: "#008060",
  attribute: "#008060",
  symbol: "#008060",
  bullet: "#008060",
  addition: "#008060",
  variable: "#008060",
  "template-tag": "#008060",
  "template-variable": "#008060",
  number: "#9c36b5",
  regexp: "#9c36b5",
  meta: "#9c36b5",
  built_in: "#9c36b5",
  "builtin-name": "#9c36b5",
  params: "#9c36b5",
  deletion: "#f4212e",
  doctag: "#f4212e",
};

/** The colour this element asks for, or the one it sits inside. */
function codeColour(element: Element, inherited: string): string {
  for (const name of element.classList) {
    if (!name.startsWith("hljs-")) continue;
    const colour = CODE_INK[name.slice("hljs-".length)];
    if (colour) return colour;
  }
  return inherited;
}

/**
 * A fenced block read as lines of same-coloured runs.
 *
 * highlight.js nests its spans, so the colour of a piece of text is the
 * nearest ancestor that names one -- an unnamed wrapper passes through what it
 * was given rather than resetting to the body colour.
 */
function codeSpans(pre: Element): CodeSpan[][] {
  const lines: CodeSpan[][] = [[]];
  const visit = (node: Node, colour: string) => {
    if (node.nodeType === Node.TEXT_NODE) {
      (node.nodeValue ?? "").split("\n").forEach((part, index) => {
        if (index > 0) lines.push([]);
        if (part !== "") lines[lines.length - 1]?.push({ text: part, colour });
      });
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const own = codeColour(node as Element, colour);
    for (const child of node.childNodes) visit(child, own);
  };
  for (const child of pre.childNodes) visit(child, INK);
  // The fence's closing newline is not a line somebody wrote.
  while (lines.length > 0 && (lines[lines.length - 1]?.length ?? 0) === 0)
    lines.pop();
  return lines;
}

/** As truncate, for a line that changes colour partway along. */
function truncateSpans(
  ctx: CanvasRenderingContext2D,
  spans: CodeSpan[],
  maxWidth: number,
): CodeSpan[] {
  const width = (text: string) => ctx.measureText(text).width;
  if (spans.reduce((sum, span) => sum + width(span.text), 0) <= maxWidth)
    return spans;
  const room = maxWidth - width("…");
  const kept: CodeSpan[] = [];
  let used = 0;
  for (const span of spans) {
    if (used + width(span.text) <= room) {
      kept.push(span);
      used += width(span.text);
      continue;
    }
    let cut = span.text;
    while (cut.length > 0 && used + width(cut) > room) cut = cut.slice(0, -1);
    if (cut !== "") kept.push({ colour: span.colour, text: cut });
    break;
  }
  kept.push({ colour: MUTED, text: "…" });
  return kept;
}

function truncate(
  ctx: CanvasRenderingContext2D,
  text: string,
  maxWidth: number,
): string {
  if (ctx.measureText(text).width <= maxWidth) return text;
  let cut = text;
  while (cut.length > 1 && ctx.measureText(`${cut}…`).width > maxWidth) {
    cut = cut.slice(0, -1);
  }
  return `${cut}…`;
}

function loadImage(source: string): Promise<HTMLImageElement | null> {
  return new Promise((settle) => {
    const image = new Image();
    // Same-origin in every caller, so the canvas is never tainted. Declaring it
    // anyway keeps a future absolute URL from silently poisoning toBlob.
    image.crossOrigin = "anonymous";
    image.decoding = "async";
    image.addEventListener("load", () => settle(image), { once: true });
    image.addEventListener("error", () => settle(null), { once: true });
    image.src = source;
  });
}

/**
 * Make sure the faces are in memory before anything is measured.
 *
 * A canvas asked for a font it does not have does not wait -- it silently
 * substitutes, and every width measured after that is measured against the
 * wrong glyphs, so the card wraps in places the finished image does not.
 */
async function readyFonts(): Promise<void> {
  if (!document.fonts) return;
  await Promise.all([
    document.fonts.load(font(400, BODY_SIZE)),
    document.fonts.load(font(700, NAME_SIZE)),
  ]);
  await document.fonts.ready;
}

/** Cover-fit: fill the box, crop the overflow, never letterbox. */
function drawCover(
  ctx: CanvasRenderingContext2D,
  image: HTMLImageElement,
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
) {
  const scale = Math.max(
    width / image.naturalWidth,
    height / image.naturalHeight,
  );
  const drawWidth = image.naturalWidth * scale;
  const drawHeight = image.naturalHeight * scale;
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(x, y, width, height, radius);
  ctx.clip();
  ctx.drawImage(
    image,
    x + (width - drawWidth) / 2,
    y + (height - drawHeight) / 2,
    drawWidth,
    drawHeight,
  );
  ctx.restore();
}

/**
 * The link preview's box: a picture, if there is one, over what it is.
 *
 * The picture keeps its own proportions rather than being forced to a shape,
 * within limits -- a very tall thumbnail would otherwise push everything the
 * post actually said off the bottom of the card.
 */
function linkLayout(
  ruler: CanvasRenderingContext2D,
  link: ShareCardLink,
  thumbnail: HTMLImageElement | null,
  width: number,
): { thumbHeight: number; title: string[]; height: number } | null {
  if (!thumbnail && link.title === "" && link.siteName === "") return null;
  const thumbHeight = thumbnail
    ? Math.round(
        Math.min(
          Math.max(
            (width * thumbnail.naturalHeight) / thumbnail.naturalWidth,
            width * 0.42,
          ),
          width * 0.62,
        ),
      )
    : 0;
  ruler.font = font(650, LINK_TITLE_SIZE);
  const wrapped =
    link.title === "" ? [] : wrap(ruler, link.title, width - LINK_PAD * 2);
  const title = wrapped.slice(0, LINK_TITLE_LINES);
  if (wrapped.length > title.length && title.length > 0) {
    title[title.length - 1] = truncate(
      ruler,
      title[title.length - 1] ?? "",
      width - LINK_PAD * 2,
    );
  }
  return {
    thumbHeight,
    title,
    height:
      thumbHeight +
      LINK_PAD +
      LINK_SITE_LINE +
      (title.length > 0 ? 3 + title.length * LINK_TITLE_LINE : 0) +
      LINK_PAD,
  };
}

/** Draw the link preview the post carries. */
function drawLinkCard(
  ctx: CanvasRenderingContext2D,
  link: ShareCardLink,
  thumbnail: HTMLImageElement | null,
  laid: { thumbHeight: number; title: string[]; height: number },
  x: number,
  y: number,
  width: number,
): void {
  if (thumbnail) {
    // Square-cornered inside the clip: the box's own rounding is what shapes
    // the top two corners, and rounding the picture as well leaves a sliver of
    // background showing through each one.
    ctx.save();
    ctx.beginPath();
    ctx.roundRect(x, y, width, laid.height, LINK_RADIUS);
    ctx.clip();
    drawCover(ctx, thumbnail, x, y, width, laid.thumbHeight, 0);
    ctx.restore();
    if (link.video) drawPlayBadge(ctx, x, y, width, laid.thumbHeight);
  }

  // Stroked on the half pixel, so a one-pixel border lands on one pixel.
  ctx.beginPath();
  ctx.roundRect(x + 0.5, y + 0.5, width - 1, laid.height - 1, LINK_RADIUS);
  ctx.lineWidth = 1;
  ctx.strokeStyle = LINK_BORDER;
  ctx.stroke();

  let cursor = y + laid.thumbHeight + LINK_PAD;
  if (link.siteName !== "") {
    ctx.fillStyle = MUTED;
    ctx.font = font(400, LINK_SITE_SIZE);
    ctx.fillText(
      truncate(ctx, link.siteName, width - LINK_PAD * 2),
      x + LINK_PAD,
      cursor + LINK_SITE_SIZE,
    );
  }
  cursor += LINK_SITE_LINE + (laid.title.length > 0 ? 3 : 0);
  ctx.fillStyle = INK;
  ctx.font = font(650, LINK_TITLE_SIZE);
  laid.title.forEach((line, row) => {
    ctx.fillText(
      line,
      x + LINK_PAD,
      cursor + row * LINK_TITLE_LINE + LINK_TITLE_SIZE,
    );
  });
}

/**
 * The badge a video wears on its still.
 *
 * A poster frame is a picture like any other, and a card full of pictures says
 * nothing about which of them moves. This is the same badge the page draws over
 * a paused video -- a dark disc and a white triangle -- so a reader who has seen
 * one recognises the other.
 */
function drawPlayBadge(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  height: number,
): void {
  const size = Math.max(30, Math.min(52, Math.min(width, height) * 0.3));
  const cx = x + width / 2;
  const cy = y + height / 2;

  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, size / 2, 0, Math.PI * 2);
  ctx.fillStyle = "rgb(15 20 25 / 78%)";
  ctx.fill();
  ctx.lineWidth = 1;
  ctx.strokeStyle = "rgb(255 255 255 / 24%)";
  ctx.stroke();

  // Nudged right of centre, the way the page nudges its own glyph: a triangle
  // centred on its bounding box carries its weight to the left and reads as
  // leaning backwards.
  const arm = size * 0.3;
  const left = cx - arm * 0.6 + size * 0.04;
  ctx.beginPath();
  ctx.moveTo(left, cy - arm);
  ctx.lineTo(cx + arm * 0.85 + size * 0.04, cy);
  ctx.lineTo(left, cy + arm);
  ctx.closePath();
  // Stroked as well as filled, with a round join, which is how the glyph on the
  // page gets its softened points.
  ctx.lineJoin = "round";
  ctx.lineWidth = size * 0.09;
  ctx.fillStyle = "#ffffff";
  ctx.strokeStyle = "#ffffff";
  ctx.fill();
  ctx.stroke();
  ctx.restore();
}

/**
 * The media block's shape, which follows the count.
 *
 * A single attachment keeps its full aspect ratio, as it does in the reader.
 * Multiple attachments retain the compact grid used by the timeline.
 */
function mediaLayout(
  images: HTMLImageElement[],
  width: number,
): { boxes: Array<[number, number, number, number]>; height: number } {
  const gap = 6;
  const half = (width - gap) / 2;
  const count = images.length;
  if (count === 1) {
    const image = images[0]!;
    const scale = Math.min(
      width / image.naturalWidth,
      SINGLE_MEDIA_MAX_HEIGHT / image.naturalHeight,
    );
    const imageWidth = image.naturalWidth * scale;
    const height = image.naturalHeight * scale;
    return { boxes: [[0, 0, imageWidth, height]], height };
  }
  const height = Math.round(width * 0.62);
  if (count === 2) {
    return {
      boxes: [
        [0, 0, half, height],
        [half + gap, 0, half, height],
      ],
      height,
    };
  }
  const short = (height - gap) / 2;
  if (count === 3) {
    return {
      boxes: [
        [0, 0, half, height],
        [half + gap, 0, half, short],
        [half + gap, short + gap, half, short],
      ],
      height,
    };
  }
  return {
    boxes: [
      [0, 0, half, short],
      [half + gap, 0, half, short],
      [0, short + gap, half, short],
      [half + gap, short + gap, half, short],
    ],
    height,
  };
}

/** A block once its lines are known and its height can be added up. */
type LaidBlock =
  | {
      kind: "lines";
      item?: boolean;
      lines: string[];
      size: number;
      leading: number;
      weight: number;
      indent: number;
      marker?: string;
      quote?: boolean;
      runs?: ShareCardTextRun[][];
    }
  | { kind: "code"; lines: CodeSpan[][] }
  | LaidTable;

interface LaidTable {
  kind: "table";
  columnWidth: number;
  rows: Array<{
    height: number;
    cells: Array<Omit<ShareCardTableCell, "text"> & { lines: string[] }>;
  }>;
}

function blockHeight(piece: LaidBlock): number {
  if (piece.kind === "table")
    return piece.rows.reduce((sum, row) => sum + row.height, 0);
  return piece.kind === "code"
    ? piece.lines.length * CODE_LINE + CODE_PAD * 2
    : piece.lines.length * piece.leading;
}

function layOutTable(
  ruler: CanvasRenderingContext2D,
  block: Extract<ShareCardBlock, { kind: "table" }>,
  width: number,
  remaining: number,
  exact: boolean,
): { table: LaidTable; lines: number; clipped: boolean } {
  const columns = Math.max(1, ...block.rows.map((row) => row.length));
  const table: LaidTable = {
    kind: "table",
    columnWidth: width / columns,
    rows: [],
  };
  const pad = Math.min(TABLE_PAD, table.columnWidth / 4);
  let used = 0;
  for (const row of block.rows) {
    const cells = Array.from({ length: columns }, (_, index) => {
      const cell = row[index] ?? {
        text: "",
        header: false,
        align: "left" as const,
      };
      ruler.font = font(cell.header ? 700 : 400, TABLE_SIZE);
      const lines = wrap(ruler, cell.text, table.columnWidth - pad * 2, true);
      return { header: cell.header, align: cell.align, lines };
    });
    const lines = Math.max(1, ...cells.map((cell) => cell.lines.length));
    if (used + lines > remaining) {
      if (exact) throw new ShareCardTooLongError();
      table.rows.push({
        height: TABLE_LINE + pad * 2,
        cells: cells.map((cell, index) => ({
          ...cell,
          header: false,
          lines: [index === 0 ? "…" : ""],
        })),
      });
      return { table, lines: used + 1, clipped: true };
    }
    table.rows.push({ cells, height: lines * TABLE_LINE + pad * 2 });
    used += lines;
  }
  return { table, lines: used, clipped: false };
}

function drawTable(
  ctx: CanvasRenderingContext2D,
  table: LaidTable,
  x: number,
  y: number,
) {
  const pad = Math.min(TABLE_PAD, table.columnWidth / 4);
  for (const row of table.rows) {
    row.cells.forEach((cell, column) => {
      const left = x + column * table.columnWidth;
      ctx.fillStyle = cell.header ? HAIRLINE : GROUND;
      ctx.fillRect(left, y, table.columnWidth, row.height);
      ctx.strokeStyle = LINK_BORDER;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(left, y);
      ctx.lineTo(left + table.columnWidth, y);
      ctx.lineTo(left + table.columnWidth, y + row.height);
      ctx.lineTo(left, y + row.height);
      ctx.closePath();
      ctx.stroke();
      ctx.font = font(cell.header ? 700 : 400, TABLE_SIZE);
      ctx.fillStyle = INK;
      cell.lines.forEach((line, index) => {
        const spare = table.columnWidth - pad * 2 - ctx.measureText(line).width;
        const offset =
          cell.align === "right"
            ? spare
            : cell.align === "center"
              ? spare / 2
              : 0;
        ctx.fillText(
          line,
          left + pad + Math.max(0, offset),
          y + pad + index * TABLE_LINE + baseline(ctx, line, TABLE_LINE),
        );
      });
    });
    y += row.height;
  }
}

/**
 * Lay the body out before anything is drawn.
 *
 * The card's height is the sum of what it will hold, and a canvas cannot be
 * resized once it has been painted -- so every line is measured first, against
 * a throwaway context, and only then is the real one made.
 */
function layOutBody(
  ruler: CanvasRenderingContext2D,
  blocks: ShareCardBlock[],
  contentWidth: number,
  exactExcerpt = false,
): LaidBlock[] {
  const laid: LaidBlock[] = [];
  let used = 0;
  let clipped = false;

  for (const block of blocks) {
    if (clipped) break;
    if (block.kind === "table") {
      const result = layOutTable(
        ruler,
        block,
        contentWidth,
        Math.max(0, MAX_BODY_LINES - used),
        exactExcerpt,
      );
      laid.push(result.table);
      used += result.lines;
      clipped = result.clipped;
      continue;
    }
    if (block.kind === "code") {
      if (exactExcerpt && used + block.lines.length > MAX_BODY_LINES)
        throw new ShareCardTooLongError();
      const shown = block.lines.slice(0, Math.max(MAX_BODY_LINES - used, 0));
      if (shown.length < block.lines.length) clipped = true;
      if (shown.length === 0) break;
      ruler.font = monoFont(CODE_SIZE);
      laid.push({
        kind: "code",
        // A fenced block is written with its line breaks meant; wrapping them
        // would reflow somebody's code. Anything too wide is cut instead.
        lines: shown.map((line) =>
          truncateSpans(ruler, line, contentWidth - CODE_PAD * 2),
        ),
      });
      used += shown.length;
      continue;
    }
    const heading = block.kind === "text" ? block.heading : undefined;
    const size = heading ? HEADING_SIZES[heading] : BODY_SIZE;
    const weight = heading ? 700 : 400;
    const indent =
      block.kind === "item"
        ? ITEM_INDENT
        : block.kind === "quote"
          ? QUOTE_INDENT
          : 0;
    ruler.font = font(weight, size);
    const runs = block.runs
      ? wrapRuns(ruler, block.runs, contentWidth - indent)
      : undefined;
    const wrapped = runs
      ? runs.map((line) => line.map((run) => run.text).join(""))
      : wrap(ruler, block.text, contentWidth - indent);
    if (exactExcerpt && used + wrapped.length > MAX_BODY_LINES)
      throw new ShareCardTooLongError();
    const shown = wrapped.slice(0, Math.max(MAX_BODY_LINES - used, 0));
    if (shown.length < wrapped.length) clipped = true;
    if (shown.length === 0) break;
    if (clipped) {
      shown[shown.length - 1] = truncate(
        ruler,
        shown[shown.length - 1] ?? "",
        contentWidth - indent - 16,
      );
    }
    laid.push({
      kind: "lines",
      lines: shown,
      size,
      leading: heading ? Math.round(size * 1.35) : BODY_LINE,
      weight,
      indent,
      marker: block.kind === "item" ? block.marker : undefined,
      item: block.kind === "item",
      quote: block.kind === "quote",
      ...(runs ? { runs: runs.slice(0, shown.length) } : {}),
    });
    used += shown.length;
  }
  return laid;
}

function drawDecoration(
  ctx: CanvasRenderingContext2D,
  icon: keyof typeof DECORATIONS,
  x: number,
  y: number,
  colour: string,
) {
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(20 / 24, 20 / 24);
  ctx.fillStyle = colour;
  ctx.fill(new Path2D(DECORATIONS[icon]));
  ctx.restore();
}

function drawLogo(ctx: CanvasRenderingContext2D) {
  const source = new DOMParser().parseFromString(logoSvg, "image/svg+xml");
  const path = source.querySelector("path")?.getAttribute("d");
  if (!path) throw new Error("the share card logo has no path");
  ctx.save();
  ctx.translate(WIDTH - PAD - 25, HEADER_TOP);
  ctx.scale(25 / 1044, 25 / 1044);
  ctx.translate(-105, -110.5);
  ctx.fillStyle = INK;
  ctx.fill(new Path2D(path));
  ctx.restore();
}

/** Place text within a line box using the font the browser actually resolved. */
function baseline(
  ctx: CanvasRenderingContext2D,
  text: string,
  leading: number,
) {
  const { fontBoundingBoxAscent: ascent, fontBoundingBoxDescent: descent } =
    ctx.measureText(text);
  return (leading - ascent - descent) / 2 + ascent;
}

/** These are part of the exported image, so they never handle user input. */
function drawFooter(ctx: CanvasRenderingContext2D, label: string, y: number) {
  const width = WIDTH - PAD * 2;
  ctx.font = font(400, FOOT_SIZE);
  ctx.fillStyle = MUTED;
  const date = truncate(ctx, label, width - 32);
  ctx.fillText(date, PAD, y + 8 + baseline(ctx, date, 20));
  drawDecoration(ctx, "info", WIDTH - PAD - 20, y + 8, MUTED);

  ctx.strokeStyle = LINK_BORDER;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(PAD, y + 38.5);
  ctx.lineTo(WIDTH - PAD, y + 38.5);
  ctx.stroke();

  drawDecoration(ctx, "heart", PAD + 2, y + 49, "#f91880");
  drawDecoration(ctx, "reply", PAD + 54, y + 49, ACCENT);
  ctx.font = font(700, 14);
  ctx.fillStyle = MUTED;
  ctx.fillText("回复", PAD + 84, y + 51 + baseline(ctx, "回复", 16));
  const linkX = PAD + 84 + ctx.measureText("回复").width + 22;
  drawDecoration(ctx, "link", linkX, y + 49, MUTED);
  ctx.fillStyle = MUTED;
  ctx.fillText(
    "复制帖子链接",
    linkX + 30,
    y + 51 + baseline(ctx, "复制帖子链接", 16),
  );

  ctx.beginPath();
  ctx.roundRect(PAD, y + 79, width, 32, 16);
  ctx.stroke();
  ctx.fillStyle = LINK_INK;
  ctx.textAlign = "center";
  ctx.fillText(
    "Read More on Moment",
    WIDTH / 2,
    y + 87 + baseline(ctx, "Read More on Moment", 16),
  );
  ctx.textAlign = "left";
}

/** Render the card and hand back the bytes. */
export async function renderShareCard(
  input: ShareCardInput,
  signal?: AbortSignal,
): Promise<Blob> {
  signal?.throwIfAborted();
  await readyFonts();

  const contentWidth = WIDTH - PAD * 2;
  const media = input.media.slice(0, 4);
  const stills = await Promise.all(
    media.map(async (item) => ({
      image: await loadImage(shareCardMediaUrl(item)),
      video: item.mimeType.startsWith("video/"),
    })),
  );
  // A still that would not load drops out entirely rather than leaving a hole,
  // which is why what survives is carried together with what it came from.
  const drawable = stills.filter(
    (still): still is { image: HTMLImageElement; video: boolean } =>
      still.image !== null,
  );
  // One batch: the avatar used to wait for the stills to finish before it even
  // started, and a link thumbnail would have waited behind both.
  const mapHeight = input.mapViewport
    ? (contentWidth * input.mapViewport.height) / input.mapViewport.width
    : 340;
  const mapCard = input.mapCard;
  const [avatar, thumbnail, mapImage] = await Promise.all([
    input.avatarUrl ? loadImage(input.avatarUrl) : null,
    input.link?.thumbnailUrl ? loadImage(input.link.thumbnailUrl) : null,
    mapCard
      ? import("../maps/mapSnapshot").then(({ renderMapSnapshot }) =>
          renderMapSnapshot(
            mapCard,
            contentWidth,
            mapHeight,
            signal,
            input.mapViewport,
          ),
        )
      : null,
  ]);
  signal?.throwIfAborted();
  const location = input.mapCard ? null : input.location;

  const ruler = document.createElement("canvas").getContext("2d");
  if (!ruler) throw new Error("this browser has no 2d canvas");

  const laid = layOutBody(
    ruler,
    input.blocks,
    contentWidth,
    input.articleTitle !== undefined,
  );
  const titleSize = HEADING_SIZES[1];
  const titleLine = Math.round(titleSize * 1.35);
  ruler.font = font(700, titleSize);
  const titleLines =
    input.articleTitle === undefined
      ? []
      : wrap(ruler, input.articleTitle, contentWidth, true);
  const titleHeight =
    titleLines.length > 0 ? 24 + titleLines.length * titleLine : 0;
  /** The air before a block, which a list closes up within itself. */
  const gapBefore = (index: number) => {
    if (index === 0) return 0;
    const previous = laid[index - 1];
    const current = laid[index];
    const bothItems =
      previous?.kind === "lines" &&
      previous.item &&
      current?.kind === "lines" &&
      current.item;
    return bothItems ? ITEM_GAP : BLOCK_GAP;
  };
  const bodyHeight = laid.reduce(
    (total, piece, index) => total + blockHeight(piece) + gapBefore(index),
    0,
  );
  // A one-pixel rule with 16px of air on either side separates the source title.
  const bodyGap = titleLines.length > 0 ? 33 : 12;

  const linkShape = input.link
    ? linkLayout(ruler, input.link, thumbnail, contentWidth)
    : null;
  const shape =
    drawable.length > 0
      ? mediaLayout(
          drawable.map((still) => still.image),
          contentWidth,
        )
      : null;
  const height =
    HEADER_TOP +
    AVATAR +
    titleHeight +
    (bodyHeight > 0 ? bodyGap + bodyHeight : 0) +
    (linkShape ? 18 + linkShape.height : 0) +
    (shape ? 22 + shape.height : 0) +
    (mapImage ? 22 + mapHeight : 0) +
    (location ? 14 + 26 : 0) +
    FOOTER_HEIGHT;

  const canvas = document.createElement("canvas");
  canvas.width = WIDTH * SCALE;
  canvas.height = Math.round(height) * SCALE;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("this browser has no 2d canvas");
  ctx.scale(SCALE, SCALE);
  ctx.textBaseline = "alphabetic";

  ctx.fillStyle = GROUND;
  ctx.strokeStyle = LINK_BORDER;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.roundRect(0.5, 0.5, WIDTH - 1, height - 1, CARD_RADIUS);
  ctx.fill();
  ctx.stroke();

  drawLogo(ctx);
  let y = HEADER_TOP;

  if (avatar) {
    ctx.save();
    ctx.beginPath();
    ctx.arc(PAD + AVATAR / 2, y + AVATAR / 2, AVATAR / 2, 0, Math.PI * 2);
    ctx.clip();
    ctx.drawImage(avatar, PAD, y, AVATAR, AVATAR);
    ctx.restore();
  } else {
    ctx.fillStyle = HAIRLINE;
    ctx.beginPath();
    ctx.arc(PAD + AVATAR / 2, y + AVATAR / 2, AVATAR / 2, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = MUTED;
    ctx.font = font(700, 22);
    ctx.textAlign = "center";
    ctx.fillText(
      [...(input.displayName.trim() || "M")][0]?.toLocaleUpperCase() ?? "M",
      PAD + AVATAR / 2,
      y + AVATAR / 2 + 8,
    );
    ctx.textAlign = "left";
  }

  const textLeft = PAD + AVATAR + 4;
  const nameWidth = WIDTH - PAD - 25 - 12 - textLeft;
  ctx.fillStyle = INK;
  ctx.font = font(700, NAME_SIZE);
  const name = truncate(ctx, input.displayName, nameWidth);
  ctx.fillText(name, textLeft, y + 3 + baseline(ctx, name, 20));
  ctx.fillStyle = MUTED;
  ctx.font = font(700, HANDLE_SIZE);
  const followWidth = ctx.measureText("关注").width;
  ctx.font = font(400, HANDLE_SIZE);
  const dotWidth = ctx.measureText("·").width + 8;
  const handle = truncate(
    ctx,
    input.username ? `@${input.username}` : "",
    nameWidth - dotWidth - followWidth,
  );
  const handleY = y + 25 + baseline(ctx, handle, 20);
  ctx.fillText(handle, textLeft, handleY);
  const dotX = textLeft + ctx.measureText(handle).width + 4;
  ctx.fillText("·", dotX, handleY);
  ctx.fillStyle = LINK_INK;
  ctx.font = font(700, HANDLE_SIZE);
  ctx.fillText("关注", dotX + dotWidth - 4, handleY);
  y += AVATAR;

  if (titleLines.length > 0) {
    y += 24;
    ctx.fillStyle = INK;
    ctx.font = font(700, titleSize);
    titleLines.forEach((line, row) =>
      ctx.fillText(line, PAD, y + row * titleLine + titleSize),
    );
    y += titleLines.length * titleLine;
  }

  if (bodyHeight > 0) {
    if (titleLines.length > 0) {
      ctx.strokeStyle = LINK_BORDER;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(PAD, y + 16.5);
      ctx.lineTo(PAD + contentWidth, y + 16.5);
      ctx.stroke();
    }
    y += bodyGap;
    laid.forEach((piece, index) => {
      y += gapBefore(index);
      if (piece.kind === "table") {
        drawTable(ctx, piece, PAD, y);
        y += blockHeight(piece);
        return;
      }
      if (piece.kind === "code") {
        ctx.fillStyle = HAIRLINE;
        ctx.beginPath();
        ctx.roundRect(PAD, y, contentWidth, blockHeight(piece), 10);
        ctx.fill();
        ctx.font = monoFont(CODE_SIZE);
        piece.lines.forEach((line, row) => {
          const baseline = y + CODE_PAD + row * CODE_LINE + CODE_SIZE;
          let x = PAD + CODE_PAD;
          for (const span of line) {
            ctx.fillStyle = span.colour;
            ctx.fillText(span.text, x, baseline);
            x += ctx.measureText(span.text).width;
          }
        });
        y += blockHeight(piece);
        return;
      }
      if (piece.quote) {
        ctx.fillStyle = HAIRLINE;
        ctx.fillRect(PAD, y + 2, 3, blockHeight(piece) - 4);
      }
      if (piece.marker !== undefined) {
        ctx.fillStyle = MUTED;
        ctx.font = font(400, BODY_SIZE);
        ctx.fillText(piece.marker, PAD, y + piece.size);
      }
      const defaultColour = piece.quote ? MUTED : INK;
      ctx.fillStyle = defaultColour;
      ctx.font = font(piece.weight, piece.size);
      piece.lines.forEach((line, row) => {
        const x = PAD + piece.indent;
        const lineBaseline =
          y + row * piece.leading + baseline(ctx, line, piece.leading);
        const runs = piece.runs?.[row];
        if (!runs) {
          ctx.fillText(line, x, lineBaseline);
          return;
        }
        let prefix = "";
        const positions = runs.map((run) => {
          const left = ctx.measureText(prefix).width;
          prefix += run.text;
          return { run, left, width: ctx.measureText(prefix).width - left };
        });
        for (const { run, left, width } of positions) {
          if (!run.background) continue;
          ctx.fillStyle = run.background;
          ctx.fillRect(
            x + left,
            lineBaseline - piece.size,
            width,
            piece.size * 1.25,
          );
        }
        for (const { run, left } of positions) {
          ctx.fillStyle = run.colour ?? defaultColour;
          ctx.fillText(run.text, x + left, lineBaseline);
        }
      });
      y += blockHeight(piece);
    });
  }

  if (linkShape && input.link) {
    y += 18;
    drawLinkCard(ctx, input.link, thumbnail, linkShape, PAD, y, contentWidth);
    y += linkShape.height;
  }

  if (shape) {
    y += 22;
    shape.boxes.forEach((box, index) => {
      const still = drawable[index];
      if (!still) return;
      drawCover(
        ctx,
        still.image,
        PAD + box[0],
        y + box[1],
        box[2],
        box[3],
        MEDIA_RADIUS,
      );
      if (still.video) {
        drawPlayBadge(ctx, PAD + box[0], y + box[1], box[2], box[3]);
      }
    });
    y += shape.height;
  }

  if (mapImage) {
    y += 22;
    ctx.save();
    ctx.beginPath();
    ctx.roundRect(PAD, y, contentWidth, mapHeight, MEDIA_RADIUS);
    ctx.clip();
    ctx.drawImage(mapImage, PAD, y, contentWidth, mapHeight);
    ctx.restore();
    y += mapHeight;
  }

  if (location) {
    y += 14;
    const iconSize = 16;
    const textOffset = iconSize + 4.8;
    ctx.save();
    ctx.translate(PAD, y + 4);
    ctx.scale(iconSize / 24, iconSize / 24);
    ctx.strokeStyle = ACCENT;
    ctx.lineWidth = 1.7;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    ctx.stroke(new Path2D(locationIconArtwork.outline));
    ctx.beginPath();
    ctx.arc(
      locationIconArtwork.center.x,
      locationIconArtwork.center.y,
      locationIconArtwork.center.radius,
      0,
      Math.PI * 2,
    );
    ctx.stroke();
    ctx.restore();
    ctx.fillStyle = MUTED;
    ctx.font = font(400, PLACE_SIZE);
    ctx.fillText(
      truncate(ctx, location, contentWidth - textOffset),
      PAD + textOffset,
      y + 17,
    );
    y += 26;
  }

  drawFooter(ctx, input.publishedLabel, y);

  return new Promise((settle, reject) => {
    canvas.toBlob(
      (blob) =>
        blob
          ? settle(blob)
          : reject(new Error("the card could not be encoded")),
      "image/png",
    );
  });
}

/**
 * The body of a rendered post, block by block.
 *
 * The card draws type, not markup, and the post arrives as HTML. Walking the
 * blocks keeps what the author built -- the bullets of a list, the line breaks
 * of a fenced block -- which a plain `textContent` throws away, leaving one run
 * with the sentences fused at the seams.
 */
export function shareCardBlocks(html: string): ShareCardBlock[] {
  const parsed = new DOMParser().parseFromString(html, "text/html");
  highlightCodeBlocks(parsed.body);
  const blocks: ShareCardBlock[] = [];
  const flat = (node: Element) =>
    (node.textContent ?? "").replace(/\s+/g, " ").trim();

  const walk = (parent: Element) => {
    for (const node of parent.children) {
      const tag = node.tagName.toLowerCase();
      if (tag === "table") {
        const rows = Array.from(node.querySelectorAll("tr"))
          .filter((row) => row.closest("table") === node)
          .map((row) =>
            Array.from(row.children)
              .filter((cell) => /^(TD|TH)$/.test(cell.tagName))
              .map((cell): ShareCardTableCell => {
                const alignment =
                  cell.getAttribute("align") ??
                  (cell as HTMLElement).style.textAlign;
                return {
                  text: flat(cell),
                  header: cell.tagName === "TH",
                  align:
                    alignment === "center" || alignment === "right"
                      ? alignment
                      : ("left" as const),
                };
              }),
          )
          .filter((row) => row.length > 0);
        if (rows.length > 0) blocks.push({ kind: "table", rows });
        continue;
      }
      if (tag === "ul" || tag === "ol") {
        let ordinal = 1;
        for (const item of node.children) {
          if (item.tagName.toLowerCase() !== "li") continue;
          const text = flat(item);
          if (text === "") continue;
          blocks.push({
            kind: "item",
            marker: tag === "ol" ? `${ordinal}.` : "•",
            text,
          });
          ordinal += 1;
        }
        continue;
      }
      if (tag === "pre") {
        // Split on the newlines the author wrote, not on whitespace: this is
        // the one block whose shape is part of its meaning.
        const lines = codeSpans(node);
        if (
          lines.some((line) => line.some((span) => span.text.trim() !== ""))
        ) {
          blocks.push({ kind: "code", lines });
        }
        continue;
      }
      if (tag === "blockquote") {
        const text = flat(node);
        if (text !== "") blocks.push({ kind: "quote", text });
        continue;
      }
      if (/^h[1-6]$/.test(tag)) {
        const text = flat(node);
        const level = Number(tag.slice(1));
        if (text !== "") {
          blocks.push({
            kind: "text",
            text,
            heading: level <= 3 ? (level as 1 | 2 | 3) : 3,
          });
        }
        continue;
      }
      if (tag === "p") {
        const text = flat(node);
        if (text !== "") blocks.push({ kind: "text", text });
        continue;
      }
      // Anything else is a wrapper this renderer has no opinion about; its
      // children are what carry the text.
      if (node.children.length > 0) walk(node);
      else {
        const text = flat(node);
        if (text !== "") blocks.push({ kind: "text", text });
      }
    }
  };

  walk(parsed.body);
  if (blocks.length === 0) {
    const text = flat(parsed.body);
    if (text !== "") blocks.push({ kind: "text", text });
  }
  return blocks;
}
