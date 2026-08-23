import type { IconName } from "../../components/icons";

export const PROVIDER_ICONS: Record<string, IconName> = {
  github: "github",
  google: "google",
};

const PROVIDER_LABELS: Record<string, string> = {
  github: "GitHub",
  google: "Google",
};

export const PROVIDER_NAME_PATTERN = /^[a-z][a-z0-9_]{0,31}$/;

export function providerLabel(name: string): string {
  return PROVIDER_LABELS[name] ?? name;
}

/**
 * The address that starts a provider round trip.
 *
 * The server decides what the trip means: the same route that signs a visitor
 * in binds the provider to the account instead when the request already
 * carries a session, and says which one happened on the way back.
 */
export function oauthStartUrl(provider: string, returnPath: string): string {
  return `/api/v1/login/oauth/${encodeURIComponent(provider)}/start?return=${encodeURIComponent(returnPath)}`;
}
