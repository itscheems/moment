import { decodeResponse, parseAccount, type PublicAccount } from "./public";
import { invalidResponse, isRecord, requestInit } from "./client";

export interface BrowserAccount {
  account: PublicAccount;
  available: boolean;
}
export interface BrowserAccounts {
  accounts: BrowserAccount[];
  csrfToken: string;
}

const base = "/api/v1/browser/accounts";
export const browserAccountsApi = {
  async list(signal?: AbortSignal): Promise<BrowserAccounts> {
    const { data } = await decodeResponse(
      await fetch(base, { ...requestInit("GET"), signal }),
    );
    if (
      !isRecord(data) ||
      !Array.isArray(data.accounts) ||
      typeof data.csrf_token !== "string" ||
      !data.csrf_token
    )
      throw invalidResponse("invalid browser account list");
    return {
      csrfToken: data.csrf_token,
      accounts: data.accounts.map((value) => {
        if (!isRecord(value) || typeof value.available !== "boolean")
          throw invalidResponse("invalid browser account");
        return {
          account: parseAccount(value.account),
          available: value.available,
        };
      }),
    };
  },
  async bootstrap(csrf: string): Promise<void> {
    await decodeResponse(
      await fetch(`${base}/bootstrap`, requestInit("POST", csrf)),
    );
  },
  async add(
    csrf: string,
    credential: string,
    method: "password" | "token",
    username?: string,
  ): Promise<void> {
    await decodeResponse(
      await fetch(
        base,
        requestInit("POST", csrf, {
          [method]: credential,
          ...(username ? { username } : {}),
        }),
      ),
    );
  },
  async activate(csrf: string, id: string): Promise<void> {
    await decodeResponse(
      await fetch(
        `${base}/${encodeURIComponent(id)}/activate`,
        requestInit("POST", csrf),
      ),
    );
  },
  async remove(csrf: string, id?: string): Promise<void> {
    await decodeResponse(
      await fetch(
        id ? `${base}/${encodeURIComponent(id)}` : base,
        requestInit("DELETE", csrf),
      ),
    );
  },
  async counts(signal?: AbortSignal): Promise<Record<string, number>> {
    const { data } = await decodeResponse(
      await fetch(`${base}/notification-counts`, {
        ...requestInit("GET"),
        signal,
      }),
    );
    if (
      !isRecord(data) ||
      !isRecord(data.counts) ||
      Object.entries(data.counts).some(
        ([id, count]) =>
          !/^[0-9a-f]{32}$/.test(id) ||
          !Number.isSafeInteger(count) ||
          Number(count) < 0,
      )
    )
      throw invalidResponse("invalid browser notification counts");
    return data.counts as Record<string, number>;
  },
  async startOAuth(csrf: string, provider: string): Promise<string> {
    const { data } = await decodeResponse(
      await fetch(
        `/api/v1/login/oauth/${encodeURIComponent(provider)}/add?return=/`,
        requestInit("POST", csrf),
      ),
    );
    if (!isRecord(data) || typeof data.authorization_url !== "string")
      throw invalidResponse("invalid authorization URL");
    const url = new URL(data.authorization_url);
    if (url.protocol !== "https:")
      throw invalidResponse("authorization URL must use HTTPS");
    return url.href;
  },
};
