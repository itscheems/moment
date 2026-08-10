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

interface ErrorPayload {
  code?: unknown;
  message?: unknown;
  request_id?: unknown;
}

type Fetcher = typeof fetch;

export class PublicApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId: string | null;

  constructor(
    message: string,
    options: { code: string; status: number; requestId?: string | null },
  ) {
    super(message);
    this.name = "PublicApiError";
    this.code = options.code;
    this.status = options.status;
    this.requestId = options.requestId ?? null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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
    throw new PublicApiError(message, {
      code,
      status: response.status,
      requestId:
        typeof error?.request_id === "string" ? error.request_id : null,
    });
  }

  if (!("data" in payload) || !isRecord(payload.meta)) {
    throw invalidResponse("success response must contain data and meta");
  }
  return { data: payload.data, meta: payload.meta };
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
}
