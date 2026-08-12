export interface SiteIdentity {
  title: string;
  bio: string;
  timezone: string;
  publicBaseUrl: string | null;
  avatarMediaId: string | null;
  coverMediaId: string | null;
  pinnedPostId: string | null;
  commentsEnabled: boolean;
  instanceId: string | null;
}

export interface PublicPost {
  id: string;
  contentMd: string;
  contentHtml: string;
  createdAt: string;
  updatedAt: string;
  publishedAt: string;
  media: PublicMediaUsage[];
}

export interface PublicMediaUsage {
  mediaId: string;
  position: number;
  altText: string | null;
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
  authorKind: "owner" | "guest";
  authorName: string;
  contentMd: string;
  contentHtml: string;
  createdAt: string;
  media: PublicMediaUsage[];
}

export interface CommentThread {
  items: PublicComment[];
}

export interface CreateGuestCommentInput {
  authorName: string;
  contentMd: string;
  parentId: string | null;
  website?: string;
  media?: PublicMediaUsage[];
}

export interface UploadedMedia {
  id: string;
  byteSize: number;
  width: number;
  height: number;
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
    !hasExactKeys(value, [
      "id",
      "post_id",
      "parent_id",
      "depth",
      "author_kind",
      "author_name",
      "content_md",
      "content_html",
      "created_at",
      "media",
    ])
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
  return {
    id,
    postId,
    parentId,
    depth: depth as 0 | 1,
    authorKind,
    authorName,
    contentMd: commentTextField(value, "content_md", 2_000),
    contentHtml: safeCommentHtmlField(value, "content_html"),
    createdAt: instantField(value, "created_at"),
    media,
  };
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
    if (
      !isRecord(item) ||
      !hasExactKeys(item, ["media_id", "position", "alt_text"]) ||
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
    return { mediaId: stringField(item, "media_id"), position: index, altText };
  });
}

function commentOrder(left: PublicComment, right: PublicComment): number {
  const time = Date.parse(left.createdAt) - Date.parse(right.createdAt);
  return time || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0);
}

function parseCommentThread(value: unknown, postId: string): CommentThread {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["items"]) ||
    !Array.isArray(value.items)
  ) {
    throw invalidResponse("comment thread must contain only an items array");
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
  return { items };
}

export function parsePost(value: unknown): PublicPost {
  if (!isRecord(value)) throw invalidResponse("post must be an object");
  if (
    !hasExactKeys(value, [
      "id",
      "content_md",
      "content_html",
      "created_at",
      "updated_at",
      "published_at",
      "media",
    ]) ||
    !Array.isArray(value.media) ||
    value.media.length > 9
  ) {
    throw invalidResponse("post contains an unsupported or missing field");
  }
  const media = parseMediaUsages(value.media, 9, "post");
  return {
    id: stringField(value, "id"),
    contentMd: stringField(value, "content_md"),
    contentHtml: stringField(value, "content_html"),
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
    publishedAt: instantField(value, "published_at"),
    media,
  };
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
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "id",
      "content_md",
      "content_html",
      "created_at",
      "updated_at",
      "published_at",
      "media",
      "snippet",
    ])
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
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["items"]) ||
    !Array.isArray(value.items)
  ) {
    throw invalidResponse("archive months must contain only an items array");
  }
  const items = value.items.map((item, index, values) => {
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
): SiteIdentity {
  if (!isRecord(data)) throw invalidResponse("site data must be an object");
  if (
    !hasExactKeys(data, [
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
    title: stringField(data, "title"),
    bio: stringField(data, "bio"),
    timezone,
    publicBaseUrl: nullableStringField(data, "public_base_url"),
    avatarMediaId,
    coverMediaId,
    pinnedPostId,
    commentsEnabled,
    instanceId,
  };
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
    if (
      instanceId !== undefined &&
      (typeof instanceId !== "string" || !/^[0-9a-f]{32}$/.test(instanceId))
    ) {
      throw invalidResponse("meta instance_id must be a runtime instance ID");
    }
    return parseSiteIdentity(data, instanceId ?? null);
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
    const ids = new Set<string>();
    for (const item of items) {
      if (ids.has(item.id)) {
        throw invalidResponse("timeline page contains a duplicate post");
      }
      ids.add(item.id);
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

  async comments(postId: string, signal?: AbortSignal): Promise<CommentThread> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments`,
      { headers: { Accept: "application/json" }, signal },
    );
    const { data } = await decodeCommentResponse(response, []);
    if (response.status !== 200) {
      throw invalidResponse("comment thread must return HTTP 200");
    }
    return parseCommentThread(data, postId);
  }

  async createGuestComment(
    postId: string,
    input: CreateGuestCommentInput,
    idempotencyKey: string,
  ): Promise<CreatedComment> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments`,
      {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
          "Idempotency-Key": idempotencyKey,
        },
        body: JSON.stringify({
          author_name: input.authorName,
          content_md: input.contentMd,
          parent_id: input.parentId,
          website: input.website ?? "",
          media: (input.media ?? []).map((usage, position) => ({
            media_id: usage.mediaId,
            position,
            alt_text: usage.altText,
          })),
        }),
      },
    );
    return decodeCreatedComment(response, postId, {
      parentId: input.parentId,
      authorKind: "guest",
      authorName: input.authorName.trim(),
    });
  }

  async uploadCommentMedia(
    postId: string,
    file: File,
    onProgress: (percent: number) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<UploadedMedia> {
    return uploadMediaXHR(
      `/api/v1/posts/${encodeURIComponent(postId)}/comment-media`,
      file,
      onProgress,
      signal,
    );
  }
}

export function uploadMediaXHR(
  endpoint: string,
  file: File,
  onProgress: (percent: number) => void = () => undefined,
  signal?: AbortSignal,
  headers: Record<string, string> = {},
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
    request.open("POST", endpoint);
    request.withCredentials = true;
    request.setRequestHeader("Accept", "application/json");
    for (const [name, value] of Object.entries(headers))
      request.setRequestHeader(name, value);
    request.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable && event.total > 0)
        onProgress(
          Math.min(99, Math.round((event.loaded / event.total) * 100)),
        );
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
    request.addEventListener("loadend", () =>
      signal?.removeEventListener("abort", abort),
    );
    const body = new FormData();
    body.append("file", file, file.name);
    onProgress(0);
    request.send(body);
  });
}

function parseUploadedMedia(data: unknown): UploadedMedia {
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
    originalUrl !== `/api/v1/media/${id}` ||
    previewUrl !== `${originalUrl}/preview`
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
    animatedOriginal,
    previewUrl,
    originalUrl,
  };
}

export async function decodeCreatedComment(
  response: Response,
  postId: string,
  expected?: {
    parentId: string | null;
    authorKind: PublicComment["authorKind"];
    authorName?: string;
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
      comment.authorKind !== expected.authorKind ||
      (expected.authorName !== undefined &&
        comment.authorName !== expected.authorName))
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
