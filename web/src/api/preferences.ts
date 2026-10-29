import {
  ApiClient,
  hasExactKeys,
  invalidResponse,
  objectData,
  requestInit,
} from "./client";
import { decodeResponse } from "./public";

import { isUILocale, type UILocale } from "../i18n/locale";
export type { UILocale } from "../i18n/locale";

export interface AccountPreferences {
  uiLocale: UILocale | null;
}

export function parsePreferences(value: unknown): AccountPreferences {
  const record = objectData(value);
  if (
    !hasExactKeys(record, ["ui_locale"]) ||
    (record.ui_locale !== null && !isUILocale(record.ui_locale))
  ) {
    throw invalidResponse(
      "account preferences contain an unsupported language",
    );
  }
  return { uiLocale: record.ui_locale };
}

export class PreferencesApi extends ApiClient {
  async update(
    csrfToken: string,
    uiLocale: UILocale | null,
    signal?: AbortSignal,
  ): Promise<AccountPreferences> {
    const response = await this.fetcher("/api/v1/account/preferences", {
      ...requestInit("PATCH", csrfToken, { ui_locale: uiLocale }),
      signal,
    });
    return parsePreferences((await decodeResponse(response)).data);
  }
}

export const preferencesApi = new PreferencesApi();
