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
 * The proportions are the timeline's own -- same faces, same neutrals -- so the
 * card reads as a moment lifted out of the page rather than a poster made about
 * one.
 */

import type { PublicLinkCard } from "../../api/public";
import { mediaPosterUrl, mediaPreviewUrl } from "../../api/mediaUrls";
import { highlightCodeBlocks } from "../../components/codeHighlight";

/** The face the timeline sets posts in. */
const FACE =
  '"TwitterChirp", -apple-system, "system-ui", "Segoe UI", Roboto, Helvetica, Arial, sans-serif';
const MONO = '"SFMono-Regular", Consolas, "Liberation Mono", monospace';

/**
 * The card is drawn in these units and rasterised at twice them.
 *
 * 540 is close to the reading column the timeline uses, so type sized against
 * it keeps the proportions a reader already knows. Twice that is 1080 across,
 * which is sharp on a phone opened full-screen and still small enough to send.
 */
const WIDTH = 540;
const SCALE = 2;
const PAD = 36;
/** The card's own corner. Outside it the image is transparent. */
const CARD_RADIUS = 24;

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
  | { kind: "text"; text: string; heading?: 1 | 2 | 3 }
  | { kind: "item"; marker: string; text: string }
  | { kind: "quote"; text: string }
  | { kind: "code"; lines: CodeSpan[][] };

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
}

const INK = "#0f1419";
const MUTED = "#536471";
const HAIRLINE = "#eff3f4";
const GROUND = "#ffffff";
const ACCENT = "#1d9bf0";

/*
 * Sizes with real distance between them.
 *
 * The first pass ran 19 / 18 / 16 / 14 -- four steps inside five pixels, which
 * reads as one undifferentiated grey block however carefully it is spaced. A
 * hierarchy is only legible where the steps are.
 */
// The two lines of the byline sit closer in size than a heading and a caption
// would. They are one identity read as a pair, and a handle set half the height
// of the name beside it reads as a footnote about the name rather than the
// other half of it.
const NAME_SIZE = 21;
const HANDLE_SIZE = 17;
/** The place line, which stays a caption whatever the byline does. */
const PLACE_SIZE = 15;
const BODY_SIZE = 18;
const BODY_LINE = 29;
const FOOT_SIZE = 13;
const CODE_SIZE = 15;
const CODE_LINE = 23;
const HEADING_SIZES: Record<1 | 2 | 3, number> = { 1: 24, 2: 21, 3: 19 };
const AVATAR = 52;
const MEDIA_RADIUS = 14;
const ITEM_INDENT = 22;
const QUOTE_INDENT = 16;
const CODE_PAD = 14;
const BLOCK_GAP = 14;
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

/** Three coloured dots decorate the share card frame. */
const WINDOW_DOTS = ["#ff5f57", "#febc2e", "#28c840"];
const DOT = 13;
const DOT_GAP = 9;
// The dots keep their own inset rather than the content margin. A window's
// traffic lights sit in the corner; lined up with the text below them they
// stop reading as a frame and start reading as a bullet list.
const DOT_INSET = 21;

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

function breakPieces(text: string): string[] {
  const pieces: string[] = [];
  let current = "";
  for (const character of text) {
    if (current !== "" && CLOSING.test(character)) {
      current += character;
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
): string[] {
  const lines: string[] = [];
  let line = "";
  for (const piece of breakPieces(text)) {
    const candidate = line + piece;
    if (line !== "" && ctx.measureText(candidate.trimEnd()).width > maxWidth) {
      lines.push(line.trimEnd());
      line = piece.trimStart();
      continue;
    }
    line = candidate;
  }
  if (line.trim() !== "") lines.push(line.trimEnd());
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
 * Nothing is letterboxed: a band of grey either side of a photo is the surest
 * way to make a card look unfinished, and where a subject falls inside the
 * frame is the photographer's composition, not this function's problem.
 */
function mediaLayout(
  count: number,
  width: number,
): { boxes: Array<[number, number, number, number]>; height: number } {
  const gap = 6;
  const half = (width - gap) / 2;
  if (count === 1) {
    const height = Math.round((width * 3) / 4);
    return { boxes: [[0, 0, width, height]], height };
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
    }
  | { kind: "code"; lines: CodeSpan[][] };

const blockHeight = (piece: LaidBlock) =>
  piece.kind === "code"
    ? piece.lines.length * CODE_LINE + CODE_PAD * 2
    : piece.lines.length * piece.leading;

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
): LaidBlock[] {
  const laid: LaidBlock[] = [];
  let used = 0;
  let clipped = false;

  for (const block of blocks) {
    if (clipped) break;
    if (block.kind === "code") {
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
    const wrapped = wrap(ruler, block.text, contentWidth - indent);
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
    });
    used += shown.length;
  }
  return laid;
}

/** Render the card and hand back the bytes. */
export async function renderShareCard(input: ShareCardInput): Promise<Blob> {
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
  const [avatar, thumbnail] = await Promise.all([
    input.avatarUrl ? loadImage(input.avatarUrl) : null,
    input.link?.thumbnailUrl ? loadImage(input.link.thumbnailUrl) : null,
  ]);

  const ruler = document.createElement("canvas").getContext("2d");
  if (!ruler) throw new Error("this browser has no 2d canvas");

  const laid = layOutBody(ruler, input.blocks, contentWidth);
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

  const linkShape = input.link
    ? linkLayout(ruler, input.link, thumbnail, contentWidth)
    : null;
  const headerHeight = DOT_INSET + DOT + 24;
  const shape =
    drawable.length > 0 ? mediaLayout(drawable.length, contentWidth) : null;
  const height =
    headerHeight +
    AVATAR +
    (bodyHeight > 0 ? 24 + bodyHeight : 0) +
    (linkShape ? 18 + linkShape.height : 0) +
    (shape ? 22 + shape.height : 0) +
    (input.location ? 14 + 26 : 0) +
    30 +
    FOOT_SIZE +
    PAD;

  const canvas = document.createElement("canvas");
  canvas.width = WIDTH * SCALE;
  canvas.height = Math.round(height) * SCALE;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("this browser has no 2d canvas");
  ctx.scale(SCALE, SCALE);
  ctx.textBaseline = "alphabetic";

  // The canvas starts transparent; only the rounded card is painted, so the
  // corners stay clear and whatever the card lands on shows through them.
  ctx.fillStyle = GROUND;
  ctx.beginPath();
  ctx.roundRect(0, 0, WIDTH, height, CARD_RADIUS);
  ctx.fill();

  // The three dots a window wears. They say nothing, which is the point: the
  // card is a small window onto a page, and the frame is the whole ornament.
  WINDOW_DOTS.forEach((colour, index) => {
    ctx.fillStyle = colour;
    ctx.beginPath();
    ctx.arc(
      DOT_INSET + DOT / 2 + index * (DOT + DOT_GAP),
      DOT_INSET + DOT / 2,
      DOT / 2,
      0,
      Math.PI * 2,
    );
    ctx.fill();
  });

  let y = headerHeight;

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

  const textLeft = PAD + AVATAR + 14;
  const nameWidth = WIDTH - PAD - textLeft;
  ctx.fillStyle = INK;
  ctx.font = font(700, NAME_SIZE);
  ctx.fillText(truncate(ctx, input.displayName, nameWidth), textLeft, y + 22);
  ctx.fillStyle = MUTED;
  ctx.font = font(400, HANDLE_SIZE);
  ctx.fillText(
    truncate(ctx, `@${input.username}`, nameWidth),
    textLeft,
    y + 44,
  );
  y += AVATAR;

  if (bodyHeight > 0) {
    y += 24;
    laid.forEach((piece, index) => {
      y += gapBefore(index);
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
      ctx.fillStyle = piece.quote ? MUTED : INK;
      ctx.font = font(piece.weight, piece.size);
      piece.lines.forEach((line, row) => {
        ctx.fillText(
          line,
          PAD + piece.indent,
          y + row * piece.leading + piece.size,
        );
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

  if (input.location) {
    y += 14;
    ctx.fillStyle = ACCENT;
    // A pin drawn rather than an icon font: one glyph is cheaper than loading a
    // sprite the card uses once.
    const pinX = PAD + 5;
    const pinY = y + 12;
    ctx.beginPath();
    ctx.arc(pinX, pinY - 3, 5, Math.PI, 0);
    ctx.lineTo(pinX, pinY + 7);
    ctx.closePath();
    ctx.fill();
    ctx.fillStyle = GROUND;
    ctx.beginPath();
    ctx.arc(pinX, pinY - 3, 2, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = MUTED;
    ctx.font = font(400, PLACE_SIZE);
    ctx.fillText(
      truncate(ctx, input.location, contentWidth - 22),
      PAD + 22,
      y + 17,
    );
    y += 26;
  }

  // No rule above the date, and no second signature beside it. The name is at
  // the top of the card; repeating it at the bottom says nothing twice, and a
  // line drawn to separate what whitespace already separates is decoration
  // wearing the costume of structure.
  y += 30;
  ctx.fillStyle = MUTED;
  ctx.font = font(400, FOOT_SIZE);
  ctx.fillText(input.publishedLabel, PAD, y + FOOT_SIZE);

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
