export interface SiteIdentity {
  title: string;
  bio: string;
  timezone: string;
  publicBaseUrl: string | null;
  avatarMediaId: string | null;
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
}

export interface CommentThread {
  items: PublicComment[];
}

export interface CreateGuestCommentInput {
  authorName: string;
  contentMd: string;
  parentId: string | null;
  website?: string;
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

const ULID_PATTERN = /^[0-9A-HJKMNP-TV-Z]{26}$/;
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
    ])
  ) {
    throw invalidResponse("comment contains an unsupported or missing field");
  }
  const id = stringField(value, "id");
  const postId = stringField(value, "post_id");
  const parentId = nullableStringField(value, "parent_id");
  if (
    !ULID_PATTERN.test(id) ||
    !ULID_PATTERN.test(postId) ||
    (parentId !== null && !ULID_PATTERN.test(parentId))
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
  };
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
  return {
    id: stringField(value, "id"),
    contentMd: stringField(value, "content_md"),
    contentHtml: stringField(value, "content_html"),
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
    publishedAt: instantField(value, "published_at"),
  };
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
      throw invalidResponse(
        "comment success response must use application/json",
      );
    }
  }
  if (
    options.strictSuccessEnvelope &&
    !hasExactKeys(payload, ["data", "meta"])
  ) {
    throw invalidResponse(
      "comment success response must contain only data and meta",
    );
  }
  if (!("data" in payload) || !isRecord(payload.meta)) {
    throw invalidResponse("success response must contain data and meta");
  }
  return { data: payload.data, meta: payload.meta };
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
    if (!isRecord(data)) throw invalidResponse("site data must be an object");
    const commentsEnabled = data.comments_enabled;
    if (typeof commentsEnabled !== "boolean") {
      throw invalidResponse(
        "response field comments_enabled must be a boolean",
      );
    }
    const timezone = stringField(data, "timezone");
    try {
      new Intl.DateTimeFormat("zh-CN", { timeZone: timezone }).format();
    } catch {
      throw invalidResponse("response field timezone must be an IANA timezone");
    }
    const instanceId = meta.instance_id;
    if (
      instanceId !== undefined &&
      (typeof instanceId !== "string" || !/^[0-9a-f]{32}$/.test(instanceId))
    ) {
      throw invalidResponse("meta instance_id must be a runtime instance ID");
    }
    return {
      title: stringField(data, "title"),
      bio: stringField(data, "bio"),
      timezone,
      publicBaseUrl: nullableStringField(data, "public_base_url"),
      avatarMediaId: nullableStringField(data, "avatar_media_id"),
      commentsEnabled,
      instanceId: instanceId ?? null,
    };
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
        }),
      },
    );
    return decodeCreatedComment(response, postId, {
      parentId: input.parentId,
      authorKind: "guest",
      authorName: input.authorName.trim(),
    });
  }
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
