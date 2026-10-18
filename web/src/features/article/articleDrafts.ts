import {
  PublicApi,
  PublicApiError,
  parseArticleDocument,
  parsePost,
  decodeResponse,
  type ArticleDocument,
  type PostVisibility,
  type PublicPost,
} from "../../api/public";

export interface ArticleDraft {
  excerpt?: string;
  authorUserId?: string;
  sourceState?: "draft" | "working_copy" | "withdrawn";
  sourceVisibility?: PostVisibility;
  id: string;
  title: string;
  body: ArticleDocument;
  bodySchemaVersion: number;
  coverMediaId: string | null;
  /**
   * The published article this draft is the working copy of, and null for a
   * draft that has never been an article. Publishing a working copy replaces
   * that article rather than adding a second one.
   */
  publishedEntryId: string | null;
  createdAt: string;
  updatedAt: string;
}

/** What a save carries. The server derives everything else. */
export interface ArticleDraftInput {
  title: string;
  body: ArticleDocument;
  coverMediaId: string | null;
}

/** The error codes this surface has an answer for. */
export const ARTICLE_DRAFT_STALE = "ARTICLE_DRAFT_STALE";
export const ARTICLE_DRAFT_LIMIT = "ARTICLE_DRAFT_LIMIT";
export const ARTICLE_DRAFT_NOT_FOUND = "ARTICLE_DRAFT_NOT_FOUND";

/** Mirrors MaxArticleDrafts (internal/app/articles.go). */
export const MAX_ARTICLE_DRAFTS = 50;
/** Mirrors MaxArticleTitleCodePoints (internal/domain/article_doc.go). */
export const MAX_ARTICLE_TITLE_CODE_POINTS = 200;

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

function hasExactKeys(
  record: Record<string, unknown>,
  expected: readonly string[],
): boolean {
  const actual = Object.keys(record).sort();
  const wanted = [...expected].sort();
  return (
    actual.length === wanted.length &&
    actual.every((value, index) => value === wanted[index])
  );
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw invalidResponse(`draft field ${field} must be a string`);
  }
  return value;
}

function nullableStringField(
  record: Record<string, unknown>,
  field: string,
): string | null {
  const value = record[field];
  if (value === null) return null;
  if (typeof value !== "string") {
    throw invalidResponse(`draft field ${field} must be a string or null`);
  }
  return value;
}

function instantField(record: Record<string, unknown>, field: string): string {
  const value = stringField(record, field);
  if (!Number.isFinite(Date.parse(value))) {
    throw invalidResponse(`draft field ${field} must be an instant`);
  }
  return value;
}

/**
 * One draft, rebuilt field by field.
 *
 * `body_schema_version` is a plain integer here where a published article
 * carries a nullable one: a draft always has a body, so the two cannot share a
 * parser without one of them lying about what it holds.
 */
export function parseArticleDraft(value: unknown): ArticleDraft {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      ...["author_user_id", "source_state", "source_visibility"].filter(
        (key) => key in value,
      ),
      ...("excerpt" in value ? ["excerpt"] : []),
      "id",
      "title",
      "body",
      "body_schema_version",
      "cover_media_id",
      "published_entry_id",
      "created_at",
      "updated_at",
    ])
  ) {
    throw invalidResponse("draft contains an unsupported or missing field");
  }
  if (
    "author_user_id" in value &&
    (typeof value.author_user_id !== "string" ||
      !/^[0-9a-f]{32}$/.test(value.author_user_id))
  )
    throw invalidResponse("draft author identity is invalid");
  if (
    "source_state" in value &&
    value.source_state !== "draft" &&
    value.source_state !== "working_copy" &&
    value.source_state !== "withdrawn"
  )
    throw invalidResponse("draft source state is invalid");
  if (
    "source_visibility" in value &&
    value.source_visibility !== "everyone" &&
    value.source_visibility !== "only_me"
  )
    throw invalidResponse("draft source visibility is invalid");
  const schemaVersion = value.body_schema_version;
  if (!Number.isSafeInteger(schemaVersion) || Number(schemaVersion) < 1) {
    throw invalidResponse("draft body_schema_version must be a version");
  }
  return {
    ...("excerpt" in value ? { excerpt: stringField(value, "excerpt") } : {}),
    ...(typeof value.author_user_id === "string"
      ? { authorUserId: value.author_user_id }
      : {}),
    ...(value.source_state === "draft" ||
    value.source_state === "working_copy" ||
    value.source_state === "withdrawn"
      ? { sourceState: value.source_state }
      : {}),
    ...(value.source_visibility === "everyone" ||
    value.source_visibility === "only_me"
      ? { sourceVisibility: value.source_visibility }
      : {}),
    id: stringField(value, "id"),
    title: stringField(value, "title"),
    body: parseArticleDocument(value.body),
    bodySchemaVersion: Number(schemaVersion),
    coverMediaId: nullableStringField(value, "cover_media_id"),
    publishedEntryId: nullableStringField(value, "published_entry_id"),
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
  };
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

function draftPayload(input: ArticleDraftInput): Record<string, unknown> {
  return {
    title: input.title,
    body: input.body,
    cover_media_id: input.coverMediaId,
  };
}

function draftPath(id: string): string {
  return `/api/v1/articles/drafts/${encodeURIComponent(id)}`;
}

export class ArticleDraftApi {
  private readonly fetcher: Fetcher;

  constructor(fetcher: Fetcher = window.fetch.bind(window)) {
    this.fetcher = fetcher;
  }

  async list(signal?: AbortSignal): Promise<ArticleDraftSummary[]> {
    const response = await this.fetcher("/api/v1/articles/drafts", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    if (!Array.isArray(data)) {
      throw invalidResponse("the draft list must be an array");
    }
    return data.map(parseArticleDraftSummary);
  }

  async published(cursor?: string, signal?: AbortSignal) {
    return new PublicApi((path, init) =>
      this.fetcher(
        String(path).replace("/api/v1/posts", "/api/v1/articles"),
        init,
      ),
    ).timeline({ cursor, limit: 20, signal });
  }
  async readManaged(id: string, signal?: AbortSignal): Promise<PublicPost> {
    const { data } = await decodeResponse(
      await this.fetcher(`/api/v1/articles/${encodeURIComponent(id)}`, {
        ...requestInit("GET"),
        signal,
      }),
    );
    return parsePost(data);
  }

  async unpublish(csrfToken: string, id: string): Promise<ArticleDraft> {
    const response = await this.fetcher(
      `/api/v1/articles/${encodeURIComponent(id)}/unpublish`,
      requestInit("POST", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parseArticleDraft(data);
  }
  async removeArticle(csrfToken: string, id: string): Promise<void> {
    await decodeResponse(
      await this.fetcher(
        `/api/v1/articles/${encodeURIComponent(id)}`,
        requestInit("DELETE", csrfToken),
      ),
    );
  }

  async read(id: string, signal?: AbortSignal): Promise<ArticleDraft> {
    const response = await this.fetcher(draftPath(id), {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return parseArticleDraft(data);
  }

  async create(
    csrfToken: string,
    input: ArticleDraftInput,
    idempotencyKey?: string,
  ): Promise<ArticleDraft> {
    const response = await this.fetcher("/api/v1/articles/drafts", {
      ...requestInit("POST", csrfToken, draftPayload(input)),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-CSRF-Token": csrfToken,
        ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
      },
    });
    const { data } = await decodeResponse(response);
    return parseArticleDraft(data);
  }

  /**
   * Save over a draft.
   *
   * `expectedUpdatedAt` is the instant this copy was read at, echoed back
   * exactly as the server spelled it: the row is matched on it, so a save that
   * guessed the format would read as a conflict rather than as a save. The
   * server answers 409 ARTICLE_DRAFT_STALE when somebody else has written
   * since, which is the one case this client must never resolve on its own.
   */
  async replace(
    csrfToken: string,
    id: string,
    input: ArticleDraftInput,
    expectedUpdatedAt: string,
  ): Promise<ArticleDraft> {
    const response = await this.fetcher(
      draftPath(id),
      requestInit("PUT", csrfToken, {
        ...draftPayload(input),
        expected_updated_at: expectedUpdatedAt,
      }),
    );
    const { data } = await decodeResponse(response);
    return parseArticleDraft(data);
  }

  async discard(csrfToken: string, id: string): Promise<void> {
    const response = await this.fetcher(
      draftPath(id),
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    if (!isRecord(data) || data.discarded !== true || data.id !== id) {
      throw invalidResponse("the draft deletion was not confirmed");
    }
  }

  /**
   * Publish a draft, which removes it and answers with the post it became.
   *
   * The key is scoped to the draft id on the server, so a retry after a lost
   * answer replays the first publish instead of writing a second article.
   */
  async publish(
    csrfToken: string,
    id: string,
    idempotencyKey: string,
    visibility?: PostVisibility,
  ): Promise<PublicPost> {
    const init = requestInit(
      "POST",
      csrfToken,
      visibility === undefined ? {} : { visibility },
    );
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher(`${draftPath(id)}/publish`, {
      ...init,
      headers,
    });
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  /** Open a working copy of an article that is already published. */
  async openPublished(
    csrfToken: string,
    articleId: string,
  ): Promise<ArticleDraft> {
    const response = await this.fetcher(
      `/api/v1/articles/${encodeURIComponent(articleId)}/draft`,
      requestInit("POST", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parseArticleDraft(data);
  }
}

function errorCode(error: unknown): string | null {
  return error instanceof PublicApiError ? error.code : null;
}

export function isStaleDraftError(error: unknown): boolean {
  return errorCode(error) === ARTICLE_DRAFT_STALE;
}

export function isDraftLimitError(error: unknown): boolean {
  return errorCode(error) === ARTICLE_DRAFT_LIMIT;
}

export function isDraftMissingError(error: unknown): boolean {
  return errorCode(error) === ARTICLE_DRAFT_NOT_FOUND;
}

export function articleDraftErrorMessage(error: unknown): string {
  switch (errorCode(error)) {
    case ARTICLE_DRAFT_STALE:
      return "这份草稿在别处改过了。";
    case ARTICLE_DRAFT_LIMIT:
      return `草稿已经有 ${MAX_ARTICLE_DRAFTS} 份，删掉一些再新建。`;
    case ARTICLE_DRAFT_NOT_FOUND:
      return "这份草稿已经不在了。";
    case "CONTENT_EMPTY":
      return "文章要有标题，正文也要有内容。";
    case "CONTENT_TOO_LARGE":
      return "这篇文章太长了，服务端存不下。";
    case "INVALID_ARGUMENT":
      return "正文里有服务端不接受的内容。";
    case "EXTERNAL_IMAGE_NOT_ALLOWED":
      return "正文里的图片必须先上传到这个站点。";
    case "UNAUTHORIZED":
    case "CSRF_INVALID":
      return "登录状态已经失效，重新登录后再保存。";
    case null:
      return "没能连上服务器。";
    default:
      return "保存没有成功。";
  }
}

/**
 * The instant the server says the draft now carries, from a 409.
 *
 * It is what a "用服务器上的版本" choice reloads against, so it is read from
 * the rejection rather than guessed.
 */
export function staleDraftUpdatedAt(error: unknown): string | null {
  if (
    !(error instanceof PublicApiError) ||
    error.code !== ARTICLE_DRAFT_STALE
  ) {
    return null;
  }
  const updatedAt = error.details.updated_at;
  return typeof updatedAt === "string" ? updatedAt : null;
}

export type ArticleDraftSummary = Omit<
  ArticleDraft,
  "body" | "bodySchemaVersion" | "coverMediaId"
>;
function parseArticleDraftSummary(value: unknown): ArticleDraftSummary {
  if (!isRecord(value))
    throw invalidResponse("draft summary must be an object");
  const keys = [
    ...("excerpt" in value ? ["excerpt"] : []),
    "id",
    "title",
    "published_entry_id",
    "created_at",
    "updated_at",
    "author_user_id",
    "source_state",
    "source_visibility",
  ];
  if ("body" in value) {
    const {
      body: _body,
      bodySchemaVersion: _version,
      coverMediaId: _cover,
      ...summary
    } = parseArticleDraft(value);
    void _body;
    void _version;
    void _cover;
    return summary;
  }
  if (!hasExactKeys(value, keys))
    throw invalidResponse(
      "draft summary contains unsupported or missing fields",
    );
  if (
    typeof value.author_user_id !== "string" ||
    !/^[0-9a-f]{32}$/.test(value.author_user_id)
  )
    throw invalidResponse("summary author identity is invalid");
  const state = value.source_state;
  const visibility = value.source_visibility;
  if (
    (state !== "draft" && state !== "working_copy" && state !== "withdrawn") ||
    (visibility !== "everyone" && visibility !== "only_me")
  )
    throw invalidResponse("draft summary state is invalid");
  return {
    ...("excerpt" in value ? { excerpt: stringField(value, "excerpt") } : {}),
    id: stringField(value, "id"),
    title: stringField(value, "title"),
    publishedEntryId: nullableStringField(value, "published_entry_id"),
    createdAt: instantField(value, "created_at"),
    updatedAt: instantField(value, "updated_at"),
    authorUserId: stringField(value, "author_user_id"),
    sourceState: state,
    sourceVisibility: visibility,
  };
}
