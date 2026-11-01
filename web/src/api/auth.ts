import { parsePreferences, type AccountPreferences } from "./preferences";
import {
  ApiClient,
  decodeExactSuccess,
  hasExactKeys,
  invalidResponse,
  isRecord,
  objectData,
  positiveIntegerField,
  requestInit,
  stringField,
} from "./client";
import { type Account, decodeResponse, parsePrivateAccount } from "./public";

export interface AuthSession {
  csrfToken: string;
  expiresAt: string;
}

export interface RegistrationOptions {
  registrationMode: "open" | "approval" | "closed";
  usernameMinLength: number;
  usernameMaxLength: number;
  passwordMinBytes: number;
  passwordMaxBytes: number;
}

export interface EmailChallenge {
  challengeId: string;
  expiresAt: string;
  resendAt: string;
}
export interface VerifiedEmail {
  token: string;
  email: string;
  expiresAt: string;
}

export interface RegistrationResult extends AuthSession {
  username: string;
  accountStatus: "active" | "pending";
}

export type SignInMethod = "password" | "token";

export interface SignInMethods {
  methods: SignInMethod[];
}

export interface OAuthLoginProvider {
  name: string;
  label: string;
}

export interface AccountLimits {
  profilePictureBytes: number;
  displayNameLength: number;
  bioLength: number;
}

export interface AccountSession {
  csrfToken: string;
  expiresAt: string;
  account: Account;
  limits: AccountLimits;
  preferences: AccountPreferences;
}

export function hasKeys(
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

export function parseAccountLimits(value: unknown): AccountLimits {
  const record = objectData(value);
  if (
    !hasExactKeys(record, [
      "profile_picture_bytes",
      "display_name_length",
      "bio_length",
    ])
  ) {
    throw invalidResponse("account limits contain unsupported fields");
  }
  return {
    profilePictureBytes: positiveIntegerField(record, "profile_picture_bytes"),
    displayNameLength: positiveIntegerField(record, "display_name_length"),
    bioLength: positiveIntegerField(record, "bio_length"),
  };
}

export class AuthApi extends ApiClient {
  async registrationOptions(
    signal?: AbortSignal,
  ): Promise<RegistrationOptions> {
    const response = await this.fetcher("/api/v1/register/options", {
      ...requestInit("GET"),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    if (
      !hasExactKeys(record, [
        "registration_mode",
        "username_min_length",
        "username_max_length",
        "password_min_bytes",
        "password_max_bytes",
      ]) ||
      (record.registration_mode !== "open" &&
        record.registration_mode !== "approval" &&
        record.registration_mode !== "closed")
    ) {
      throw invalidResponse("registration options contain unsupported fields");
    }
    const usernameMinLength = positiveIntegerField(
      record,
      "username_min_length",
    );
    const usernameMaxLength = positiveIntegerField(
      record,
      "username_max_length",
    );
    const passwordMinBytes = positiveIntegerField(record, "password_min_bytes");
    const passwordMaxBytes = positiveIntegerField(record, "password_max_bytes");
    if (
      usernameMinLength > usernameMaxLength ||
      passwordMinBytes > passwordMaxBytes
    ) {
      throw invalidResponse("registration limits are invalid");
    }
    return {
      registrationMode: record.registration_mode,
      usernameMinLength,
      usernameMaxLength,
      passwordMinBytes,
      passwordMaxBytes,
    };
  }

  async checkRegistration(username: string, password: string): Promise<string> {
    const response = await this.fetcher(
      "/api/v1/register/check",
      requestInit("POST", undefined, { username, password }),
    );
    return stringField(
      objectData((await decodeResponse(response)).data),
      "username",
    );
  }

  async requestRegistrationEmail(email: string): Promise<EmailChallenge> {
    const response = await this.fetcher(
      "/api/v1/register/email",
      requestInit("POST", undefined, { email }),
    );
    const record = objectData((await decodeResponse(response)).data);
    const challengeId = stringField(record, "challenge_id"),
      expiresAt = stringField(record, "expires_at"),
      resendAt = stringField(record, "resend_at");
    if (
      !challengeId ||
      !Number.isFinite(Date.parse(expiresAt)) ||
      !Number.isFinite(Date.parse(resendAt))
    )
      throw invalidResponse("email challenge is invalid");
    return { challengeId, expiresAt, resendAt };
  }

  async verifyRegistrationEmail(
    challengeId: string,
    code: string,
  ): Promise<VerifiedEmail> {
    const response = await this.fetcher(
      "/api/v1/register/email/verify",
      requestInit("POST", undefined, { challenge_id: challengeId, code }),
    );
    const record = objectData((await decodeResponse(response)).data);
    const token = stringField(record, "verification_token"),
      email = stringField(record, "email"),
      expiresAt = stringField(record, "expires_at");
    if (!token || !email || !Number.isFinite(Date.parse(expiresAt)))
      throw invalidResponse("email verification is invalid");
    return { token, email, expiresAt };
  }

  async register(
    username: string,
    password: string,
    verificationToken: string,
    displayName = "",
  ): Promise<RegistrationResult> {
    const response = await this.fetcher(
      "/api/v1/register",
      requestInit("POST", undefined, {
        username,
        password,
        verification_token: verificationToken,
        display_name: displayName,
      }),
    );
    const record = objectData((await decodeResponse(response)).data);
    if (
      response.status !== 201 ||
      !hasExactKeys(record, [
        "account_created",
        "username",
        "account_status",
        "expires_at",
        "csrf_token",
      ]) ||
      record.account_created !== true ||
      (record.account_status !== "active" &&
        record.account_status !== "pending")
    ) {
      throw invalidResponse("registration response is invalid");
    }
    const normalizedUsername = stringField(record, "username");
    const expiresAt = stringField(record, "expires_at");
    const csrfToken = stringField(record, "csrf_token");
    if (
      !normalizedUsername ||
      !Number.isFinite(Date.parse(expiresAt)) ||
      !csrfToken
    ) {
      throw invalidResponse("registration session is invalid");
    }
    return {
      username: normalizedUsername,
      accountStatus: record.account_status,
      expiresAt,
      csrfToken,
    };
  }

  async exchange(
    credential: string,
    method: "password" | "token" = "password",
    username?: string,
  ): Promise<AuthSession> {
    // The handle is sent so the server can refuse a credential typed under
    // somebody else's name. Omitting it is still allowed, which is what keeps
    // the token usable as the break-glass credential it exists to be.
    const payload: Record<string, string> = { [method]: credential };
    if (username) payload.username = username;
    const response = await this.fetcher(
      "/api/v1/login/session",
      requestInit("POST", undefined, payload),
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

  async signInMethods(signal?: AbortSignal): Promise<SignInMethods> {
    const response = await this.fetcher("/api/v1/login/methods", {
      ...requestInit("GET"),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.signInMethods
    if (!hasExactKeys(record, ["methods"])) {
      throw invalidResponse("sign-in methods response has unexpected fields");
    }
    const raw = record.methods;
    if (!Array.isArray(raw)) {
      throw invalidResponse("sign-in methods must be a list");
    }
    const methods: SignInMethod[] = [];
    for (const value of raw) {
      // An instance newer than this client can name a way of signing in that
      // this build has no screen for. Dropping it keeps the ones it does know
      // usable, where refusing the whole response would leave no way in at all.
      if (value === "password" || value === "token") methods.push(value);
    }
    return { methods };
  }

  async loginProviders(signal?: AbortSignal): Promise<OAuthLoginProvider[]> {
    const response = await this.fetcher("/api/v1/login/oauth/providers", {
      ...requestInit("GET"),
      signal,
    });
    const data = await decodeExactSuccess(response, ["providers"]);
    const providers = data.providers;
    if (!Array.isArray(providers)) {
      throw invalidResponse("login providers must be an array");
    }
    return providers.flatMap((entry): OAuthLoginProvider[] => {
      // mirrors oauth.Descriptor (internal/auth/oauth/provider.go)
      if (
        !isRecord(entry) ||
        !hasKeys(entry, ["name", "label"], ["flows", "native_client_id"])
      ) {
        throw invalidResponse("login provider contains unsupported fields");
      }
      const name = stringField(entry, "name");
      const label = stringField(entry, "label");
      if (!/^[a-z][a-z0-9_]{0,31}$/.test(name) || !label.trim()) {
        throw invalidResponse("login provider identity is invalid");
      }
      // The client id a native app runs its own authorization as; the
      // browser has no use for it beyond checking it is well-formed.
      if ("native_client_id" in entry) stringField(entry, "native_client_id");
      // A browser can finish only the browser flow. The same descriptor also
      // tells native clients about device and assertion flows, so a provider
      // that names flows but not this one is not a way in from here. An
      // instance from before flows were published served only the browser
      // flow, so no list means it is available.
      if ("flows" in entry && entry.flows !== null) {
        const flows = entry.flows;
        if (
          !Array.isArray(flows) ||
          !flows.every((flow) => typeof flow === "string")
        ) {
          throw invalidResponse("login provider flows must be strings");
        }
        if (!flows.includes("browser")) return [];
      }
      return [{ name, label }];
    });
  }

  async renewSession(csrfToken: string, signal?: AbortSignal): Promise<string> {
    const response = await this.fetcher("/api/v1/account/session/renew", {
      ...requestInit("POST", csrfToken),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    if (!hasExactKeys(record, ["expires_at"]))
      throw invalidResponse("invalid session renewal");
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt)))
      throw invalidResponse("invalid session expiry");
    return expiresAt;
  }

  async accountSession(signal?: AbortSignal): Promise<AccountSession> {
    const response = await this.fetcher("/api/v1/account/session", {
      ...requestInit("GET"),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    // mirrors ginhttp.accountSessionView (internal/transport/http/gin/accounts.go)
    if (
      !hasExactKeys(record, [
        "expires_at",
        "csrf_token",
        "account",
        "limits",
        "preferences",
      ])
    ) {
      throw invalidResponse("account session contains unsupported fields");
    }
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) {
      throw invalidResponse("response field expires_at must be an instant");
    }
    const csrfToken = stringField(record, "csrf_token");
    if (!csrfToken) throw invalidResponse("csrf_token cannot be empty");
    return {
      csrfToken,
      expiresAt,
      account: parsePrivateAccount(record.account),
      limits: parseAccountLimits(record.limits),
      preferences: parsePreferences(record.preferences),
    };
  }

  async logout(csrfToken: string): Promise<boolean> {
    const response = await this.fetcher(
      "/api/v1/login/session",
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    if (record.logged_out !== true) {
      throw invalidResponse("logout response was not confirmed");
    }
    return record.active_account === true;
  }
}

export const authApi = new AuthApi();
