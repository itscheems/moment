import {
  decodeCommentResponse,
  decodeCreatedComment,
  decodeResponse,
  isCanonicalUlid,
  parseComment,
  parseDeletedCommentMeta,
  parsePost,
  parseSiteIdentity,
  PublicApiError,
  type CreatedComment,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "./public";

export interface OwnerSession {
  csrfToken: string;
  expiresAt: string;
}

export interface OwnerStatus {
  initialized: boolean;
  sessionEpoch: number;
  initializedAt: string | null;
  rotatedAt: string | null;
}

export interface RotatedCredential {
  token: string;
  initialized: boolean;
  sessionEpoch: number;
  initializedAt: string;
  rotatedAt: string;
}

export interface SiteSettingsUpdate {
  title?: string;
  bio?: string;
  timezone?: string;
  publicBaseUrl?: string | null;
  avatarMediaId?: string | null;
  commentsEnabled?: boolean;
}

export interface DeletedComment {
  comment: PublicComment;
  repliesAffected: number;
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

export interface PostMediaInput {
  mediaId: string;
  position: number;
  altText: string | null;
}

type Fetcher = typeof fetch;

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

function booleanField(record: Record<string, unknown>, field: string): boolean {
  const value = record[field];
  if (typeof value !== "boolean") {
    throw invalidResponse(`response field ${field} must be a boolean`);
  }
  return value;
}

function objectData(data: unknown): Record<string, unknown> {
  if (!isRecord(data)) throw invalidResponse("response data must be an object");
  return data;
}

function hasExactKeys(record: Record<string, unknown>, expected: string[]) {
  const actual = Object.keys(record).sort();
  const sortedExpected = [...expected].sort();
  return (
    actual.length === sortedExpected.length &&
    actual.every((value, index) => value === sortedExpected[index])
  );
}

function positiveIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) <= 0) {
    throw invalidResponse(`response field ${field} must be a positive integer`);
  }
  return Number(value);
}

function utcInstantField(
  record: Record<string, unknown>,
  field: string,
): string {
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

function optionalInstantField(
  record: Record<string, unknown>,
  field: string,
): string | null {
  return record[field] === undefined ? null : utcInstantField(record, field);
}

async function decodeExactOwnerSuccess(
  response: Response,
  dataKeys: string[],
  optionalDataKeys: string[] = [],
): Promise<Record<string, unknown>> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  if (response.status !== 200) {
    throw invalidResponse("owner settings success must return HTTP 200");
  }
  if (
    !hasExactKeys(result.meta, ["request_id"]) ||
    !stringField(result.meta, "request_id")
  ) {
    throw invalidResponse("owner settings metadata is invalid");
  }
  const data = objectData(result.data);
  const expectedDataKeys = [
    ...dataKeys,
    ...optionalDataKeys.filter((key) => key in data),
  ];
  if (!hasExactKeys(data, expectedDataKeys)) {
    throw invalidResponse("owner settings data contains unsupported fields");
  }
  return data;
}

function sitePayload(update: SiteSettingsUpdate): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.title !== undefined) payload.title = update.title;
  if (update.bio !== undefined) payload.bio = update.bio;
  if (update.timezone !== undefined) payload.timezone = update.timezone;
  if (update.publicBaseUrl !== undefined) {
    payload.public_base_url = update.publicBaseUrl;
  }
  if (update.avatarMediaId !== undefined) {
    payload.avatar_media_id = update.avatarMediaId;
  }
  if (update.commentsEnabled !== undefined) {
    payload.comments_enabled = update.commentsEnabled;
  }
  return payload;
}

function parseUpload(data: unknown): UploadedMedia {
  const result = objectData(data);
  if (
    !hasExactKeys(result, ["media", "preview", "original_url", "preview_url"])
  ) {
    throw invalidResponse("upload result contains unsupported fields");
  }
  const media = objectData(result.media);
  const preview = objectData(result.preview);
  const id = stringField(media, "id");
  if (!isCanonicalUlid(id)) {
    throw invalidResponse("uploaded media id is invalid");
  }
  const originalUrl = stringField(result, "original_url");
  const previewUrl = stringField(result, "preview_url");
  const expectedOriginal = `/api/v1/media/${id}`;
  if (
    originalUrl !== expectedOriginal ||
    previewUrl !== `${expectedOriginal}/preview`
  ) {
    throw invalidResponse("upload result contains an uncontrolled media URL");
  }
  const animatedOriginal = booleanField(media, "animated_original");
  if (booleanField(preview, "animated_original") !== animatedOriginal) {
    throw invalidResponse("upload preview animation metadata is inconsistent");
  }
  return {
    id,
    byteSize: positiveIntegerField(media, "byte_size"),
    width: positiveIntegerField(media, "width"),
    height: positiveIntegerField(media, "height"),
    animatedOriginal,
    previewUrl,
    originalUrl,
  };
}

function mediaPayload(media: PostMediaInput[]): Record<string, unknown>[] {
  return media.map((usage, position) => ({
    media_id: usage.mediaId,
    position,
    alt_text: usage.altText,
  }));
}

function requestInit(
  method: string,
  csrfToken?: string,
  body?: unknown,
): RequestInit {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  return {
    method,
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export class OwnerApi {
  private readonly fetcher: Fetcher;

  constructor(fetcher: Fetcher = window.fetch.bind(window)) {
    this.fetcher = fetcher;
  }

  async exchange(token: string): Promise<OwnerSession> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("POST", undefined, { token }),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) {
      throw invalidResponse("response field expires_at must be an instant");
    }
    const csrfToken = stringField(record, "csrf_token");
    if (!csrfToken) throw invalidResponse("csrf_token cannot be empty");
    return { csrfToken, expiresAt };
  }

  async status(signal?: AbortSignal): Promise<OwnerStatus> {
    const response = await this.fetcher("/api/v1/owner/status", {
      ...requestInit("GET"),
      signal,
    });
    const preliminary = await decodeExactOwnerSuccess(
      response,
      ["initialized", "session_epoch", "initialized_at"],
      ["rotated_at"],
    );
    if (booleanField(preliminary, "initialized") !== true) {
      throw invalidResponse("owner status must be initialized");
    }
    return {
      initialized: true,
      sessionEpoch: positiveIntegerField(preliminary, "session_epoch"),
      initializedAt: utcInstantField(preliminary, "initialized_at"),
      rotatedAt: optionalInstantField(preliminary, "rotated_at"),
    };
  }

  async updateSite(
    csrfToken: string,
    update: SiteSettingsUpdate,
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      "/api/v1/owner/site",
      requestInit("PATCH", csrfToken, sitePayload(update)),
    );
    const data = await decodeExactOwnerSuccess(response, [
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "comments_enabled",
    ]);
    return parseSiteIdentity(data);
  }

  async rotateToken(csrfToken: string): Promise<RotatedCredential> {
    const response = await this.fetcher(
      "/api/v1/owner/token/rotate",
      requestInit("POST", csrfToken),
    );
    const record = await decodeExactOwnerSuccess(response, [
      "token",
      "initialized",
      "session_epoch",
      "initialized_at",
      "rotated_at",
    ]);
    const token = stringField(record, "token");
    const rotatedAt = utcInstantField(record, "rotated_at");
    if (!token) throw invalidResponse("rotated owner token cannot be empty");
    if (booleanField(record, "initialized") !== true) {
      throw invalidResponse("rotated owner credential must be initialized");
    }
    return {
      token,
      initialized: true,
      sessionEpoch: positiveIntegerField(record, "session_epoch"),
      initializedAt: utcInstantField(record, "initialized_at"),
      rotatedAt,
    };
  }

  async logout(csrfToken: string): Promise<void> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    if (record.logged_out !== true) {
      throw invalidResponse("logout response was not confirmed");
    }
  }

  async create(
    csrfToken: string,
    contentMd: string,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
  ): Promise<PublicPost> {
    const init = requestInit("POST", csrfToken, {
      content_md: contentMd,
      media: mediaPayload(media),
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher("/api/v1/owner/posts", {
      ...init,
      headers,
    });
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async update(
    csrfToken: string,
    id: string,
    contentMd: string,
    media?: PostMediaInput[],
  ): Promise<PublicPost> {
    const body: Record<string, unknown> = { content_md: contentMd };
    if (media) body.media = mediaPayload(media);
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("PATCH", csrfToken, body),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async uploadMedia(
    csrfToken: string,
    file: File,
    onProgress: (percent: number) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<UploadedMedia> {
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      request.open("POST", "/api/v1/owner/media");
      request.withCredentials = true;
      request.setRequestHeader("Accept", "application/json");
      request.setRequestHeader("X-CSRF-Token", csrfToken);
      request.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable && event.total > 0) {
          onProgress(
            Math.min(99, Math.round((event.loaded / event.total) * 100)),
          );
        }
      });
      request.addEventListener("error", () =>
        reject(
          new PublicApiError("media upload failed", {
            code: "NETWORK_ERROR",
            status: 0,
          }),
        ),
      );
      request.addEventListener("abort", () =>
        reject(
          new PublicApiError("media upload was cancelled", {
            code: "CANCELLED",
            status: 0,
          }),
        ),
      );
      request.addEventListener("load", () => {
        const headers = new Headers();
        const contentType = request.getResponseHeader("Content-Type");
        if (contentType) headers.set("Content-Type", contentType);
        const response = new Response(request.responseText, {
          status: request.status,
          headers,
        });
        if (
          request.status >= 200 &&
          request.status < 300 &&
          request.status !== 201
        ) {
          reject(invalidResponse("media upload success must return HTTP 201"));
          return;
        }
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
            const uploaded = parseUpload(data);
            onProgress(100);
            resolve(uploaded);
          })
          .catch(reject);
      });
      const abort = () => request.abort();
      if (signal?.aborted) {
        reject(
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

  async delete(csrfToken: string, id: string): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async createComment(
    csrfToken: string,
    postId: string,
    contentMd: string,
    parentId: string | null,
    idempotencyKey: string,
  ): Promise<CreatedComment> {
    const init = requestInit("POST", csrfToken, {
      content_md: contentMd,
      parent_id: parentId,
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(postId)}/comments`,
      { ...init, headers },
    );
    return decodeCreatedComment(response, postId, {
      parentId,
      authorKind: "owner",
    });
  }

  async deleteComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<DeletedComment> {
    const response = await this.fetcher(
      `/api/v1/owner/comments/${encodeURIComponent(commentId)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data, meta } = await decodeCommentResponse(response, [
      "deleted",
      "dry_run",
      "replies_affected",
    ]);
    if (response.status !== 200) {
      throw invalidResponse("comment deletion must return HTTP 200");
    }
    const comment = parseComment(data, postId);
    const result = parseDeletedCommentMeta(meta);
    if (!result.deleted || result.dryRun || comment.id !== commentId) {
      throw invalidResponse("comment deletion was not confirmed");
    }
    if (comment.parentId !== null && result.repliesAffected !== 0) {
      throw invalidResponse("a reply deletion cannot affect child comments");
    }
    return { comment, repliesAffected: result.repliesAffected };
  }
}
