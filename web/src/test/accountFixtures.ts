import { t } from "../i18n";
/**
 * Account payload shapes the server actually sends.
 *
 * `/api/v1/account/session` is validated against an exact key set, so a
 * fixture that predates a field the server now publishes does not fail -- it
 * passes, while every real sign-in throws. `limits` arrived that way. Keeping
 * it behind one factory means the next field is one edit rather than a sweep
 * through ten hand-written envelopes.
 */

/** mirrors ginhttp.accountLimitsView (internal/transport/http/gin/accounts.go) */
export interface AccountLimitsWire {
  profile_picture_bytes: number;
  display_name_length: number;
  bio_length: number;
}

export function makeAccountLimits(
  overrides: Partial<AccountLimitsWire> = {},
): AccountLimitsWire {
  return {
    profile_picture_bytes: 8 * 1024 * 1024,
    display_name_length: 80,
    bio_length: 500,
    ...overrides,
  };
}

/** Data returned by ginhttp.accountSessionView for an active member. */
export function makeAccountSession() {
  const identity = makeSiteIdentity();
  return {
    csrf_token: "csrf-from-cookie",
    preferences: { ui_locale: null },
    expires_at: "2999-08-11T01:00:00.000Z",
    account: {
      user_id: identity.profileUserId,
      username: identity.username,
      display_name: identity.displayName,
      bio: identity.bio,
      avatar_media_id: identity.avatarMediaId,
      cover_media_id: identity.coverMediaId,
      role: "member",
      status: "active",
      username_chosen: true,
      profile_url: `/${identity.username}`,
    },
    limits: makeAccountLimits(),
  };
}

export function makeSiteIdentity(): import("../api/public").SiteIdentity {
  return {
    profileUserId: "1".repeat(32),
    username: "writer",
    displayName: "Writer",
    title: t("Moment"),
    bio: "",
    timezone: "UTC",
    publicBaseUrl: null,
    avatarMediaId: null,
    coverMediaId: null,
    pinnedPostId: null,
    commentsEnabled: true,
    instanceId: "a".repeat(32),
    instanceInitializedAt: null,
  };
}
