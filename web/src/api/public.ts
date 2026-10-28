import {
  parseMapCard,
  mapCardPayload,
  type MapCardData,
  type MapCardWire,
} from "../features/maps/types";
import { mediaOriginalUrl, mediaPreviewUrl } from "./mediaUrls";

export interface SiteIdentity {
  profileUserId: string;
  username: string;
  displayName: string;
  title: string;
  bio: string;
  timezone: string;
  publicBaseUrl: string | null;
  avatarMediaId: string | null;
  coverMediaId: string | null;
  pinnedPostId: string | null;
  commentsEnabled: boolean;
  instanceId: string | null;
  instanceInitializedAt: string | null;
}

export interface MentionSuggestion {
  userId: string;
  username: string;
  displayName: string;
  avatarMediaId: string | null;
  profileUrl: string;
}

export type PostVisibility = "everyone" | "only_me";

export const POST_VISIBILITIES: readonly PostVisibility[] = [
  "everyone",
  "only_me",
];

export function isPostVisibility(value: unknown): value is PostVisibility {
  return (POST_VISIBILITIES as readonly unknown[]).includes(value);
}

/**
 * What a post's body is made of.
 *
 * A moment is written in Markdown and carries it as `content_md`. An article
 * keeps its body in a form Markdown cannot describe, so it arrives with no
 * `content_md` at all. Both render from `content_html`, which is why one
 * timeline can carry the two without a renderer knowing the difference.
 */
export type PostKind = "moment" | "article";

export const POST_KINDS: readonly PostKind[] = ["moment", "article"];

export function isPostKind(value: unknown): value is PostKind {
  return (POST_KINDS as readonly unknown[]).includes(value);
}

/**
 * The nodes an article body may be built from, mirroring the allowlist in
 * internal/domain/article_doc.go. Anything outside it is refused rather than
 * carried: a client that keeps what it cannot name is a client that hands the
 * unknown to whatever renders next.
 */
export type ArticleNodeType =
  | "mapCard"
  | "paragraph"
  | "heading"
  | "blockquote"
  | "bulletList"
  | "orderedList"
  | "listItem"
  | "taskList"
  | "taskItem"
  | "tocNode"
  | "codeBlock"
  | "horizontalRule"
  | "table"
  | "tableRow"
  | "tableHeader"
  | "tableCell"
  | "image"
  | "hardBreak"
  | "text";

export type ArticleMarkType =
  | "bold"
  | "italic"
  | "strike"
  | "code"
  | "underline"
  | "link"
  | "textStyle"
  | "highlight"
  | "superscript"
  | "subscript";

/**
 * Every attribute any node may carry, each optional because the server writes
 * only the ones that carry information: an ordered list starting at one, a
 * cell spanning one column and a fence with no language all arrive without
 * `attrs` at all.
 *
 * Spelled the way the wire spells them, `media_id` included. This document is
 * the value an editor will hand straight back, and a shape renamed on the way
 * in is a shape that has to be renamed on the way out — one more place for the
 * two spellings to drift apart.
 */
export interface ArticleNodeAttrs {
  map_card?: MapCardWire;
  /** heading, 1 to 3. */
  level?: number;
  /** orderedList, written only when it is not 1. */
  start?: number;
  /** codeBlock, written only when the fence named a language this site knows. */
  language?: string;
  /** image. */
  media_id?: string;
  /** image, written only when the picture has a description. */
  alt?: string;
  /** tableHeader and tableCell, written only when greater than 1. */
  colspan?: number;
  rowspan?: number;
  id?: string;
  "data-toc-id"?: string;
  textAlign?: string;
  backgroundColor?: string;
  nodeTextAlign?: string;
  nodeVerticalAlign?: string;
  colwidth?: number[];
  align?: string;
  type?: string;
  checked?: boolean;
  title?: string;
  width?: number;
  height?: number;
  "data-align"?: string;
  showCaption?: boolean;
  topOffset?: number;
  maxShowCount?: number;
  showTitle?: boolean;
}

export interface ArticleMark {
  type: ArticleMarkType;
  /** Link destinations and native text/highlight colors. */
  attrs?: { href?: string; color?: string };
}

export interface ArticleNode {
  type: ArticleNodeType;
  attrs?: ArticleNodeAttrs;
  content?: ArticleNode[];
  /** Only a text node carries one, and it is never empty. */
  text?: string;
  marks?: ArticleMark[];
}

/**
 * An article body as the server serialises it.
 *
 * Shared by the Tiptap reading surface and editor. Transport parsing validates
 * the document before either surface consumes it.
 */
export interface ArticleDocument {
  schema_version: number;
  type: "doc";
  content: ArticleNode[];
}

export interface PublicArticleCover {
  mediaId: string;
  mimeType: string;
  width: number;
  height: number;
}

/**
 * What an article carries beyond a post.
 *
 * `body` and `bodySchemaVersion` are present on a detail read and null in a
 * list, together and never singly, because a page of twenty articles carrying
 * twenty full bodies would be megabytes nobody on that page reads. The same
 * split governs `contentHtml`, which is the empty string in a list: the body
 * and its rendering come and go as one fact.
 */
export interface PublicArticle {
  title: string;
  excerpt: string;
  cover: PublicArticleCover | null;
  body: ArticleDocument | null;
  bodySchemaVersion: number | null;
  /** Inline media geometry, independent of the attachment grid. */
  bodyMedia?: PublicArticleCover[];
}

export interface PublicPost {
  /** Present only on a comment projected into the shared post UI. */
  commentPostId?: string;
  author?: PublicCommentAuthor;
  id: string;
  kind: PostKind;
  /** The Markdown source of a moment; null on an article, which has none. */
  contentMd: string | null;
  /** The title, excerpt and cover of an article; null on a moment. */
  article: PublicArticle | null;
  contentHtml: string;
  createdAt: string;
  updatedAt: string;
  editedAt?: string | null;
  publishedAt: string;
  repostedAt: string | null;
  visibility: PostVisibility;
  timelineItemKind: "original" | "repost";
  commentCount: number;
  media: PublicMediaUsage[];
  linkCards: PublicLinkCard[];
  location?: PublicPostLocation | null;
  mapCard?: MapCardData | null;
  quotedPost?: PublicQuotedPost | null;
}

/**
 * A post whose body is the Markdown a writer typed.
 *
 * Kept as its own type because the composer round-trips `content_md`: handing
 * it a post that has none would save an empty body over whatever the post
 * actually holds. Narrowing with `isMomentPost` is what keeps that impossible
 * to express rather than merely discouraged.
 */
export interface PublicMomentPost extends PublicPost {
  kind: "moment";
  contentMd: string;
}

export function isMomentPost(post: PublicPost): post is PublicMomentPost {
  return post.kind === "moment" && post.contentMd !== null;
}

/**
 * A post that leads with a title and a cover rather than with its prose.
 *
 * The narrowing exists for the same reason `PublicMomentPost` does: a card
 * that reaches for `post.article.title` must not have to assert that the
 * field is there, and `parsePost` has already made it impossible for an
 * article to arrive without one.
 */
export interface PublicArticlePost extends PublicPost {
  kind: "article";
  article: PublicArticle;
}

export function isArticlePost(post: PublicPost): post is PublicArticlePost {
  return post.kind === "article" && post.article !== null;
}

/**
 * An article read in full: the detail response, where the body and the HTML
 * rendered from it both arrive.
 */
export interface PublicReadArticlePost extends PublicArticlePost {
  article: PublicArticle & {
    body: ArticleDocument;
    bodySchemaVersion: number;
  };
}

export function isReadArticlePost(
  post: PublicPost,
): post is PublicReadArticlePost {
  return (
    isArticlePost(post) &&
    post.article.body !== null &&
    post.article.bodySchemaVersion !== null
  );
}

/**
 * A captured link preview. Every field was snapshotted when the post was
 * published, so rendering one never needs a network request: the thumbnail is
 * managed media served from this instance.
 *
 * embedUrl is present only while its origin is still allowlisted by the server.
 * A card whose embed origin has since been withdrawn arrives without it and
 * renders as a static card rather than a frame the page could not load.
 */
export interface PublicLinkCard {
  position: number;
  canonicalUrl: string;
  cardKind: "link" | "video";
  title: string;
  description: string;
  siteName: string;
  authorName: string;
  durationSeconds: number | null;
  /**
   * Engagement figures captured with the rest of the snapshot. Platforms that
   * publish these as prose have them parsed server-side, so a reader renders
   * numbers and never has to know which platform a card came from.
   */
  viewCount: number | null;
  commentCount: number | null;
  likeCount: number | null;
  /** The site's own mark, captured as managed media. Null when it published none. */
  faviconMediaId: string | null;
  thumbnailMediaId: string | null;
  thumbnailMimeType: string | null;
  thumbnailWidth: number | null;
  thumbnailHeight: number | null;
  embedUrl: string | null;
}

export interface PublicAvailableQuotedPost {
  commentPostId?: string;
  mapCard?: MapCardData | null;
  author?: PublicCommentAuthor;
  unavailable: false;
  id: string;
  kind: PostKind;
  /**
   * The quoted article's card, and null when the target is a moment.
   *
   * A quote shows a card. Its `body` is always null here: embedding a whole
   * long post inside a short one is not a quote, so the server does not send
   * one and this refuses a response that does.
   */
  article: PublicArticle | null;
  contentMd: string | null;
  contentHtml: string;
  publishedAt: string;
  media: PublicMediaUsage[];
  location: PublicPostLocation | null;
}

export interface PublicUnavailableQuotedPost {
  unavailable: true;
}

export type PublicQuotedPost =
  PublicAvailableQuotedPost | PublicUnavailableQuotedPost;

export interface PublicPostLocation {
  name: string;
  displayName: string;
}

export interface PublicMediaUsage {
  mediaId: string;
  position: number;
  altText: string | null;
  mimeType: string;
  width: number;
  height: number;
}

export interface TimelinePage {
  items: PublicPost[];
  /** Visible published originals for an author, independent of page/filter. */
  authorPostCount?: number;
  /** Continues older from the last item; null once older is exhausted. */
  nextCursor: string | null;
  /** Continues newer from the first item; null once newer is exhausted. */
  prevCursor: string | null;
  /** Persistent newer watermark; absent in older recorded responses. */
  refreshCursor?: string | null;
}

export interface TimelineRequest {
  authorUserId?: string;
  cursor?: string | null;
  from?: string | null;
  to?: string | null;
  /**
   * A site day to read back from: the page holds what appeared on or before
   * it and `prevCursor` continues into what appeared after. Not with a
   * window or a cursor.
   */
  anchor?: string | null;
  /**
   * Read one kind of post and leave the other out. Absent reads both, which
   * is what the timeline itself asks for; a reading list of articles asks for
   * one. The server answers 400 to anything that is neither kind, so this is
   * typed rather than free text.
   */
  kind?: PostKind | null;
  limit?: number;
  signal?: AbortSignal;
}

export interface SearchHit {
  post: PublicPost;
  snippet: string;
}

export interface SearchPage {
  items: SearchHit[];
  yearCounts: Array<{ year: number; count: number }>;
  nextCursor: string | null;
}

export interface SearchRequest {
  query: string;
  cursor?: string | null;
  from?: string | null;
  to?: string | null;
  limit?: number;
  signal?: AbortSignal;
}

export interface ArchiveMonthCount {
  year: number;
  month: number;
  count: number;
}

export interface ArchiveMonths {
  items: ArchiveMonthCount[];
}

export interface ArchiveMonth {
  month: string;
  days: Array<{ day: number; count: number }>;
}

export interface PublicComment {
  id: string;
  postId: string;
  parentId: string | null;
  depth: 0 | 1;
  /**
   * The name stored on the row. `author` is what to render; this is kept so a
   * client can round trip a comment without losing what was persisted, and it
   * is the only identity a legacy guest row has.
   */
  authorName: string;
  author: PublicCommentAuthor;
  /**
   * The comment this one answers, resolved by the server because it can sit
   * outside the page currently loaded. Null when the comment answers nobody in
   * particular, and cleared when the comment it named was deleted.
   */
  replyTo: PublicCommentReplyTarget | null;
  contentMd: string;
  contentHtml: string;
  createdAt: string;
  media: PublicMediaUsage[];
  location: PublicPostLocation | null;
  /** The true number of replies under a root, whatever this page carried. */
  replyCount: number;
  /** Set when the root has replies beyond the ones already delivered. */
  repliesNextCursor: string | null;
}

/**
 * The identity to show beside a comment.
 *
 * A legacy guest row has no account behind it, so everything but displayName is
 * null and it renders as unlinked plain text with no badge and no preview card.
 */
export interface PublicCommentAuthor {
  userId: string | null;
  username: string | null;
  displayName: string;
  avatarMediaId: string | null;
  profileUrl: string | null;
}

export interface PublicCommentReplyTarget {
  commentId: string;
  author: PublicCommentAuthor;
}

export interface CommentThread {
  items: PublicComment[];
  pinnedCommentId: string | null;
  /**
   * The pinned root and its inline replies, delivered beside the page rather
   * than hoisted into it. The stream stays in creation order so a cursor keeps
   * meaning the same thing when the pin moves; the client renders this above
   * the stream and skips the duplicate by id.
   */
  pinned: PublicComment[];
  nextCursor: string | null;
}

export interface CommentReplyPage {
  items: PublicComment[];
  nextCursor: string | null;
}

export type CommentSort = "oldest" | "newest";

export type CommentReplyScope = "thread" | "direct";

export interface CommentContext {
  comment: PublicComment;
  replyTarget: PublicComment | null;
  rootCommentId: string;
  pinnedCommentId: string | null;
  directReplyCount: number;
}

export interface UploadedMedia {
  id: string;
  byteSize: number;
  width: number;
  height: number;
  mimeType: string;
  previewMimeType: string;
  animatedOriginal: boolean;
  previewUrl: string;
  originalUrl: string;
}

export interface CreatedComment {
  comment: PublicComment;
  replayed: boolean;
}

interface ErrorPayload {
  code?: unknown;
  message?: unknown;
  details?: unknown;
  request_id?: unknown;
}

type Fetcher = typeof fetch;

export class PublicApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId: string | null;
  readonly details: Record<string, unknown>;
  readonly retryAfterSeconds: number | null;

  constructor(
    message: string,
    options: {
      code: string;
      status: number;
      requestId?: string | null;
      details?: Record<string, unknown>;
      retryAfterSeconds?: number | null;
    },
  ) {
    super(message);
    this.name = "PublicApiError";
    this.code = options.code;
    this.status = options.status;
    this.requestId = options.requestId ?? null;
    this.details = options.details ?? {};
    this.retryAfterSeconds = options.retryAfterSeconds ?? null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(
  record: Record<string, unknown>,
  expected: string[],
): boolean {
  const actual = Object.keys(record).sort();
  const sortedExpected = [...expected].sort();
  return (
    actual.length === sortedExpected.length &&
    actual.every((value, index) => value === sortedExpected[index])
  );
}

/**
 * Accept a record carrying every required key and nothing beyond the optional
 * ones.
 *
 * Enumerating every legal key combination was fine while one field was
 * optional and stops scaling the moment a second is: each new optional field
 * doubles the list, and a combination left out reads as a corrupt response
 * rather than as the oversight it is.
 */
function hasKeys(
  record: Record<string, unknown>,
  required: string[],
  optional: string[] = [],
): boolean {
  const allowed = new Set([...required, ...optional]);
  return (
    required.every((key) => key in record) &&
    Object.keys(record).every((key) => allowed.has(key))
  );
}

function invalidResponse(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw invalidResponse(`response field ${field} must be a string`);
  }
  return value;
}

function booleanField(record: Record<string, unknown>, field: string): boolean {
  const value = record[field];
  if (typeof value !== "boolean") {
    throw invalidResponse(`response field ${field} must be a boolean`);
  }
  return value;
}

function nonNegativeIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) < 0) {
    throw invalidResponse(
      `response field ${field} must be a non-negative safe integer`,
    );
  }
  return Number(value);
}

function positiveIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = nonNegativeIntegerField(record, field);
  if (value < 1) {
    throw invalidResponse(`response field ${field} must be a positive integer`);
  }
  return value;
}

function nullableStringField(
  record: Record<string, unknown>,
  field: string,
): string | null {
  const value = record[field];
  if (value !== null && typeof value !== "string") {
    throw invalidResponse(`response field ${field} must be a string or null`);
  }
  return value;
}

function instantField(record: Record<string, unknown>, field: string): string {
  const value = stringField(record, field);
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/,
  );
  if (!match) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  const [, yearText, monthText, dayText, hourText, minuteText, secondText] =
    match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const monthDays = [
    31,
    leapYear ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > monthDays[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59
  ) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  return value;
}

const ULID_PATTERN = /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/;

/**
 * The code-point ceiling on a display name, mirroring MaxDisplayNameLength in
 * internal/domain/user.go.
 *
 * A comment's author_name is the writer's display name — the server stamps it
 * there on every write — so one number bounds both, exactly as
 * domain.MaxCommentAuthorNameCodePoints is defined as domain.MaxDisplayNameLength
 * rather than as a figure of its own. Spelling a second, smaller number beside
 * author_name meant that a single comment by an account with a 41-to-80
 * character name was rejected here, and because the rejection is thrown from
 * inside the map over the thread's items, the whole conversation rendered as an
 * error for every reader rather than that one row.
 */
const MAX_DISPLAY_NAME_CODE_POINTS = 80;

export function isCanonicalUlid(value: string): boolean {
  return ULID_PATTERN.test(value);
}
const COMMENT_HTML_TAGS = new Set([
  "A",
  "BLOCKQUOTE",
  "BR",
  "CODE",
  "EM",
  "H1",
  "H2",
  "H3",
  "H4",
  "H5",
  "H6",
  "HR",
  "LI",
  "OL",
  "P",
  "PRE",
  "STRONG",
  "UL",
]);

function validCommentLink(value: string): boolean {
  const trimmed = value.trim();
  if (
    !trimmed ||
    trimmed.startsWith("//") ||
    trimmed.includes("\\") ||
    Array.from(trimmed).some((character) => {
      const codePoint = character.codePointAt(0) ?? 0;
      return codePoint <= 0x1f || codePoint === 0x7f;
    })
  ) {
    return false;
  }
  const scheme = trimmed.match(/^([a-z][a-z0-9+.-]*):/i)?.[1]?.toLowerCase();
  if (scheme && !["http", "https", "mailto"].includes(scheme)) return false;
  try {
    const parsed = new URL(trimmed, "https://moment.invalid/");
    return ["http:", "https:", "mailto:"].includes(parsed.protocol);
  } catch {
    return false;
  }
}

function validateCommentElement(element: Element): void {
  if (!COMMENT_HTML_TAGS.has(element.tagName)) {
    throw invalidResponse(
      "comment content_html contains an unsupported element",
    );
  }
  for (const attribute of Array.from(element.attributes)) {
    const allowed =
      (element.tagName === "A" &&
        ["href", "rel", "title"].includes(attribute.name)) ||
      (/^H[1-6]$/.test(element.tagName) && attribute.name === "id") ||
      (element.tagName === "CODE" &&
        attribute.name === "class" &&
        /^language-[^\s]+$/.test(attribute.value)) ||
      (element.tagName === "OL" &&
        attribute.name === "start" &&
        /^\d+$/.test(attribute.value));
    if (!allowed) {
      throw invalidResponse(
        "comment content_html contains an unsupported attribute",
      );
    }
  }
  if (element.tagName === "A") {
    const href = element.getAttribute("href");
    if (
      !href ||
      !validCommentLink(href) ||
      element.getAttribute("rel") !== "nofollow noopener noreferrer"
    ) {
      throw invalidResponse("comment content_html contains an unsafe link");
    }
  }
  for (const child of Array.from(element.childNodes)) {
    if (child.nodeType === Node.ELEMENT_NODE) {
      validateCommentElement(child as Element);
    } else if (child.nodeType !== Node.TEXT_NODE) {
      throw invalidResponse("comment content_html contains unsupported markup");
    }
  }
}

function safeCommentHtmlField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = stringField(record, field);
  const template = document.createElement("template");
  template.innerHTML = value;
  for (const child of Array.from(template.content.childNodes)) {
    if (child.nodeType === Node.ELEMENT_NODE) {
      validateCommentElement(child as Element);
    } else if (
      child.nodeType !== Node.TEXT_NODE ||
      child.textContent?.trim() !== ""
    ) {
      throw invalidResponse("comment content_html contains unsupported markup");
    }
  }
  return value;
}

function commentTextField(
  record: Record<string, unknown>,
  field: string,
  limit: number,
): string {
  const value = stringField(record, field);
  if (!value.trim() || Array.from(value).length > limit) {
    throw invalidResponse(`response field ${field} violates comment limits`);
  }
  return value;
}

export function parseComment(
  value: unknown,
  expectedPostId?: string,
): PublicComment {
  if (!isRecord(value)) throw invalidResponse("comment must be an object");
  if (
    !hasKeys(
      value,
      [
        "id",
        "post_id",
        "parent_id",
        "depth",

        "author_name",
        "author",
        "reply_to",
        "content_md",
        "content_html",
        "created_at",
        "media",
        "reply_count",
      ],
      ["location", "replies_next_cursor"],
    )
  ) {
    throw invalidResponse("comment contains an unsupported or missing field");
  }
  const id = stringField(value, "id");
  const postId = stringField(value, "post_id");
  const parentId = nullableStringField(value, "parent_id");
  if (
    !isCanonicalUlid(id) ||
    !isCanonicalUlid(postId) ||
    (parentId !== null && !isCanonicalUlid(parentId))
  ) {
    throw invalidResponse("comment contains an invalid identifier");
  }
  if (expectedPostId && postId !== expectedPostId) {
    throw invalidResponse("comment belongs to a different post");
  }
  const depth = nonNegativeIntegerField(value, "depth");
  if (
    (parentId === null && depth !== 0) ||
    (parentId !== null && depth !== 1)
  ) {
    throw invalidResponse("comment depth does not match parent_id");
  }
  const authorName = commentTextField(
    value,
    "author_name",
    MAX_DISPLAY_NAME_CODE_POINTS,
  );
  if (
    authorName !== authorName.trim() ||
    Array.from(authorName).some((character) => {
      const codePoint = character.codePointAt(0) ?? 0;
      return codePoint <= 0x1f || codePoint === 0x7f;
    })
  ) {
    throw invalidResponse("comment author_name is not normalized");
  }
  const media = parseMediaUsages(value.media, 4, "comment");
  let location: PublicPostLocation | null = null;
  if (value.location !== undefined && value.location !== null) {
    // mirrors postdto.Location (internal/transport/postdto/dto.go)
    if (
      !isRecord(value.location) ||
      !hasExactKeys(value.location, ["name", "display_name"])
    ) {
      throw invalidResponse("comment location is invalid");
    }
    const name = stringField(value.location, "name");
    const displayName = stringField(value.location, "display_name");
    if (!name.trim() || !displayName.trim()) {
      throw invalidResponse("comment location cannot be empty");
    }
    location = { name, displayName };
  }
  const contentMd = stringField(value, "content_md");
  if (
    Array.from(contentMd).length > 2_000 ||
    (contentMd === "" ? location === null : !contentMd.trim())
  ) {
    throw invalidResponse("response field content_md violates comment limits");
  }
  const repliesNextCursor =
    value.replies_next_cursor === undefined
      ? null
      : stringField(value, "replies_next_cursor");
  const replyCount = nonNegativeIntegerField(value, "reply_count");
  if (parentId !== null && repliesNextCursor !== null) {
    throw invalidResponse("a reply cannot carry reply pagination");
  }
  return {
    id,
    postId,
    parentId,
    depth: depth as 0 | 1,
    authorName,
    author: parseCommentAuthor(value.author),
    replyTo: parseCommentReplyTarget(value.reply_to, id),
    contentMd,
    contentHtml: safeCommentHtmlField(value, "content_html"),
    createdAt: instantField(value, "created_at"),
    media,
    location,
    replyCount,
    repliesNextCursor,
  };
}

/**
 * An account either has all of its reference fields or none of them. Anything
 * between the two would render a link that goes nowhere.
 */
function parseCommentAuthor(value: unknown): PublicCommentAuthor {
  // mirrors commentdto.Author (internal/transport/commentdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "user_id",
      "username",
      "display_name",
      "avatar_media_id",
      "profile_url",
    ])
  ) {
    throw invalidResponse("comment author is invalid");
  }
  const userId = nullableStringField(value, "user_id");
  const username = nullableStringField(value, "username");
  const profileUrl = nullableStringField(value, "profile_url");
  const displayName = stringField(value, "display_name");
  if (!displayName.trim()) {
    throw invalidResponse("comment author display_name cannot be empty");
  }
  const linked = [userId, username, profileUrl].filter(
    (field) => field !== null,
  ).length;
  if (linked !== 0 && linked !== 3 && !(linked === 1 && username !== null)) {
    throw invalidResponse("comment author identity is partially resolved");
  }
  if (profileUrl !== null && profileUrl !== `/${username}`) {
    throw invalidResponse("comment author profile_url does not match username");
  }
  return {
    userId,
    username,
    displayName,
    avatarMediaId: nullableStringField(value, "avatar_media_id"),
    profileUrl,
  };
}

function parseCommentReplyTarget(
  value: unknown,
  commentId: string,
): PublicCommentReplyTarget | null {
  if (value === undefined || value === null) return null;
  // mirrors commentdto.ReplyTarget (internal/transport/commentdto/dto.go)
  if (!isRecord(value) || !hasExactKeys(value, ["comment_id", "author"])) {
    throw invalidResponse("comment reply target is invalid");
  }
  const targetId = stringField(value, "comment_id");
  if (!isCanonicalUlid(targetId) || targetId === commentId) {
    throw invalidResponse("comment reply target is not another comment");
  }
  return { commentId: targetId, author: parseCommentAuthor(value.author) };
}

/**
 * A moment attaches at most nine pictures, the grid it is composed in
 * (MaxEntryMediaUsages). An article's pictures are not attachments: the body
 * places them, and the storage layer projects them into the same media rows so
 * that reference counting stays one predicate (internal/storage/sqlite/articles.go).
 * They arrive in the same field and are bounded by MaxArticleInlineImages, so
 * holding an article to the grid's nine would reject a perfectly ordinary
 * article the tenth time it showed a picture.
 */
const MAX_MOMENT_MEDIA = 9;
const MAX_ARTICLE_MEDIA = 50;

function mediaCeiling(kind: PostKind): number {
  return kind === "article" ? MAX_ARTICLE_MEDIA : MAX_MOMENT_MEDIA;
}

function parseMediaUsages(
  value: unknown,
  maximum: number,
  subject: string,
): PublicMediaUsage[] {
  if (!Array.isArray(value) || value.length > maximum) {
    throw invalidResponse(`${subject} media must be a bounded array`);
  }
  return value.map((item, index): PublicMediaUsage => {
    // mirrors postdto.MediaProjection (internal/transport/postdto/dto.go)
    if (
      !isRecord(item) ||
      !hasExactKeys(item, [
        "media_id",
        "position",
        "alt_text",
        "mime_type",
        "width",
        "height",
      ]) ||
      !isCanonicalUlid(stringField(item, "media_id")) ||
      item.position !== index
    ) {
      throw invalidResponse(`${subject} media usage is not canonical`);
    }
    const altText = nullableStringField(item, "alt_text");
    if (
      altText !== null &&
      ((altText !== "" && !altText.trim()) ||
        Array.from(altText).length > 1_000)
    ) {
      throw invalidResponse(`${subject} media alt_text is not canonical`);
    }
    return {
      mediaId: stringField(item, "media_id"),
      position: index,
      mimeType: mediaMimeTypeField(item, "mime_type"),
      altText,
      width: positiveIntegerField(item, "width"),
      height: positiveIntegerField(item, "height"),
    };
  });
}

function commentOrder(left: PublicComment, right: PublicComment): number {
  const time = Date.parse(left.createdAt) - Date.parse(right.createdAt);
  return time || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0);
}

function parseCommentThread(
  value: unknown,
  postId: string,
  nextCursor: string | null,
  sort: CommentSort = "oldest",
): CommentThread {
  if (
    !isRecord(value) ||
    !hasKeys(value, ["items", "pinned_comment_id", "pinned"]) ||
    !Array.isArray(value.items)
  ) {
    throw invalidResponse(
      "comment thread must contain items, pinned_comment_id and pinned",
    );
  }
  const pinnedCommentId = nullableStringField(value, "pinned_comment_id");
  if (pinnedCommentId !== null && !isCanonicalUlid(pinnedCommentId)) {
    throw invalidResponse("pinned_comment_id must be a comment ID");
  }
  const items = value.items.map((item) => parseComment(item, postId));
  const ids = new Set<string>();
  let currentRoot = "";
  let previousRoot: PublicComment | null = null;
  let previousReply: PublicComment | null = null;
  for (const comment of items) {
    if (ids.has(comment.id)) {
      throw invalidResponse("comment thread contains a duplicate comment");
    }
    ids.add(comment.id);
    if (comment.parentId === null) {
      if (
        previousRoot &&
        commentOrder(previousRoot, comment) * (sort === "newest" ? -1 : 1) >= 0
      ) {
        throw invalidResponse("comment roots are not in stable order");
      }
      currentRoot = comment.id;
      previousRoot = comment;
      previousReply = null;
    } else {
      if (comment.parentId !== currentRoot) {
        throw invalidResponse("comment reply is not adjacent to its root");
      }
      if (
        previousReply &&
        commentOrder(previousReply, comment) * (sort === "newest" ? -1 : 1) >= 0
      ) {
        throw invalidResponse("comment replies are not in stable order");
      }
      previousReply = comment;
    }
  }
  // The pinned root is no longer required to appear in items. Under pagination
  // it legitimately may not: it can sit many pages further down the stream,
  // which is exactly why it travels separately.
  for (const comment of items) {
    if (comment.id === pinnedCommentId && comment.parentId !== null) {
      throw invalidResponse("pinned_comment_id must identify a root comment");
    }
  }
  const pinned = Array.isArray(value.pinned)
    ? value.pinned.map((item) => parseComment(item, postId))
    : [];
  if (value.pinned !== null && !Array.isArray(value.pinned)) {
    throw invalidResponse("pinned conversation must be an array or null");
  }
  if (pinned.length > 0) {
    if (
      pinned[0].id !== pinnedCommentId ||
      pinned[0].parentId !== null ||
      pinned.slice(1).some((reply) => reply.parentId !== pinned[0].id)
    ) {
      throw invalidResponse(
        "pinned conversation does not lead with the pinned root",
      );
    }
  }
  return { items, pinnedCommentId, pinned, nextCursor };
}

function parseCommentReplyPage(
  value: unknown,
  postId: string,
  rootId: string,
  nextCursor: string | null,
  scope: CommentReplyScope = "thread",
  sort: CommentSort = "oldest",
): CommentReplyPage {
  // mirrors locationdto.Results (internal/transport/locationdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["items"]) ||
    !Array.isArray(value.items)
  ) {
    throw invalidResponse("comment reply page must contain items");
  }
  const items = value.items.map((item) => parseComment(item, postId));
  let previous: PublicComment | null = null;
  for (const comment of items) {
    if (
      scope === "thread"
        ? comment.parentId !== rootId
        : comment.parentId === null ||
          (comment.replyTo?.commentId !== rootId &&
            !(comment.replyTo === null && comment.parentId === rootId))
    ) {
      throw invalidResponse("comment reply page mixes conversations");
    }
    if (
      previous &&
      commentOrder(previous, comment) * (sort === "newest" ? -1 : 1) >= 0
    ) {
      throw invalidResponse("comment replies are not in stable order");
    }
    previous = comment;
  }
  return { items, nextCursor };
}

/**
 * A post's kind together with the body that kind is allowed to carry.
 *
 * The two are one fact split across two fields, so they are decoded as one:
 * a moment without Markdown and an article with it both describe a post the
 * server cannot have produced, and letting either through would push the
 * contradiction into a renderer that has no way to resolve it.
 */
function postBody(
  record: Record<string, unknown>,
  subject: string,
): { kind: PostKind; contentMd: string | null } {
  const kind = record.kind;
  if (!isPostKind(kind)) {
    throw invalidResponse(`${subject} kind is invalid`);
  }
  const contentMd = nullableStringField(record, "content_md");
  if (kind === "moment" ? contentMd === null : contentMd !== null) {
    throw invalidResponse(`${subject} content_md does not match its kind`);
  }
  return { kind, contentMd };
}

const ARTICLE_NODE_TYPES: readonly ArticleNodeType[] = [
  "mapCard",
  "paragraph",
  "heading",
  "blockquote",
  "bulletList",
  "orderedList",
  "listItem",
  "taskList",
  "taskItem",
  "tocNode",
  "codeBlock",
  "horizontalRule",
  "table",
  "tableRow",
  "tableHeader",
  "tableCell",
  "image",
  "hardBreak",
  "text",
];

const ARTICLE_MARK_TYPES: readonly ArticleMarkType[] = [
  "bold",
  "italic",
  "strike",
  "code",
  "underline",
  "link",
  "textStyle",
  "highlight",
  "superscript",
  "subscript",
];

/**
 * Which attributes each node may carry, mirroring articleNodeSpecs in
 * internal/domain/article_doc.go. A node whose `attrs` names anything outside
 * its own row is refused rather than trimmed.
 */
const ARTICLE_NODE_ATTRS: Record<ArticleNodeType, readonly string[]> = {
  mapCard: ["id", "map_card"],
  paragraph: [
    "id",
    "textAlign",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  heading: [
    "level",
    "id",
    "data-toc-id",
    "textAlign",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  blockquote: ["id", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"],
  bulletList: ["id", "backgroundColor"],
  orderedList: ["start", "type", "id", "backgroundColor"],
  listItem: [],
  taskList: ["id", "backgroundColor"],
  taskItem: ["checked"],
  codeBlock: ["language", "id"],
  horizontalRule: [],
  table: ["id"],
  tableRow: [],
  tableHeader: [
    "colspan",
    "rowspan",
    "colwidth",
    "align",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  tableCell: [
    "colspan",
    "rowspan",
    "colwidth",
    "align",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  image: [
    "media_id",
    "alt",
    "title",
    "width",
    "height",
    "data-align",
    "showCaption",
  ],
  tocNode: ["id", "backgroundColor", "topOffset", "maxShowCount", "showTitle"],
  hardBreak: [],
  text: [],
};

/**
 * The attributes a node cannot be written without, as against the ones it may
 * leave out because the default carries no information. A heading is nothing
 * without its level and a picture is nothing without the media it names.
 */
const ARTICLE_REQUIRED_ATTRS: Partial<Record<ArticleNodeType, string>> = {
  mapCard: "map_card",
  heading: "level",
  image: "media_id",
};

/** The nodes that hold nothing: a rule, a break and a picture are leaves. */
const ARTICLE_LEAF_NODES: readonly ArticleNodeType[] = [
  "mapCard",
  "horizontalRule",
  "hardBreak",
  "tocNode",
];

/** Mirrors MaxArticleDepth and MaxArticleNodes (internal/domain/article_doc.go). */
const MAX_ARTICLE_DEPTH = 10;
const MAX_ARTICLE_NODES = 10_000;

function isArticleNodeType(value: unknown): value is ArticleNodeType {
  return (ARTICLE_NODE_TYPES as readonly unknown[]).includes(value);
}

function isArticleMarkType(value: unknown): value is ArticleMarkType {
  return (ARTICLE_MARK_TYPES as readonly unknown[]).includes(value);
}

/**
 * A link an article may point at, mirroring validateLink in
 * internal/domain/markdown.go: a relative destination, or one of the three
 * schemes a reader can be sent to safely.
 */
function isSafeArticleHref(value: string): boolean {
  const trimmed = value.trim();
  if (trimmed === "" || trimmed.startsWith("//")) return false;
  const scheme = trimmed.match(/^([A-Za-z][A-Za-z0-9+.-]*):/);
  if (!scheme) return true;
  return ["http", "https", "mailto"].includes(scheme[1].toLowerCase());
}

function parseArticleMarks(value: unknown): ArticleMark[] {
  if (!Array.isArray(value)) {
    throw invalidResponse("article body marks must be an array");
  }
  const seen = new Set<ArticleMarkType>();
  return value.map((item): ArticleMark => {
    if (!isRecord(item)) {
      throw invalidResponse("article body mark must be an object");
    }
    const type = item.type;
    if (!isArticleMarkType(type)) {
      throw invalidResponse(
        "article body carries a mark this client cannot read",
      );
    }
    if (seen.has(type)) {
      throw invalidResponse("article body repeats a mark on one run of text");
    }
    seen.add(type);
    if (type === "textStyle" || type === "highlight") {
      if (
        !hasExactKeys(item, ["type", "attrs"]) ||
        !isRecord(item.attrs) ||
        !hasExactKeys(item.attrs, ["color"])
      )
        throw invalidResponse("color mark must carry a color");
      return { type, attrs: { color: stringField(item.attrs, "color") } };
    }
    if (type !== "link") {
      if (!hasExactKeys(item, ["type"])) {
        throw invalidResponse(
          `article body mark ${type} carries no attributes`,
        );
      }
      return { type };
    }
    if (!hasExactKeys(item, ["type", "attrs"]) || !isRecord(item.attrs)) {
      throw invalidResponse("article body link mark must carry its attributes");
    }
    if (!hasExactKeys(item.attrs, ["href"])) {
      throw invalidResponse("article body link mark carries only an href");
    }
    const href = stringField(item.attrs, "href");
    if (!isSafeArticleHref(href)) {
      throw invalidResponse("article body link destination is not allowed");
    }
    return { type, attrs: { href } };
  });
}

/**
 * The attributes of one node, kept in the wire's own spelling and read only
 * through the row `ARTICLE_NODE_ATTRS` allows the node.
 */
function parseArticleNodeAttrs(
  type: ArticleNodeType,
  value: unknown,
): ArticleNodeAttrs {
  const allowed = ARTICLE_NODE_ATTRS[type];
  if (!isRecord(value) || allowed.length === 0) {
    throw invalidResponse(`article body node ${type} carries no attributes`);
  }
  if (!Object.keys(value).every((key) => allowed.includes(key))) {
    throw invalidResponse(
      `article body node ${type} carries an unknown attribute`,
    );
  }
  const attrs: ArticleNodeAttrs = {};
  if ("map_card" in value)
    attrs.map_card = mapCardPayload(parseMapCard(value.map_card));
  if ("level" in value) {
    const level = positiveIntegerField(value, "level");
    if (level > 3) {
      throw invalidResponse("article body heading level must be 1, 2 or 3");
    }
    attrs.level = level;
  }
  if ("start" in value) attrs.start = positiveIntegerField(value, "start");
  if ("language" in value) attrs.language = stringField(value, "language");
  if ("media_id" in value) {
    const mediaId = stringField(value, "media_id");
    if (!isCanonicalUlid(mediaId)) {
      throw invalidResponse("article body picture names no managed media");
    }
    attrs.media_id = mediaId;
  }
  if ("alt" in value) attrs.alt = stringField(value, "alt");
  if ("colspan" in value)
    attrs.colspan = positiveIntegerField(value, "colspan");
  if ("rowspan" in value)
    attrs.rowspan = positiveIntegerField(value, "rowspan");
  for (const name of [
    "id",
    "data-toc-id",
    "textAlign",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
    "type",
    "title",
    "data-align",
    "align",
  ] as const) {
    if (name in value) attrs[name] = stringField(value, name);
  }
  for (const name of ["checked", "showCaption", "showTitle"] as const) {
    if (name in value) attrs[name] = booleanField(value, name);
  }
  for (const name of ["width", "height", "maxShowCount"] as const) {
    if (name in value) attrs[name] = positiveIntegerField(value, name);
  }
  if ("topOffset" in value)
    attrs.topOffset = nonNegativeIntegerField(value, "topOffset");
  if ("colwidth" in value) {
    if (
      !Array.isArray(value.colwidth) ||
      !value.colwidth.every((x) => Number.isSafeInteger(x) && x >= 0)
    )
      throw invalidResponse("column widths must be positive integers");
    attrs.colwidth = value.colwidth as number[];
  }
  return attrs;
}

function parseArticleNodes(
  value: unknown,
  depth: number,
  budget: { remaining: number },
): ArticleNode[] {
  if (!Array.isArray(value)) {
    throw invalidResponse("article body content must be an array");
  }
  return value.map((item) => parseArticleNode(item, depth, budget));
}

function parseArticleNode(
  value: unknown,
  depth: number,
  budget: { remaining: number },
): ArticleNode {
  if (depth > MAX_ARTICLE_DEPTH) {
    throw invalidResponse(
      "article body is nested deeper than this client reads",
    );
  }
  budget.remaining -= 1;
  if (budget.remaining < 0) {
    throw invalidResponse(
      "article body holds more nodes than this client reads",
    );
  }
  if (!isRecord(value)) {
    throw invalidResponse("article body node must be an object");
  }
  const type = value.type;
  if (!isArticleNodeType(type)) {
    throw invalidResponse(
      "article body carries a node this client cannot read",
    );
  }
  if (!hasKeys(value, ["type"], ["attrs", "content", "text", "marks"])) {
    throw invalidResponse(
      `article body node ${type} carries an unsupported field`,
    );
  }
  const node: ArticleNode = { type };
  if ("attrs" in value) node.attrs = parseArticleNodeAttrs(type, value.attrs);
  const required = ARTICLE_REQUIRED_ATTRS[type];
  if (required !== undefined && !(required in (node.attrs ?? {}))) {
    throw invalidResponse(
      `article body node ${type} must name its ${required}`,
    );
  }
  if ("content" in value) {
    if (ARTICLE_LEAF_NODES.includes(type)) {
      throw invalidResponse(`article body node ${type} holds nothing`);
    }
    node.content = parseArticleNodes(value.content, depth + 1, budget);
  }
  if (type === "text") {
    const text = stringField(value, "text");
    if (text === "") {
      throw invalidResponse("article body carries a run of text with no text");
    }
    node.text = text;
    if ("content" in value) {
      throw invalidResponse("article body text node carries no children");
    }
    if ("marks" in value) node.marks = parseArticleMarks(value.marks);
    return node;
  }
  if ("text" in value || "marks" in value) {
    throw invalidResponse(`article body node ${type} is not a run of text`);
  }
  return node;
}

/**
 * One article body. The document is rebuilt field by field rather than passed
 * through, so what a caller holds is exactly what this grammar allows and
 * nothing a response smuggled alongside it.
 */
export function parseArticleDocument(value: unknown): ArticleDocument {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["schema_version", "type", "content"])
  ) {
    throw invalidResponse(
      "article body contains an unsupported or missing field",
    );
  }
  if (value.type !== "doc") {
    throw invalidResponse("article body must be a document");
  }
  const schemaVersion = positiveIntegerField(value, "schema_version");
  if (schemaVersion > 3)
    throw invalidResponse("article body schema is not supported");
  const budget = { remaining: MAX_ARTICLE_NODES };
  const content = parseArticleNodes(value.content, 1, budget);
  const containsMap = (nodes: ArticleNode[]): boolean =>
    nodes.some(
      (node) =>
        node.type === "mapCard" ||
        (node.content ? containsMap(node.content) : false),
    );
  if (schemaVersion < 3 && containsMap(content))
    throw invalidResponse("map nodes require article schema 3");
  return { schema_version: schemaVersion, type: "doc", content };
}

function parseArticleCover(value: unknown): PublicArticleCover | null {
  if (value === null) return null;
  // mirrors postdto.ArticleCover (internal/transport/postdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["media_id", "mime_type", "width", "height"])
  ) {
    throw invalidResponse(
      "article cover contains an unsupported or missing field",
    );
  }
  const mediaId = stringField(value, "media_id");
  if (!isCanonicalUlid(mediaId)) {
    throw invalidResponse("article cover names no managed media");
  }
  const mimeType = mediaMimeTypeField(value, "mime_type");
  if (!mimeType.startsWith("image/")) {
    throw invalidResponse("article cover must be a picture");
  }
  return {
    mediaId,
    mimeType,
    width: positiveIntegerField(value, "width"),
    height: positiveIntegerField(value, "height"),
  };
}

/**
 * The article half of a post, held to the kind it arrived with.
 *
 * `body`, `body_schema_version` and `content_html` are one fact in three
 * fields: a list read leaves all three behind, a detail read brings all three.
 * Accepting a body without the HTML rendered from it — or HTML with no body
 * behind it — would hand a page half an article and no way to say which half.
 */
function parseArticle(
  value: unknown,
  kind: PostKind,
  contentHtml: string,
): PublicArticle | null {
  if (kind === "moment") {
    if (value !== null) throw invalidResponse("a moment carries no article");
    return null;
  }
  // mirrors postdto.Article (internal/transport/postdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "title",
      "excerpt",
      "cover",
      "body",
      "body_schema_version",
      ...(Object.hasOwn(value, "body_media") ? ["body_media"] : []),
    ])
  ) {
    throw invalidResponse("article contains an unsupported or missing field");
  }
  const bodyMedia: PublicArticleCover[] = [];
  if (Object.hasOwn(value, "body_media")) {
    if (
      !Array.isArray(value.body_media) ||
      value.body_media.length > 50 ||
      value.body === null
    ) {
      throw invalidResponse(
        "article body media requires a document and at most 50 pictures",
      );
    }
    const seen = new Set<string>();
    for (const item of value.body_media) {
      const media = parseArticleCover(item);
      if (!media || seen.has(media.mediaId))
        throw invalidResponse("article body media is invalid");
      seen.add(media.mediaId);
      bodyMedia.push(media);
    }
  }
  const title = stringField(value, "title");
  if (!title.trim()) throw invalidResponse("an article must carry a title");
  const bodyPresent = value.body !== null;
  if (bodyPresent !== (contentHtml !== "")) {
    throw invalidResponse(
      "an article carries its body and its rendered content together",
    );
  }
  if (bodyPresent !== (value.body_schema_version !== null)) {
    throw invalidResponse(
      "an article carries its body and its schema version together",
    );
  }
  if (!bodyPresent) {
    return {
      title,
      excerpt: stringField(value, "excerpt"),
      cover: parseArticleCover(value.cover),
      body: null,
      bodySchemaVersion: null,
    };
  }
  const body = parseArticleDocument(value.body);
  const bodySchemaVersion = positiveIntegerField(value, "body_schema_version");
  if (body.schema_version !== bodySchemaVersion) {
    throw invalidResponse("article body names a different schema version");
  }
  return {
    title,
    excerpt: stringField(value, "excerpt"),
    cover: parseArticleCover(value.cover),
    body,
    bodySchemaVersion,
    bodyMedia,
  };
}

export function parsePost(value: unknown): PublicPost {
  if (!isRecord(value)) throw invalidResponse("post must be an object");
  const postKeys = [
    "id",
    "kind",
    "content_md",
    "article",
    "content_html",
    "created_at",
    "updated_at",
    "published_at",
    "reposted_at",
    "visibility",
    "timeline_item_kind",
    "comment_count",
    "media",
    "link_cards",
  ];
  if ("edited_at" in value && !postKeys.includes("edited_at"))
    postKeys.push("edited_at");
  if ("author" in value) postKeys.push("author");
  if ("map_card" in value) postKeys.push("map_card");
  // mirrors postdto.Post (internal/transport/postdto/dto.go)
  if (
    !hasExactKeys(value, postKeys) &&
    !hasExactKeys(value, [...postKeys, "location"]) &&
    !hasExactKeys(value, [...postKeys, "quoted_post"]) &&
    !hasExactKeys(value, [...postKeys, "location", "quoted_post"])
  ) {
    throw invalidResponse("post contains an unsupported or missing field");
  }
  const body = postBody(value, "post");
  const media = parseMediaUsages(value.media, mediaCeiling(body.kind), "post");
  const linkCards = parseLinkCards(value.link_cards);
  let location: PublicPostLocation | null = null;
  if (value.location !== undefined && value.location !== null) {
    // mirrors postdto.Location (internal/transport/postdto/dto.go)
    if (
      !isRecord(value.location) ||
      !hasExactKeys(value.location, ["name", "display_name"])
    ) {
      throw invalidResponse("post location is invalid");
    }
    const name = stringField(value.location, "name");
    const displayName = stringField(value.location, "display_name");
    if (!name.trim() || !displayName.trim()) {
      throw invalidResponse("post location cannot be empty");
    }
    location = { name, displayName };
  }
  let quotedPost: PublicQuotedPost | null = null;
  if (value.quoted_post !== undefined && value.quoted_post !== null) {
    const quoted = value.quoted_post;
    // mirrors postdto.QuotedPost (internal/transport/postdto/dto.go)
    if (
      !isRecord(quoted) ||
      !hasExactKeys(quoted, [
        ...(Object.hasOwn(quoted, "author") ? ["author"] : []),
        ...(Object.hasOwn(quoted, "comment_post_id")
          ? ["comment_post_id"]
          : []),
        ...(Object.hasOwn(quoted, "comment_author_name")
          ? ["comment_author_name"]
          : []),
        ...(Object.hasOwn(quoted, "map_card") ? ["map_card"] : []),
        "unavailable",
        "id",
        "kind",
        "article",
        "content_md",
        "content_html",
        "published_at",
        "media",
        "location",
      ])
    ) {
      throw invalidResponse("quoted post is invalid");
    }
    // Which branch a quote takes is decided by `unavailable` alone. A null
    // `content_md` cannot stand in for it any more: that is now what an
    // article looks like, and an article quote is perfectly available.
    const unavailable = booleanField(quoted, "unavailable");
    if (unavailable) {
      if (
        quoted.id !== null ||
        quoted.kind !== null ||
        quoted.article !== null ||
        quoted.content_md !== null ||
        quoted.content_html !== null ||
        quoted.published_at !== null ||
        quoted.media !== null ||
        quoted.location !== null ||
        quoted.map_card != null ||
        quoted.author != null ||
        quoted.comment_post_id != null ||
        quoted.comment_author_name != null
      ) {
        throw invalidResponse(
          "unavailable quoted post must not expose deleted target data",
        );
      }
      quotedPost = { unavailable: true };
    } else {
      if (quoted.media === null) {
        throw invalidResponse("available quoted post media must be an array");
      }
      let quotedLocation: PublicPostLocation | null = null;
      if (quoted.location !== null) {
        // mirrors postdto.Location (internal/transport/postdto/dto.go)
        if (
          !isRecord(quoted.location) ||
          !hasExactKeys(quoted.location, ["name", "display_name"])
        )
          throw invalidResponse("quoted post location is invalid");
        const quotedName = stringField(quoted.location, "name");
        const quotedDisplayName = stringField(quoted.location, "display_name");
        if (!quotedName.trim() || !quotedDisplayName.trim()) {
          throw invalidResponse("quoted post location cannot be empty");
        }
        quotedLocation = { name: quotedName, displayName: quotedDisplayName };
      }
      const quotedBody = postBody(quoted, "quoted post");
      const quotedContentHtml = stringField(quoted, "content_html");
      const quotedArticle = parseArticle(
        quoted.article,
        quotedBody.kind,
        quotedContentHtml,
      );
      if (quotedArticle !== null && quotedArticle.body !== null) {
        throw invalidResponse("a quote shows an article's card, not its body");
      }
      const commentPostId =
        quoted.comment_post_id === undefined
          ? undefined
          : stringField(quoted, "comment_post_id");
      if (
        commentPostId !== undefined &&
        (!isCanonicalUlid(commentPostId) ||
          quotedBody.kind !== "moment" ||
          quoted.map_card != null ||
          quotedArticle !== null)
      )
        throw invalidResponse("comment quote target is invalid");
      if (
        quoted.comment_author_name !== undefined &&
        (commentPostId === undefined || quoted.author != null)
      )
        throw invalidResponse("legacy quote author is invalid");
      const legacyName =
        quoted.comment_author_name === undefined
          ? undefined
          : stringField(quoted, "comment_author_name");
      if (legacyName !== undefined && !legacyName.trim())
        throw invalidResponse("legacy quote author is empty");
      quotedPost = {
        ...(commentPostId ? { commentPostId } : {}),
        author:
          legacyName === undefined
            ? quoted.author == null
              ? undefined
              : parseCommentAuthor(quoted.author)
            : {
                userId: null,
                username: null,
                displayName: legacyName,
                avatarMediaId: null,
                profileUrl: null,
              },
        unavailable: false,
        id: stringField(quoted, "id"),
        kind: quotedBody.kind,
        article: quotedArticle,
        contentMd: quotedBody.contentMd,
        contentHtml: quotedContentHtml,
        publishedAt: instantField(quoted, "published_at"),
        media: parseMediaUsages(
          quoted.media,
          mediaCeiling(quotedBody.kind),
          "quoted post",
        ),
        location: quotedLocation,
        ...(quoted.map_card != null
          ? { mapCard: parseMapCard(quoted.map_card) }
          : {}),
      };
    }
  }
  const editedAt =
    value.edited_at === undefined || value.edited_at === null
      ? null
      : instantField(value, "edited_at");
  if (
    editedAt !== null &&
    (Date.parse(editedAt) < Date.parse(instantField(value, "created_at")) ||
      Date.parse(editedAt) > Date.parse(instantField(value, "updated_at")))
  ) {
    throw invalidResponse("post edited_at is outside its lifetime");
  }
  const visibility = stringField(value, "visibility");
  if (!isPostVisibility(visibility)) {
    throw invalidResponse("post visibility is invalid");
  }
  const contentHtml = stringField(value, "content_html");
  return {
    ...(value.author !== undefined
      ? { author: parseCommentAuthor(value.author) }
      : {}),
    id: stringField(value, "id"),
    kind: body.kind,
    contentMd: body.contentMd,
    article: parseArticle(value.article, body.kind, contentHtml),
    contentHtml,
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
    editedAt,
    publishedAt: instantField(value, "published_at"),
    repostedAt:
      nullableStringField(value, "reposted_at") === null
        ? null
        : instantField(value, "reposted_at"),
    visibility,
    timelineItemKind: (() => {
      const kind = stringField(value, "timeline_item_kind");
      if (kind !== "original" && kind !== "repost") {
        throw invalidResponse("post timeline item kind is invalid");
      }
      if (kind === "repost" && value.reposted_at === null) {
        throw invalidResponse("repost timeline item requires reposted_at");
      }
      return kind;
    })(),
    commentCount: nonNegativeIntegerField(value, "comment_count"),
    media,
    linkCards,
    location,
    quotedPost,
    ...(value.map_card != null
      ? { mapCard: parseMapCard(value.map_card) }
      : {}),
  };
}

const linkCardKeys = [
  "position",
  "canonical_url",
  "card_kind",
  "title",
  "description",
  "site_name",
  "author_name",
  "duration_seconds",
  "view_count",
  "comment_count",
  "like_count",
  "favicon_media_id",
  "thumbnail_media_id",
  "thumbnail_mime_type",
  "thumbnail_width",
  "thumbnail_height",
  "embed_url",
];

function parseLinkCards(value: unknown): PublicLinkCard[] {
  if (!Array.isArray(value)) {
    throw invalidResponse("post link cards must be an array");
  }
  if (value.length > 1) {
    throw invalidResponse("a Moment carries at most one link card");
  }
  return value.map((item, index) => {
    // mirrors postdto.LinkCard (internal/transport/postdto/dto.go)
    if (!isRecord(item) || !hasExactKeys(item, linkCardKeys)) {
      throw invalidResponse(
        "link card contains an unsupported or missing field",
      );
    }
    const kind = stringField(item, "card_kind");
    if (kind !== "link" && kind !== "video") {
      throw invalidResponse("link card kind is invalid");
    }
    const canonicalUrl = stringField(item, "canonical_url");
    if (!isSafeExternalUrl(canonicalUrl)) {
      throw invalidResponse(
        "link card canonical URL is not a safe http(s) URL",
      );
    }
    const embedUrl =
      item.embed_url === null ? null : stringField(item, "embed_url");
    if (embedUrl !== null && !isSafeExternalUrl(embedUrl)) {
      throw invalidResponse("link card embed URL is not a safe http(s) URL");
    }
    if (nonNegativeIntegerField(item, "position") !== index) {
      throw invalidResponse("link card positions must be ordered from zero");
    }
    return {
      position: index,
      canonicalUrl,
      cardKind: kind,
      title: stringField(item, "title"),
      description: stringField(item, "description"),
      siteName: stringField(item, "site_name"),
      authorName: stringField(item, "author_name"),
      durationSeconds:
        item.duration_seconds === null
          ? null
          : positiveIntegerField(item, "duration_seconds"),
      viewCount:
        item.view_count === null
          ? null
          : nonNegativeIntegerField(item, "view_count"),
      commentCount:
        item.comment_count === null
          ? null
          : nonNegativeIntegerField(item, "comment_count"),
      likeCount:
        item.like_count === null
          ? null
          : nonNegativeIntegerField(item, "like_count"),
      faviconMediaId:
        item.favicon_media_id === null
          ? null
          : stringField(item, "favicon_media_id"),
      thumbnailMediaId:
        item.thumbnail_media_id === null
          ? null
          : stringField(item, "thumbnail_media_id"),
      thumbnailMimeType:
        item.thumbnail_mime_type === null
          ? null
          : stringField(item, "thumbnail_mime_type"),
      thumbnailWidth:
        item.thumbnail_width === null
          ? null
          : positiveIntegerField(item, "thumbnail_width"),
      thumbnailHeight:
        item.thumbnail_height === null
          ? null
          : positiveIntegerField(item, "thumbnail_height"),
      embedUrl,
    };
  });
}

/**
 * The server already constrains these URLs, but the client re-checks rather
 * than trusting a response body it did not generate. A card renders an href and
 * an iframe src; both must be plain http(s).
 */
function isSafeExternalUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
}

function nullableCursor(meta: Record<string, unknown>): string | null {
  const value = meta.next_cursor;
  if (value !== null && typeof value !== "string") {
    throw invalidResponse("next_cursor must be a string or null");
  }
  if (
    typeof value === "string" &&
    (value.length === 0 || value.length > 4_096)
  ) {
    throw invalidResponse("next_cursor must be a bounded opaque string");
  }
  return value;
}

function parseSearchHit(value: unknown): SearchHit {
  // searchdto.Hit embeds postdto.Post. Let the post parser own its exact
  // field set so a new post field cannot break every search result.
  if (!isRecord(value)) throw invalidResponse("search hit must be an object");
  const snippet = stringField(value, "snippet");
  if (!snippet.trim()) {
    throw invalidResponse("search snippet must contain visible text");
  }
  const postValue = { ...value };
  delete postValue.snippet;
  return { post: parsePost(postValue), snippet };
}

function parseYearCounts(value: unknown): SearchPage["yearCounts"] {
  if (!Array.isArray(value)) {
    throw invalidResponse("year_counts must be an array");
  }
  return value.map((item, index, items) => {
    // mirrors searchdto.YearCount (internal/transport/searchdto/dto.go)
    if (!isRecord(item) || !hasExactKeys(item, ["year", "count"])) {
      throw invalidResponse("year count must contain only year and count");
    }
    const year = positiveIntegerField(item, "year");
    const count = positiveIntegerField(item, "count");
    if (index > 0) {
      const previous = items[index - 1];
      if (
        !isRecord(previous) ||
        positiveIntegerField(previous, "year") <= year
      ) {
        throw invalidResponse(
          "year_counts must use reverse chronological order",
        );
      }
    }
    return { year, count };
  });
}

function parseArchiveMonths(value: unknown): ArchiveMonths {
  // mirrors locationdto.Results (internal/transport/locationdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["items"]) ||
    !Array.isArray(value.items)
  ) {
    throw invalidResponse("archive months must contain only an items array");
  }
  const items = value.items.map((item, index, values) => {
    // mirrors searchdto.MonthCount (internal/transport/searchdto/dto.go)
    if (!isRecord(item) || !hasExactKeys(item, ["year", "month", "count"])) {
      throw invalidResponse("archive month count is invalid");
    }
    const year = positiveIntegerField(item, "year");
    const month = positiveIntegerField(item, "month");
    const count = positiveIntegerField(item, "count");
    if (month > 12)
      throw invalidResponse("archive month must be between 1 and 12");
    if (index > 0) {
      const previous = values[index - 1];
      if (!isRecord(previous))
        throw invalidResponse("archive month count is invalid");
      const previousYear = positiveIntegerField(previous, "year");
      const previousMonth = positiveIntegerField(previous, "month");
      if (
        previousYear < year ||
        (previousYear === year && previousMonth <= month)
      ) {
        throw invalidResponse(
          "archive months must use reverse chronological order",
        );
      }
    }
    return { year, month, count };
  });
  return { items };
}

function parseArchiveMonth(
  value: unknown,
  expectedMonth: string,
): ArchiveMonth {
  // mirrors searchdto.MonthData (internal/transport/searchdto/dto.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["month", "days"]) ||
    !Array.isArray(value.days) ||
    stringField(value, "month") !== expectedMonth
  ) {
    throw invalidResponse(
      "archive calendar does not match the requested month",
    );
  }
  const [yearText, monthText] = expectedMonth.split("-");
  const daysInMonth = new Date(
    Date.UTC(Number(yearText), Number(monthText), 0),
  ).getUTCDate();
  const days = value.days.map((item, index, items) => {
    // mirrors searchdto.DayCount (internal/transport/searchdto/dto.go)
    if (!isRecord(item) || !hasExactKeys(item, ["day", "count"])) {
      throw invalidResponse("archive day count is invalid");
    }
    const day = positiveIntegerField(item, "day");
    const count = positiveIntegerField(item, "count");
    if (day > daysInMonth)
      throw invalidResponse("archive day is outside the requested month");
    if (index > 0) {
      const previous = items[index - 1];
      if (!isRecord(previous) || positiveIntegerField(previous, "day") >= day) {
        throw invalidResponse("archive days must use calendar order");
      }
    }
    return { day, count };
  });
  return { month: expectedMonth, days };
}

export async function decodeResponse(
  response: Response,
  options: {
    strictSuccessEnvelope?: boolean;
    requireJsonSuccess?: boolean;
  } = {},
): Promise<{ data: unknown; meta: Record<string, unknown> }> {
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw invalidResponse("server returned malformed JSON");
  }
  if (!isRecord(payload)) throw invalidResponse("response must be an object");

  if (!response.ok) {
    const error = isRecord(payload.error)
      ? (payload.error as ErrorPayload)
      : null;
    const code = typeof error?.code === "string" ? error.code : "HTTP_ERROR";
    const message =
      typeof error?.message === "string"
        ? error.message
        : "Moment request failed";
    const details = isRecord(error?.details) ? error.details : {};
    let retryAfterSeconds: number | null = null;
    if (code === "RATE_LIMITED") {
      const header = response.headers.get("Retry-After");
      const detail = details.retry_after_seconds;
      if (
        response.status !== 429 ||
        !header ||
        !/^\d+$/.test(header) ||
        Number(header) < 1 ||
        !Number.isSafeInteger(detail) ||
        Number(detail) !== Number(header)
      ) {
        throw invalidResponse(
          "rate limit response must contain matching Retry-After metadata",
        );
      }
      retryAfterSeconds = Number(header);
    }
    throw new PublicApiError(message, {
      code,
      status: response.status,
      requestId:
        typeof error?.request_id === "string" ? error.request_id : null,
      details,
      retryAfterSeconds,
    });
  }

  if (options.requireJsonSuccess) {
    const mediaType = response.headers
      .get("Content-Type")
      ?.split(";", 1)[0]
      ?.trim()
      .toLowerCase();
    if (mediaType !== "application/json") {
      throw invalidResponse("success response must use application/json");
    }
  }
  // mirrors ginhttp.successEnvelope (internal/transport/http/gin/response.go)
  if (
    options.strictSuccessEnvelope &&
    !hasExactKeys(payload, ["data", "meta"])
  ) {
    throw invalidResponse("success response must contain only data and meta");
  }
  if (!("data" in payload) || !isRecord(payload.meta)) {
    throw invalidResponse("success response must contain data and meta");
  }
  return { data: payload.data, meta: payload.meta };
}

async function decodeDiscoveryResponse(
  response: Response,
  metaKeys: string[],
): Promise<{ data: unknown; meta: Record<string, unknown> }> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: true,
    requireJsonSuccess: true,
  });
  if (response.status !== 200) {
    throw invalidResponse("discovery success response must return HTTP 200");
  }
  // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
  if (
    !hasExactKeys(result.meta, ["request_id", ...metaKeys]) ||
    !stringField(result.meta, "request_id")
  ) {
    throw invalidResponse("discovery success response metadata is invalid");
  }
  return result;
}

export async function decodeCommentResponse(
  response: Response,
  metaKeys: string[],
): Promise<{ data: unknown; meta: Record<string, unknown> }> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: true,
    requireJsonSuccess: true,
  });
  const expectedMetaKeys = ["request_id", ...metaKeys];
  // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
  if (!hasExactKeys(result.meta, expectedMetaKeys)) {
    throw invalidResponse("comment success response metadata is invalid");
  }
  if (!stringField(result.meta, "request_id")) {
    throw invalidResponse(
      "comment success response request_id cannot be empty",
    );
  }
  return result;
}

export function parseSiteIdentity(
  data: unknown,
  instanceId: string | null = null,
  instanceInitializedAt: string | null = null,
): SiteIdentity {
  if (!isRecord(data)) throw invalidResponse("site data must be an object");
  // mirrors app.SiteSettings (internal/app/site.go)
  if (
    !hasExactKeys(data, [
      "profile_user_id",
      "username",
      "display_name",
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "cover_media_id",
      "pinned_post_id",
      "comments_enabled",
    ])
  ) {
    throw invalidResponse("site data contains unsupported fields");
  }
  const commentsEnabled = data.comments_enabled;
  if (typeof commentsEnabled !== "boolean") {
    throw invalidResponse("response field comments_enabled must be a boolean");
  }
  const timezone = stringField(data, "timezone");
  const profileUserId = stringField(data, "profile_user_id");
  const username = stringField(data, "username");
  const displayName = stringField(data, "display_name");
  if (!/^[0-9a-f]{32}$/.test(profileUserId)) {
    throw invalidResponse(
      "response field profile_user_id must be a stable account ID",
    );
  }
  if (!/^[a-z][a-z0-9_]{2,29}$/.test(username)) {
    throw invalidResponse(
      "response field username must be a canonical username",
    );
  }
  if (
    !displayName.trim() ||
    displayName.trim() !== displayName ||
    Array.from(displayName).length > MAX_DISPLAY_NAME_CODE_POINTS
  ) {
    throw invalidResponse("response field display_name must be a display name");
  }
  try {
    new Intl.DateTimeFormat("zh-CN", { timeZone: timezone }).format();
  } catch {
    throw invalidResponse("response field timezone must be an IANA timezone");
  }
  const avatarMediaId = nullableStringField(data, "avatar_media_id");
  if (avatarMediaId !== null && !isCanonicalUlid(avatarMediaId)) {
    throw invalidResponse("response field avatar_media_id must be a media ID");
  }
  const coverMediaId = nullableStringField(data, "cover_media_id");
  if (coverMediaId !== null && !isCanonicalUlid(coverMediaId)) {
    throw invalidResponse("response field cover_media_id must be a media ID");
  }
  const pinnedPostId = nullableStringField(data, "pinned_post_id");
  if (pinnedPostId !== null && !isCanonicalUlid(pinnedPostId)) {
    throw invalidResponse("response field pinned_post_id must be a post ID");
  }
  return {
    profileUserId,
    username,
    displayName,
    title: stringField(data, "title"),
    bio: stringField(data, "bio"),
    timezone,
    publicBaseUrl: nullableStringField(data, "public_base_url"),
    avatarMediaId,
    coverMediaId,
    pinnedPostId,
    commentsEnabled,
    instanceId,
    instanceInitializedAt,
  };
}

export type AccountRole = "admin" | "member";
export type AccountStatus = "pending" | "active" | "suspended";

export interface PublicAccount {
  userId: string;
  username: string;
  displayName: string;
  bio: string;
  avatarMediaId: string | null;
  coverMediaId: string | null;
  profileUrl: string;
  joinedAt?: string;
}
export interface Account extends PublicAccount {
  role: AccountRole;
  status: AccountStatus;
  usernameChosen: boolean;
}

function publicAccountFields(value: Record<string, unknown>): PublicAccount {
  const userId = stringField(value, "user_id");
  const username = stringField(value, "username");
  const displayName = stringField(value, "display_name");
  const profileUrl = stringField(value, "profile_url");
  if (
    !/^[0-9a-f]{32}$/.test(userId) ||
    !/^[a-z][a-z0-9_]{2,29}$/.test(username) ||
    !displayName.trim() ||
    Array.from(displayName).length > MAX_DISPLAY_NAME_CODE_POINTS ||
    profileUrl !== `/${username}`
  ) {
    throw invalidResponse("account identity is invalid");
  }
  const avatarMediaId = nullableStringField(value, "avatar_media_id");
  const coverMediaId = nullableStringField(value, "cover_media_id");
  for (const id of [avatarMediaId, coverMediaId])
    if (id !== null && !isCanonicalUlid(id))
      throw invalidResponse("account picture must name managed media");
  return {
    userId,
    username,
    displayName,
    profileUrl,
    bio: stringField(value, "bio"),
    avatarMediaId,
    coverMediaId,
    ...(value.joined_at === undefined
      ? {}
      : { joinedAt: instantField(value, "joined_at") }),
  };
}
const publicAccountOptionalKeys = ["joined_at"];
const publicAccountKeys = [
  "user_id",
  "username",
  "display_name",
  "bio",
  "avatar_media_id",
  "cover_media_id",
  "profile_url",
];
export function parseAccount(value: unknown): PublicAccount {
  if (
    !isRecord(value) ||
    !hasKeys(value, publicAccountKeys, publicAccountOptionalKeys)
  )
    throw invalidResponse("public account contains unsupported fields");
  return publicAccountFields(value);
}
export function parsePrivateAccount(value: unknown): Account {
  if (
    !isRecord(value) ||
    !hasKeys(
      value,
      [...publicAccountKeys, "role", "status", "username_chosen"],
      publicAccountOptionalKeys,
    )
  )
    throw invalidResponse("private account is incomplete");
  const role = value.role,
    status = value.status;
  if (
    (role !== "admin" && role !== "member") ||
    (status !== "pending" && status !== "active" && status !== "suspended")
  )
    throw invalidResponse("account role or status is invalid");
  return {
    ...publicAccountFields(value),
    role,
    status,
    usernameChosen: booleanField(value, "username_chosen"),
  };
}

export class PublicApi {
  private readonly fetcher: Fetcher;
  private readonly authorUserId: string | undefined;
  private readonly postKind: PostKind | undefined;

  constructor(
    fetcher: Fetcher = (input, init) => window.fetch(input, init),
    authorUserId?: string,
    postKind?: PostKind,
    private readonly ownArchive = false,
  ) {
    this.fetcher = fetcher;
    this.authorUserId = authorUserId;
    this.postKind = postKind;
  }

  forAuthor(userId: string, kind?: PostKind): PublicApi {
    return new PublicApi(this.fetcher, userId, kind);
  }

  async site(signal?: AbortSignal): Promise<SiteIdentity> {
    const response = await this.fetcher("/api/v1/site", {
      headers: { Accept: "application/json" },
      signal,
    });
    const { data, meta } = await decodeResponse(response);
    const instanceId = meta.instance_id;
    const instanceInitializedAt = meta.instance_initialized_at;
    if (
      instanceId !== undefined &&
      (typeof instanceId !== "string" || !/^[0-9a-f]{32}$/.test(instanceId))
    ) {
      throw invalidResponse("meta instance_id must be a runtime instance ID");
    }
    if (
      instanceInitializedAt !== undefined &&
      (typeof instanceInitializedAt !== "string" ||
        instantField(
          { instance_initialized_at: instanceInitializedAt },
          "instance_initialized_at",
        ) !== instanceInitializedAt)
    ) {
      throw invalidResponse(
        "meta instance_initialized_at must be a UTC instant",
      );
    }
    if (instanceInitializedAt !== undefined && instanceId === undefined) {
      throw invalidResponse(
        "meta instance_initialized_at requires instance_id",
      );
    }
    return parseSiteIdentity(
      data,
      instanceId ?? null,
      instanceInitializedAt ?? null,
    );
  }

  /**
   * One account by handle.
   *
   * Public on purpose: a signed-out reader sees the same profile a signed-in
   * one does. An account that is not active reads as absent rather than as
   * hidden, so the server answers ACCOUNT_NOT_FOUND for both.
   */
  async user(username: string, signal?: AbortSignal): Promise<PublicAccount> {
    const response = await this.fetcher(
      `/api/v1/users/${encodeURIComponent(username)}`,
      { headers: { Accept: "application/json" }, signal },
    );
    const { data } = await decodeResponse(response);
    return parseAccount(data);
  }

  async mentionSuggestions(
    query: string,
    signal?: AbortSignal,
  ): Promise<MentionSuggestion[]> {
    const params = new URLSearchParams({ q: query, limit: "8" });
    const response = await this.fetcher(
      `/api/v1/users/mention-suggestions?${params}`,
      { headers: { Accept: "application/json" }, signal },
    );
    const { data } = await decodeResponse(response);
    if (!Array.isArray(data))
      throw invalidResponse("mention suggestions must be an array");
    return data.map((item) => {
      // mirrors app.MentionSuggestion (internal/app/site.go)
      if (
        !isRecord(item) ||
        !hasExactKeys(item, [
          "user_id",
          "username",
          "display_name",
          "avatar_media_id",
          "profile_url",
        ])
      ) {
        throw invalidResponse("mention suggestion contains unsupported fields");
      }
      const userId = stringField(item, "user_id");
      const username = stringField(item, "username");
      const displayName = stringField(item, "display_name");
      const avatarMediaId = nullableStringField(item, "avatar_media_id");
      const profileUrl = stringField(item, "profile_url");
      if (
        !/^[0-9a-f]{32}$/.test(userId) ||
        !/^[a-z][a-z0-9_]{2,29}$/.test(username) ||
        !displayName ||
        profileUrl !== `/${username}` ||
        (avatarMediaId !== null && !isCanonicalUlid(avatarMediaId))
      ) {
        throw invalidResponse("mention suggestion identity is invalid");
      }
      return { userId, username, displayName, avatarMediaId, profileUrl };
    });
  }

  forArchive(): PublicApi {
    return new PublicApi(this.fetcher, undefined, undefined, true);
  }

  async timeline(request: TimelineRequest = {}): Promise<TimelinePage> {
    const query = new URLSearchParams();
    query.set("limit", String(request.limit ?? 20));
    const author = request.authorUserId ?? this.authorUserId;
    if (author) query.set("author_user_id", author);
    if (request.cursor) query.set("cursor", request.cursor);
    if (request.from) query.set("from", request.from);
    if (request.to) query.set("to", request.to);
    if (request.anchor) query.set("anchor", request.anchor);
    // An empty value is a client that meant to send a kind and did not, which
    // the server rejects; leaving the parameter off is how both kinds are read.
    const kind = request.kind ?? this.postKind;
    if (kind) query.set("kind", kind);
    const response = await this.fetcher(
      `${this.ownArchive ? "/api/v1/archive/posts" : "/api/v1/posts"}?${query}`,
      {
        headers: { Accept: "application/json" },
        signal: request.signal,
      },
    );
    const { data, meta } = await decodeResponse(response);
    if (!isRecord(data) || !Array.isArray(data.items)) {
      throw invalidResponse("timeline data must contain an items array");
    }
    const nextCursor = meta.next_cursor;
    if (nextCursor !== null && typeof nextCursor !== "string") {
      throw invalidResponse("next_cursor must be a string or null");
    }
    // Absent from responses recorded before pages could read newer; the web
    // and the server ship together, so a live server always sends it.
    const prevCursor = meta.prev_cursor ?? null;
    if (prevCursor !== null && typeof prevCursor !== "string") {
      throw invalidResponse("prev_cursor must be a string or null");
    }
    const refreshCursor = meta.refresh_cursor ?? null;
    if (refreshCursor !== null && typeof refreshCursor !== "string") {
      throw invalidResponse("refresh_cursor must be a string or null");
    }
    const items = data.items.map(parsePost);
    const authorPostCount =
      data.author_post_count === undefined
        ? undefined
        : nonNegativeIntegerField(data, "author_post_count");
    const timelineKeys = new Set<string>();
    for (const item of items) {
      const key = `${item.id}:${item.timelineItemKind}`;
      if (timelineKeys.has(key)) {
        throw invalidResponse("timeline page contains a duplicate item");
      }
      timelineKeys.add(key);
    }
    return {
      items,
      nextCursor,
      prevCursor,
      refreshCursor,
      ...(authorPostCount === undefined ? {} : { authorPostCount }),
    };
  }

  async search(request: SearchRequest): Promise<SearchPage> {
    const query = new URLSearchParams({
      q: request.query,
      limit: String(request.limit ?? 20),
    });
    if (request.cursor) query.set("cursor", request.cursor);
    if (request.from) query.set("from", request.from);
    if (request.to) query.set("to", request.to);
    const response = await this.fetcher(`/api/v1/search?${query}`, {
      headers: { Accept: "application/json" },
      signal: request.signal,
    });
    const { data, meta } = await decodeDiscoveryResponse(response, [
      "next_cursor",
    ]);
    // mirrors searchdto.SearchData (internal/transport/searchdto/dto.go)
    if (
      !isRecord(data) ||
      !hasExactKeys(data, ["items", "year_counts"]) ||
      !Array.isArray(data.items)
    ) {
      throw invalidResponse("search data must contain items and year_counts");
    }
    const items = data.items.map(parseSearchHit);
    const ids = new Set<string>();
    for (const item of items) {
      if (ids.has(item.post.id))
        throw invalidResponse("search page contains a duplicate post");
      ids.add(item.post.id);
    }
    return {
      items,
      yearCounts: parseYearCounts(data.year_counts),
      nextCursor: nullableCursor(meta),
    };
  }

  async archiveMonths(
    request: {
      from?: string | null;
      to?: string | null;
      signal?: AbortSignal;
    } = {},
  ): Promise<ArchiveMonths> {
    const query = new URLSearchParams();
    if (request.from) query.set("from", request.from);
    if (request.to) query.set("to", request.to);
    const suffix = query.size > 0 ? `?${query}` : "";
    const response = await this.fetcher(`/api/v1/archive/months${suffix}`, {
      headers: { Accept: "application/json" },
      signal: request.signal,
    });
    const { data } = await decodeDiscoveryResponse(response, []);
    return parseArchiveMonths(data);
  }

  async archiveMonth(
    month: string,
    signal?: AbortSignal,
  ): Promise<ArchiveMonth> {
    const query = new URLSearchParams({ month });
    const response = await this.fetcher(`/api/v1/archive/month?${query}`, {
      headers: { Accept: "application/json" },
      signal,
    });
    const { data } = await decodeDiscoveryResponse(response, []);
    return parseArchiveMonth(data, month);
  }

  async post(id: string, signal?: AbortSignal): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(id)}`,
      { headers: { Accept: "application/json" }, signal },
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async comments(
    postId: string,
    options: {
      cursor?: string | null;
      signal?: AbortSignal;
      sort?: CommentSort;
    } = {},
  ): Promise<CommentThread> {
    const params = new URLSearchParams();
    if (options.cursor) params.set("cursor", options.cursor);
    if (options.sort && options.sort !== "oldest")
      params.set("sort", options.sort);
    const query = params.size ? `?${params}` : "";
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments${query}`,
      { headers: { Accept: "application/json" }, signal: options.signal },
    );
    const { data, meta } = await decodeCommentResponse(response, [
      "next_cursor",
    ]);
    if (response.status !== 200) {
      throw invalidResponse("comment thread must return HTTP 200");
    }
    return parseCommentThread(data, postId, nullableCursor(meta), options.sort);
  }

  async commentReplies(
    postId: string,
    rootId: string,
    options: {
      cursor?: string | null;
      signal?: AbortSignal;
      scope?: CommentReplyScope;
      sort?: CommentSort;
    } = {},
  ): Promise<CommentReplyPage> {
    const params = new URLSearchParams();
    if (options.cursor) params.set("cursor", options.cursor);
    if (options.scope) params.set("scope", options.scope);
    if (options.sort && options.sort !== "oldest")
      params.set("sort", options.sort);
    const query = params.size ? `?${params}` : "";
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments/${encodeURIComponent(rootId)}/replies${query}`,
      { headers: { Accept: "application/json" }, signal: options.signal },
    );
    const { data, meta } = await decodeCommentResponse(response, [
      "next_cursor",
    ]);
    if (response.status !== 200) {
      throw invalidResponse("comment replies must return HTTP 200");
    }
    return parseCommentReplyPage(
      data,
      postId,
      rootId,
      nullableCursor(meta),
      options.scope,
      options.sort,
    );
  }

  async commentContext(
    postId: string,
    commentId: string,
    signal?: AbortSignal,
  ): Promise<CommentContext> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments/${encodeURIComponent(commentId)}`,
      { headers: { Accept: "application/json" }, signal },
    );
    const { data } = await decodeResponse(response);
    if (
      response.status !== 200 ||
      !isRecord(data) ||
      !hasExactKeys(data, [
        "comment",
        "reply_target",
        "root_comment_id",
        "pinned_comment_id",
        "direct_reply_count",
      ])
    )
      throw invalidResponse("comment context is invalid");
    const comment = parseComment(data.comment, postId);
    const rootCommentId = stringField(data, "root_comment_id");
    const pinnedCommentId = nullableStringField(data, "pinned_comment_id");
    if (pinnedCommentId !== null && !isCanonicalUlid(pinnedCommentId))
      throw invalidResponse("comment context pin is invalid");
    if (
      comment.id !== commentId ||
      rootCommentId !== (comment.parentId ?? comment.id)
    )
      throw invalidResponse("comment context belongs to another conversation");
    const replyTarget =
      data.reply_target === null
        ? null
        : parseComment(data.reply_target, postId);
    if (
      replyTarget &&
      (replyTarget.id !== comment.replyTo?.commentId ||
        (replyTarget.id !== rootCommentId &&
          replyTarget.parentId !== rootCommentId))
    )
      throw invalidResponse("comment context reply target is invalid");
    return {
      comment,
      replyTarget,
      rootCommentId,
      pinnedCommentId,
      directReplyCount: nonNegativeIntegerField(data, "direct_reply_count"),
    };
  }
}

export function uploadMediaXHR(
  endpoint: string,
  file: File,
  onProgress: (percent: number) => void = () => undefined,
  signal?: AbortSignal,
  headers: Record<string, string> = {},
  onProcessingPoster?: (objectUrl: string) => void,
): Promise<UploadedMedia> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const resolveOnce = (value: UploadedMedia) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    const rejectOnce = (reason: unknown) => {
      if (settled) return;
      settled = true;
      reject(reason);
    };
    const request = new XMLHttpRequest();
    const uploadID = onProcessingPoster
      ? typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) =>
            value.toString(16).padStart(2, "0"),
          ).join("")
      : null;
    let posterPoll: ReturnType<typeof setTimeout> | null = null;
    let posterPolling = false;
    const stopPosterPoll = () => {
      posterPolling = false;
      if (posterPoll !== null) clearTimeout(posterPoll);
      posterPoll = null;
    };
    const pollPoster = async () => {
      if (!posterPolling || !uploadID || signal?.aborted) return;
      try {
        const response = await fetch(
          `/api/v1/media/uploads/${encodeURIComponent(uploadID)}/poster`,
          { credentials: "same-origin", headers: { Accept: "image/jpeg" } },
        );
        if (
          response.ok &&
          response.headers.get("Content-Type") === "image/jpeg"
        ) {
          const objectUrl = URL.createObjectURL(await response.blob());
          stopPosterPoll();
          onProcessingPoster?.(objectUrl);
          return;
        }
      } catch {
        // The primary upload owns error reporting. Poster feedback is best-effort.
      }
      if (posterPolling) posterPoll = setTimeout(() => void pollPoster(), 300);
    };
    const startPosterPoll = () => {
      if (posterPolling || !uploadID) return;
      posterPolling = true;
      void pollPoster();
    };
    request.open("POST", endpoint);
    request.withCredentials = true;
    request.setRequestHeader("Accept", "application/json");
    for (const [name, value] of Object.entries(headers))
      request.setRequestHeader(name, value);
    if (uploadID) request.setRequestHeader("X-Moment-Upload-ID", uploadID);
    request.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable && event.total > 0) {
        const progress = Math.min(
          99,
          Math.round((event.loaded / event.total) * 100),
        );
        onProgress(progress);
        if (progress === 99) startPosterPoll();
      }
    });
    request.addEventListener("error", () =>
      rejectOnce(
        new PublicApiError("media upload failed", {
          code: "NETWORK_ERROR",
          status: 0,
        }),
      ),
    );
    request.addEventListener("abort", () =>
      rejectOnce(
        new PublicApiError("media upload was cancelled", {
          code: "CANCELLED",
          status: 0,
        }),
      ),
    );
    request.addEventListener("load", () => {
      const responseHeaders = new Headers();
      const contentType = request.getResponseHeader("Content-Type");
      if (contentType) responseHeaders.set("Content-Type", contentType);
      const response = new Response(request.responseText, {
        status: request.status,
        headers: responseHeaders,
      });
      void decodeResponse(response, {
        strictSuccessEnvelope: request.status === 201,
        requireJsonSuccess: request.status === 201,
      })
        .then(({ data, meta }) => {
          // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
          if (
            request.status !== 201 ||
            !hasExactKeys(meta, ["request_id"]) ||
            !stringField(meta, "request_id")
          ) {
            throw invalidResponse("media upload metadata is invalid");
          }
          const uploaded = parseUploadedMedia(data);
          onProgress(100);
          resolveOnce(uploaded);
        })
        .catch(rejectOnce);
    });
    const abort = () => request.abort();
    if (signal?.aborted) {
      rejectOnce(
        new PublicApiError("media upload was cancelled", {
          code: "CANCELLED",
          status: 0,
        }),
      );
      return;
    }
    signal?.addEventListener("abort", abort, { once: true });
    request.addEventListener(
      "loadend",
      () => (signal?.removeEventListener("abort", abort), stopPosterPoll()),
    );
    const body = new FormData();
    body.append("file", file, file.name);
    onProgress(0);
    request.send(body);
  });
}

function parseUploadedMedia(data: unknown): UploadedMedia {
  // mirrors media.UploadResult (internal/media/types.go)
  if (
    !isRecord(data) ||
    !hasExactKeys(data, ["media", "preview", "original_url", "preview_url"]) ||
    !isRecord(data.media) ||
    !isRecord(data.preview)
  ) {
    throw invalidResponse("upload result contains unsupported fields");
  }
  const id = stringField(data.media, "id");
  if (!isCanonicalUlid(id))
    throw invalidResponse("uploaded media id is invalid");
  const originalUrl = stringField(data, "original_url");
  const previewUrl = stringField(data, "preview_url");
  if (
    originalUrl !== mediaOriginalUrl(id) ||
    previewUrl !== mediaPreviewUrl(id)
  )
    throw invalidResponse("upload result contains an uncontrolled media URL");
  const animatedOriginal = booleanField(data.media, "animated_original");
  if (booleanField(data.preview, "animated_original") !== animatedOriginal)
    throw invalidResponse("upload preview animation metadata is inconsistent");
  return {
    id,
    byteSize: positiveIntegerField(data.media, "byte_size"),
    width: positiveIntegerField(data.media, "width"),
    height: positiveIntegerField(data.media, "height"),
    mimeType: mediaMimeTypeField(data.media, "mime_type"),
    previewMimeType: mediaPreviewMimeTypeField(data.preview, "mime_type"),
    animatedOriginal,
    previewUrl,
    originalUrl,
  };
}

function mediaMimeTypeField(
  record: Record<string, unknown>,
  key: string,
): string {
  const value = stringField(record, key);
  if (
    ![
      "image/jpeg",
      "image/png",
      "image/webp",
      "image/gif",
      "image/heic",
      "video/mp4",
      "video/quicktime",
    ].includes(value)
  ) {
    throw invalidResponse(`${key} is not a supported media MIME type`);
  }
  return value;
}

function mediaPreviewMimeTypeField(
  record: Record<string, unknown>,
  key: string,
): string {
  const value = stringField(record, key);
  if (!["image/jpeg", "image/png", "video/mp4"].includes(value)) {
    throw invalidResponse(`${key} is not a supported preview MIME type`);
  }
  return value;
}

export async function decodeCreatedComment(
  response: Response,
  postId: string,
  expected?: {
    parentId: string | null;
  },
): Promise<CreatedComment> {
  const { data, meta } = await decodeCommentResponse(response, ["replayed"]);
  const replayed = booleanField(meta, "replayed");
  if (
    (response.status !== 201 && response.status !== 200) ||
    (response.status === 201 && replayed) ||
    (response.status === 200 && !replayed)
  ) {
    throw invalidResponse(
      "comment create status must match the replayed response metadata",
    );
  }
  const comment = parseComment(data, postId);
  if (expected && comment.parentId !== expected.parentId) {
    throw invalidResponse(
      "created comment does not match the submitted identity or parent",
    );
  }
  return { comment, replayed };
}

export function parseDeletedCommentMeta(meta: Record<string, unknown>): {
  deleted: boolean;
  dryRun: boolean;
  repliesAffected: number;
} {
  return {
    deleted: booleanField(meta, "deleted"),
    dryRun: booleanField(meta, "dry_run"),
    repliesAffected: nonNegativeIntegerField(meta, "replies_affected"),
  };
}
