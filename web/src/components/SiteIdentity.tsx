import { mediaPreviewUrl } from "../api/mediaUrls";

import styles from "./SiteIdentity.module.css";

/**
 * The parts of an identity that a rendered face needs.
 *
 * A site and an account both satisfy it, which is what lets one avatar, one
 * cover and one handle serve every profile on the instance instead of the
 * owner's alone.
 */
export interface IdentityFace {
  displayName: string;
  title?: string;
  avatarMediaId: string | null;
  coverMediaId: string | null;
}

// The `@` is presentation only. Profile URLs dropped it (`/username`), so never
// route a handle string into an `href` — use `siteProfileHref` for that.
export function siteHandle(site: { username: string }): string {
  return `@${site.username}`;
}

export function siteProfileHref(site: { username: string }): string {
  return `/${site.username}`;
}

export function siteDisplayName(site: IdentityFace): string {
  return site.displayName.trim() || site.title?.trim() || "Moment";
}

export function SiteAvatar({
  site,
  eager = false,
}: {
  site: IdentityFace;
  eager?: boolean;
}) {
  const title = siteDisplayName(site);
  if (site.avatarMediaId) {
    return (
      <img
        alt=""
        className={styles.avatar}
        decoding="async"
        height="44"
        loading={eager ? "eager" : "lazy"}
        src={mediaPreviewUrl(site.avatarMediaId)}
        width="44"
      />
    );
  }
  return (
    <span aria-hidden="true" className={styles.avatarFallback}>
      {Array.from(title)[0]?.toLocaleUpperCase() ?? "M"}
    </span>
  );
}

export function SiteCover({
  site,
  eager = false,
}: {
  site: IdentityFace;
  eager?: boolean;
}) {
  if (!site.coverMediaId)
    return <span aria-hidden="true" className={styles.coverFallback} />;
  return (
    <img
      alt=""
      className={styles.cover}
      decoding="async"
      loading={eager ? "eager" : "lazy"}
      src={mediaPreviewUrl(site.coverMediaId)}
    />
  );
}
