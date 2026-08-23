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
