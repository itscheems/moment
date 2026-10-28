import {
  ApiClient,
  booleanField,
  hasExactKeys,
  invalidResponse,
  isRecord,
  nonNegativeIntegerField,
  objectData,
  type RegistrationMode,
  requestInit,
  stringField,
  utcInstantField,
} from "./client";
import {
  type Account,
  type AccountStatus,
  decodeResponse,
  parsePrivateAccount,
} from "./public";

export interface AccountIdentity {
  provider: string;
  /** What the provider calls this person. Empty when it published nothing. */
  displayName: string;
  /**
   * Empty unless the provider published an address the server is willing to
   * send to: a GitHub noreply alias reads as no address at all.
   */
  email: string;
  linkedAt: string;
  lastLoginAt: string;
}

export interface AccountSessionEntry {
  id: string;
  issuedAt: string;
  lastSeenAt: string;
  expiresAt: string;
  surface: string;
  /** True for the session making the request, which is never more than one. */
  current: boolean;
}

export interface AccountProfileUpdate {
  username?: string;
  displayName?: string;
  bio?: string;
  avatarMediaId?: string | null;
  coverMediaId?: string | null;
}

export const registrationModes = new Set<string>([
  "open",
  "approval",
  "closed",
]);

export interface AccountRoster {
  accounts: Account[];
  registrationMode: RegistrationMode;
}

export function parseAccountIdentity(value: unknown): AccountIdentity {
  // mirrors ginhttp.identityView (internal/transport/http/gin/accounts.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "provider",
      "display_name",
      "email",
      "linked_at",
      "last_login_at",
    ])
  ) {
    throw invalidResponse("account identity contains unsupported fields");
  }
  const provider = stringField(value, "provider");
  if (!/^[a-z][a-z0-9_]{0,31}$/.test(provider)) {
    throw invalidResponse("account identity provider is invalid");
  }
  return {
    provider,
    // A provider that published no name, and an address the server will not
    // send to, both arrive as empty strings. That is something to render
    // around, never a reason to reject the response.
    displayName: stringField(value, "display_name"),
    email: stringField(value, "email"),
    linkedAt: utcInstantField(value, "linked_at"),
    lastLoginAt: utcInstantField(value, "last_login_at"),
  };
}

export function parseAccountSessionEntry(value: unknown): AccountSessionEntry {
  // mirrors ginhttp.sessionListItem (internal/transport/http/gin/accounts.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "id",
      "issued_at",
      "last_seen_at",
      "expires_at",
      "surface",
      "current",
    ])
  ) {
    throw invalidResponse("account session contains unsupported fields");
  }
  const id = stringField(value, "id");
  if (!id) throw invalidResponse("account session id cannot be empty");
  return {
    id,
    issuedAt: utcInstantField(value, "issued_at"),
    lastSeenAt: utcInstantField(value, "last_seen_at"),
    expiresAt: utcInstantField(value, "expires_at"),
    surface: stringField(value, "surface"),
    current: booleanField(value, "current"),
  };
}

export function accountList(
  data: unknown,
  subject: string,
): Record<string, unknown>[] {
  if (!Array.isArray(data)) {
    throw invalidResponse(`${subject} must be an array`);
  }
  return data as Record<string, unknown>[];
}

export function accountProfilePayload(
  update: AccountProfileUpdate,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.username !== undefined) payload.username = update.username;
  if (update.displayName !== undefined) {
    payload.display_name = update.displayName;
  }
  if (update.bio !== undefined) payload.bio = update.bio;
  if (update.avatarMediaId !== undefined) {
    payload.avatar_media_id = update.avatarMediaId;
  }
  if (update.coverMediaId !== undefined) {
    payload.cover_media_id = update.coverMediaId;
  }
  return payload;
}

export class AccountsApi extends ApiClient {
  async updateAccountProfile(
    csrfToken: string,
    update: AccountProfileUpdate,
  ): Promise<Account> {
    const response = await this.fetcher(
      "/api/v1/account/profile",
      requestInit("PATCH", csrfToken, accountProfilePayload(update)),
    );
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }

  private async uploadAccountPicture(
    kind: "avatar" | "cover",
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    const body = new FormData();
    body.append("file", file, file.name);
    const response = await this.fetcher(`/api/v1/account/${kind}`, {
      method: "POST",
      credentials: "same-origin",
      // Content-Type is left to the browser deliberately: only it knows the
      // multipart boundary it just generated, and naming the media type
      // without that boundary makes the body unreadable to the server.
      headers: { Accept: "application/json", "X-CSRF-Token": csrfToken },
      body,
      signal,
    });
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }

  async uploadAccountAvatar(
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    return this.uploadAccountPicture("avatar", csrfToken, file, signal);
  }

  async uploadAccountCover(
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    return this.uploadAccountPicture("cover", csrfToken, file, signal);
  }

  async accountIdentities(signal?: AbortSignal): Promise<AccountIdentity[]> {
    const response = await this.fetcher("/api/v1/account/identities", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return accountList(data, "account identities").map(parseAccountIdentity);
  }

  async unlinkIdentity(csrfToken: string, provider: string): Promise<void> {
    const response = await this.fetcher(
      `/api/v1/account/identities/${encodeURIComponent(provider)}`,
      requestInit("DELETE", csrfToken),
    );
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.unlinkOwnIdentity (internal/transport/http/gin/accounts.go)
    if (
      !hasExactKeys(record, ["unlinked"]) ||
      stringField(record, "unlinked") !== provider
    ) {
      throw invalidResponse("unlink response named a different provider");
    }
  }

  async accountSessions(signal?: AbortSignal): Promise<AccountSessionEntry[]> {
    const response = await this.fetcher("/api/v1/account/sessions", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return accountList(data, "account sessions").map(parseAccountSessionEntry);
  }

  async revokeAccountSessions(csrfToken: string): Promise<number> {
    const response = await this.fetcher(
      "/api/v1/account/sessions",
      requestInit("DELETE", csrfToken),
    );
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.revokeOwnSessions (internal/transport/http/gin/accounts.go)
    if (!hasExactKeys(record, ["revoked"])) {
      throw invalidResponse("session revocation contains unsupported fields");
    }
    return nonNegativeIntegerField(record, "revoked");
  }

  async accounts(signal?: AbortSignal): Promise<AccountRoster> {
    const response = await this.fetcher("/api/v1/accounts", {
      ...requestInit("GET"),
      signal,
    });
    const { data, meta } = await decodeResponse(response);
    const mode = meta.registration_mode;
    if (typeof mode !== "string" || !registrationModes.has(mode)) {
      throw invalidResponse("account roster must state the registration mode");
    }
    return {
      accounts: accountList(data, "accounts").map(parsePrivateAccount),
      registrationMode: mode as RegistrationMode,
    };
  }

  async updateAccountStatus(
    csrfToken: string,
    userId: string,
    status: AccountStatus,
  ): Promise<Account> {
    const response = await this.fetcher(
      `/api/v1/accounts/${encodeURIComponent(userId)}`,
      requestInit("PATCH", csrfToken, { status }),
    );
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }
}

export const accountsApi = new AccountsApi();

export interface AccountSecurity {
  methods: string[];
  passwordEnabled: boolean;
}
export interface ApiCredential {
  id: string;
  user_id: string;
  label: string;
  created_at_ms: number;
  revoked_at_ms: number | null;
}
export interface IssuedApiCredential {
  credential: ApiCredential;
  token: string;
}

export async function accountSecurity(
  signal?: AbortSignal,
): Promise<AccountSecurity> {
  const response = await window.fetch("/api/v1/account/security", {
    ...requestInit("GET"),
    signal,
  });
  const record = objectData((await decodeResponse(response)).data);
  if (
    !Array.isArray(record.methods) ||
    !record.methods.every((value) => typeof value === "string") ||
    typeof record.password_enabled !== "boolean"
  )
    throw invalidResponse("account security response is invalid");
  return { methods: record.methods, passwordEnabled: record.password_enabled };
}
export async function setAccountPassword(
  csrfToken: string,
  password: string,
): Promise<void> {
  const response = await window.fetch(
    "/api/v1/account/password",
    requestInit("PUT", csrfToken, { password }),
  );
  const data = objectData((await decodeResponse(response)).data);
  if (data.password_enabled !== true || data.reauthentication_required !== true)
    throw invalidResponse("password update was not confirmed");
}
export async function listApiCredentials(
  signal?: AbortSignal,
): Promise<ApiCredential[]> {
  const response = await window.fetch("/api/v1/account/tokens", {
    ...requestInit("GET"),
    signal,
  });
  const { data } = await decodeResponse(response);
  if (!Array.isArray(data))
    throw invalidResponse("API credentials must be an array");
  return data.map(parseApiCredential);
}
function parseApiCredential(value: unknown): ApiCredential {
  const record = objectData(value);
  const id = stringField(record, "id"),
    user_id = stringField(record, "user_id"),
    label = stringField(record, "label");
  if (
    !/^[0-9a-f]{32}$/.test(id) ||
    !/^[0-9a-f]{32}$/.test(user_id) ||
    typeof record.created_at_ms !== "number" ||
    (record.revoked_at_ms !== null && typeof record.revoked_at_ms !== "number")
  )
    throw invalidResponse("API credential is invalid");
  return {
    id,
    user_id,
    label,
    created_at_ms: record.created_at_ms,
    revoked_at_ms: record.revoked_at_ms,
  };
}
export async function createApiCredential(
  csrfToken: string,
  label: string,
): Promise<IssuedApiCredential> {
  const response = await window.fetch(
    "/api/v1/account/tokens",
    requestInit("POST", csrfToken, { label }),
  );
  const data = objectData((await decodeResponse(response)).data);
  const token = stringField(data, "token");
  if (!token) throw invalidResponse("API token is empty");
  return { credential: parseApiCredential(data.credential), token };
}
export async function revokeApiCredential(
  csrfToken: string,
  id: string,
): Promise<boolean> {
  const response = await window.fetch(
    `/api/v1/account/tokens/${encodeURIComponent(id)}`,
    requestInit("DELETE", csrfToken),
  );
  const data = objectData((await decodeResponse(response)).data);
  if (data.revoked !== true)
    throw invalidResponse("API token revocation was not confirmed");
  return booleanField(data, "reauthentication_required");
}
export async function setAccountRole(
  csrfToken: string,
  id: string,
  role: "admin" | "member",
): Promise<Account> {
  const response = await window.fetch(
    `/api/v1/accounts/${encodeURIComponent(id)}/role`,
    requestInit("PUT", csrfToken, { role }),
  );
  return parsePrivateAccount((await decodeResponse(response)).data);
}

export async function confirmAccountCredential(
  csrfToken: string,
  credential: { password: string } | { token: string },
): Promise<string> {
  const response = await window.fetch(
    "/api/v1/account/reauthentication",
    requestInit("POST", csrfToken, credential),
  );
  return utcInstantField(
    objectData((await decodeResponse(response)).data),
    "expires_at",
  );
}

export async function revokeAllCredentials(csrfToken: string): Promise<void> {
  const response = await window.fetch(
    "/api/v1/account/credentials",
    requestInit("DELETE", csrfToken),
  );
  if (objectData((await decodeResponse(response)).data).revoked !== true)
    throw invalidResponse("credential revocation was not confirmed");
}

export async function credentialConfirmationStatus(
  signal?: AbortSignal,
): Promise<string | null> {
  const response = await window.fetch("/api/v1/account/reauthentication", {
    ...requestInit("GET"),
    signal,
  });
  const data = objectData((await decodeResponse(response)).data);
  if (!booleanField(data, "confirmed")) return null;
  return utcInstantField(data, "expires_at");
}
