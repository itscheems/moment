import {
  decodeResponse,
  parsePost,
  PublicApiError,
  type PublicPost,
} from "./public";

export interface OwnerSession {
  csrfToken: string;
  expiresAt: string;
}

export interface OwnerStatus {
  initialized: boolean;
  sessionEpoch: number;
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

function integerField(record: Record<string, unknown>, field: string): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) < 0) {
    throw invalidResponse(`response field ${field} must be a safe integer`);
  }
  return Number(value);
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
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    return {
      initialized: booleanField(record, "initialized"),
      sessionEpoch: integerField(record, "session_epoch"),
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
  ): Promise<PublicPost> {
    const init = requestInit("POST", csrfToken, { content_md: contentMd });
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
  ): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("PATCH", csrfToken, { content_md: contentMd }),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async delete(csrfToken: string, id: string): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }
}
