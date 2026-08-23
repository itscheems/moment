import { mediaOriginalUrl, mediaPreviewUrl } from "./mediaUrls";

export interface SiteIdentity {
  ownerId: string;
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
  ownerId: string;
  username: string;
  displayName: string;
  avatarMediaId: string | null;
  profileUrl: string;
}

export interface PublicPost {
  id: string;
  contentMd: string;
  contentHtml: string;
  createdAt: string;
  updatedAt: string;
  publishedAt: string;
  repostedAt: string | null;
  timelineItemKind: "original" | "repost";
  commentCount: number;
  media: PublicMediaUsage[];
  linkCards: PublicLinkCard[];
  location?: PublicPostLocation | null;
  quotedPost?: PublicQuotedPost | null;
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
  unavailable: false;
  id: string;
  contentMd: string;
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
  nextCursor: string | null;
}

export interface TimelineRequest {
  cursor?: string | null;
  from?: string | null;
  to?: string | null;
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
   * `"guest"` is a legacy read-only kind. Nothing produces it any more — the
   * anonymous write path is gone — but rows carrying it still arrive from
   * archives exported before commenting required an account, and importing
   * those archives has to keep working for a product whose promise is that the
   * data is yours. So the accept set stays wider than the produce set, here and
   * in every renderer downstream.
   */
  authorKind: "owner" | "guest";
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
        "author_kind",
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
  const authorKind = stringField(value, "author_kind");
  // Narrowing this to `"owner"` would make every archive taken before accounts
  // were required undecodable. See PublicComment.authorKind.
  if (authorKind !== "owner" && authorKind !== "guest") {
    throw invalidResponse("comment author_kind is invalid");
  }
  const authorName = commentTextField(value, "author_name", 40);
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
  if (parentId !== null && (replyCount !== 0 || repliesNextCursor !== null)) {
    throw invalidResponse("a reply cannot carry reply pagination");
  }
  return {
    id,
    postId,
    parentId,
    depth: depth as 0 | 1,
    authorKind,
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
  if (linked !== 0 && linked !== 3) {
    throw invalidResponse("comment author identity is partially resolved");
  }
  if (username !== null && profileUrl !== `/${username}`) {
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
      if (previousRoot && commentOrder(previousRoot, comment) >= 0) {
        throw invalidResponse("comment roots are not in stable order");
      }
      currentRoot = comment.id;
      previousRoot = comment;
      previousReply = null;
    } else {
      if (comment.parentId !== currentRoot) {
        throw invalidResponse("comment reply is not adjacent to its root");
      }
      if (previousReply && commentOrder(previousReply, comment) >= 0) {
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
    if (comment.parentId !== rootId) {
      throw invalidResponse("comment reply page mixes conversations");
    }
    if (previous && commentOrder(previous, comment) >= 0) {
      throw invalidResponse("comment replies are not in stable order");
    }
    previous = comment;
  }
  return { items, nextCursor };
}

export function parsePost(value: unknown): PublicPost {
  if (!isRecord(value)) throw invalidResponse("post must be an object");
  const postKeys = [
    "id",
    "content_md",
    "content_html",
    "created_at",
    "updated_at",
    "published_at",
    "reposted_at",
    "timeline_item_kind",
    "comment_count",
    "media",
    "link_cards",
  ];
  // mirrors postdto.Post (internal/transport/postdto/dto.go)
  // mirrors postdto.Post (internal/transport/postdto/dto.go)
  if (
    (!hasExactKeys(value, postKeys) &&
      !hasExactKeys(value, [...postKeys, "location"]) &&
      !hasExactKeys(value, [...postKeys, "quoted_post"]) &&
      !hasExactKeys(value, [...postKeys, "location", "quoted_post"])) ||
    !Array.isArray(value.media) ||
    value.media.length > 9
  ) {
    throw invalidResponse("post contains an unsupported or missing field");
  }
  const media = parseMediaUsages(value.media, 9, "post");
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
        "unavailable",
        "id",
        "content_md",
        "content_html",
        "published_at",
        "media",
        "location",
      ])
    ) {
      throw invalidResponse("quoted post is invalid");
    }
    const unavailable = booleanField(quoted, "unavailable");
    if (unavailable) {
      if (
        quoted.id !== null ||
        quoted.content_md !== null ||
        quoted.content_html !== null ||
        quoted.published_at !== null ||
        quoted.media !== null ||
        quoted.location !== null
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
      quotedPost = {
        unavailable: false,
        id: stringField(quoted, "id"),
        contentMd: stringField(quoted, "content_md"),
        contentHtml: stringField(quoted, "content_html"),
        publishedAt: instantField(quoted, "published_at"),
        media: parseMediaUsages(quoted.media, 9, "quoted post"),
        location: quotedLocation,
      };
    }
  }
  return {
    id: stringField(value, "id"),
    contentMd: stringField(value, "content_md"),
    contentHtml: stringField(value, "content_html"),
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
    publishedAt: instantField(value, "published_at"),
    repostedAt:
      nullableStringField(value, "reposted_at") === null
        ? null
        : instantField(value, "reposted_at"),
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
  const searchKeys = [
    "id",
    "content_md",
    "content_html",
    "created_at",
    "updated_at",
    "published_at",
    "reposted_at",
    "timeline_item_kind",
    "comment_count",
    "media",
    "link_cards",
    "snippet",
  ];
  // mirrors searchdto.Hit (internal/transport/searchdto/dto.go), which embeds postdto.Post
  // mirrors searchdto.Hit (internal/transport/searchdto/dto.go), which embeds postdto.Post
  // mirrors searchdto.Hit (internal/transport/searchdto/dto.go), which embeds postdto.Post
  if (
    !isRecord(value) ||
    (!hasExactKeys(value, searchKeys) &&
      !hasExactKeys(value, [...searchKeys, "location"]) &&
      !hasExactKeys(value, [...searchKeys, "quoted_post"]) &&
      !hasExactKeys(value, [...searchKeys, "location", "quoted_post"]))
  ) {
    throw invalidResponse(
      "search hit contains an unsupported or missing field",
    );
  }
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
      "owner_id",
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
  const ownerId = stringField(data, "owner_id");
  const username = stringField(data, "username");
  const displayName = stringField(data, "display_name");
  if (!/^[0-9a-f]{32}$/.test(ownerId)) {
    throw invalidResponse("response field owner_id must be a stable owner ID");
  }
  if (!/^[a-z][a-z0-9_]{2,29}$/.test(username)) {
    throw invalidResponse(
      "response field username must be a canonical username",
    );
  }
  if (
    !displayName.trim() ||
    displayName.trim() !== displayName ||
    Array.from(displayName).length > 80
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
    ownerId,
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

export type AccountRole = "owner" | "member";
export type AccountStatus = "pending" | "active" | "suspended";

/**
 * One person on this instance.
 *
 * `status` and `usernameChosen` are moderation and onboarding facts that only
 * the account itself and the site owner are allowed to read, so the public
 * projection leaves them out. Null therefore means "this endpoint does not
 * disclose it", never "the server does not know".
 */
export interface Account {
  userId: string;
  username: string;
  displayName: string;
  bio: string;
  avatarMediaId: string | null;
  coverMediaId: string | null;
  role: AccountRole;
  profileUrl: string;
  status: AccountStatus | null;
  usernameChosen: boolean | null;
}

const accountRoles = new Set(["owner", "member"]);
const accountStatuses = new Set(["pending", "active", "suspended"]);

export function parseAccount(value: unknown): Account {
  if (!isRecord(value)) throw invalidResponse("account must be an object");
  if (
    !hasKeys(
      value,
      [
        "user_id",
        "username",
        "display_name",
        "bio",
        "avatar_media_id",
        "cover_media_id",
        "role",
        "profile_url",
      ],
      ["status", "username_chosen"],
    )
  ) {
    throw invalidResponse("account contains unsupported fields");
  }
  const userId = stringField(value, "user_id");
  const username = stringField(value, "username");
  const displayName = stringField(value, "display_name");
  const role = stringField(value, "role");
  const profileUrl = stringField(value, "profile_url");
  if (!/^[0-9a-f]{32}$/.test(userId)) {
    throw invalidResponse("response field user_id must be an account ID");
  }
  if (!/^[a-z][a-z0-9_]{2,29}$/.test(username)) {
    throw invalidResponse(
      "response field username must be a canonical username",
    );
  }
  if (!displayName.trim() || Array.from(displayName).length > 80) {
    throw invalidResponse("response field display_name must be a display name");
  }
  if (!accountRoles.has(role)) {
    throw invalidResponse("response field role must be an account role");
  }
  // The handle is the profile URL. Keeping that identity checked here is what
  // lets a renderer link an account without re-deriving the path itself.
  if (profileUrl !== `/${username}`) {
    throw invalidResponse("response field profile_url must address the handle");
  }
  const avatarMediaId = nullableStringField(value, "avatar_media_id");
  if (avatarMediaId !== null && !isCanonicalUlid(avatarMediaId)) {
    throw invalidResponse("response field avatar_media_id must be a media ID");
  }
  const coverMediaId = nullableStringField(value, "cover_media_id");
  if (coverMediaId !== null && !isCanonicalUlid(coverMediaId)) {
    throw invalidResponse("response field cover_media_id must be a media ID");
  }
  const status =
    value.status === undefined ? null : stringField(value, "status");
  if (status !== null && !accountStatuses.has(status)) {
    throw invalidResponse("response field status must be an account status");
  }
  return {
    userId,
    username,
    displayName,
    bio: stringField(value, "bio"),
    avatarMediaId,
    coverMediaId,
    role: role as AccountRole,
    profileUrl,
    status: status as AccountStatus | null,
    usernameChosen:
      value.username_chosen === undefined
        ? null
        : booleanField(value, "username_chosen"),
  };
}

/**
 * The same account, read from an endpoint that is allowed to disclose the
 * private fields: the account's own session, and the owner's roster.
 *
 * Only the public projection may leave `status` and `usernameChosen` out, so
 * their absence anywhere else is a malformed response rather than discretion.
 */
export function parsePrivateAccount(value: unknown): Account {
  const account = parseAccount(value);
  if (account.status === null || account.usernameChosen === null) {
    throw invalidResponse("account view must disclose status and onboarding");
  }
  return account;
}

export class PublicApi {
  private readonly fetcher: Fetcher;

  constructor(fetcher: Fetcher = window.fetch.bind(window)) {
    this.fetcher = fetcher;
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
  async user(username: string, signal?: AbortSignal): Promise<Account> {
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
          "owner_id",
          "username",
          "display_name",
          "avatar_media_id",
          "profile_url",
        ])
      ) {
        throw invalidResponse("mention suggestion contains unsupported fields");
      }
      const ownerId = stringField(item, "owner_id");
      const username = stringField(item, "username");
      const displayName = stringField(item, "display_name");
      const avatarMediaId = nullableStringField(item, "avatar_media_id");
      const profileUrl = stringField(item, "profile_url");
      if (
        !/^[0-9a-f]{32}$/.test(ownerId) ||
        !/^[a-z][a-z0-9_]{2,29}$/.test(username) ||
        !displayName ||
        profileUrl !== `/${username}` ||
        (avatarMediaId !== null && !isCanonicalUlid(avatarMediaId))
      ) {
        throw invalidResponse("mention suggestion identity is invalid");
      }
      return { ownerId, username, displayName, avatarMediaId, profileUrl };
    });
  }

  async timeline(request: TimelineRequest = {}): Promise<TimelinePage> {
    const query = new URLSearchParams();
    query.set("limit", String(request.limit ?? 20));
    if (request.cursor) query.set("cursor", request.cursor);
    if (request.from) query.set("from", request.from);
    if (request.to) query.set("to", request.to);
    const response = await this.fetcher(`/api/v1/posts?${query}`, {
      headers: { Accept: "application/json" },
      signal: request.signal,
    });
    const { data, meta } = await decodeResponse(response);
    if (!isRecord(data) || !Array.isArray(data.items)) {
      throw invalidResponse("timeline data must contain an items array");
    }
    const nextCursor = meta.next_cursor;
    if (nextCursor !== null && typeof nextCursor !== "string") {
      throw invalidResponse("next_cursor must be a string or null");
    }
    const items = data.items.map(parsePost);
    const timelineKeys = new Set<string>();
    for (const item of items) {
      const key = `${item.id}:${item.timelineItemKind}`;
      if (timelineKeys.has(key)) {
        throw invalidResponse("timeline page contains a duplicate item");
      }
      timelineKeys.add(key);
    }
    return { items, nextCursor };
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
    options: { cursor?: string | null; signal?: AbortSignal } = {},
  ): Promise<CommentThread> {
    const query = options.cursor
      ? `?cursor=${encodeURIComponent(options.cursor)}`
      : "";
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
    return parseCommentThread(data, postId, nullableCursor(meta));
  }

  async commentReplies(
    postId: string,
    rootId: string,
    options: { cursor?: string | null; signal?: AbortSignal } = {},
  ): Promise<CommentReplyPage> {
    const query = options.cursor
      ? `?cursor=${encodeURIComponent(options.cursor)}`
      : "";
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
    return parseCommentReplyPage(data, postId, rootId, nullableCursor(meta));
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
          `/api/v1/owner/media-uploads/${encodeURIComponent(uploadID)}/poster`,
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
    authorKind: PublicComment["authorKind"];
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
  if (
    expected &&
    (comment.parentId !== expected.parentId ||
      comment.authorKind !== expected.authorKind)
  ) {
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
