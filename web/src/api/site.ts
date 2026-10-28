import { credentialFetch } from "./credentialConfirmation";
import {
  ApiClient,
  decodeExactSuccess,
  type RegistrationMode,
  requestInit,
} from "./client";
import { decodeResponse, parseSiteIdentity, type SiteIdentity } from "./public";

export interface SiteSettingsUpdate {
  title?: string;
  timezone?: string;
  publicBaseUrl?: string | null;
  commentsEnabled?: boolean;
  registrationMode?: RegistrationMode;
}

export function sitePayload(
  update: SiteSettingsUpdate,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.title !== undefined) payload.title = update.title;
  if (update.timezone !== undefined) payload.timezone = update.timezone;
  if (update.publicBaseUrl !== undefined)
    payload.public_base_url = update.publicBaseUrl;
  if (update.commentsEnabled !== undefined)
    payload.comments_enabled = update.commentsEnabled;
  if (update.registrationMode !== undefined)
    payload.registration_mode = update.registrationMode;
  return payload;
}

export class SiteApi extends ApiClient {
  async updateSite(
    csrfToken: string,
    update: SiteSettingsUpdate,
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      "/api/v1/site",
      requestInit("PATCH", csrfToken, sitePayload(update)),
    );
    const data = await decodeExactSuccess(response, [
      "profile_user_id",
      "username",
      "display_name",
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "cover_media_id",
      "pinned_post_id",
      "comments_enabled",
    ]);
    return parseSiteIdentity(data);
  }
}

export const siteApi = new SiteApi();

export async function selectSiteProfile(
  csrfToken: string,
  userId: string,
): Promise<SiteIdentity> {
  const response = await credentialFetch(
    "/api/v1/site/profile",
    requestInit("PUT", csrfToken, { user_id: userId }),
  );
  return parseSiteIdentity((await decodeResponse(response)).data);
}
