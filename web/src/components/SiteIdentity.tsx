import type { SiteIdentity } from "../api/public";
import { mediaPreviewUrl } from "../api/mediaUrls";

import styles from "./SiteIdentity.module.css";

// The `@` is presentation only. Profile URLs dropped it (`/username`), so never
// route a handle string into an `href` — use `siteProfileHref` for that.
export function siteHandle(site: SiteIdentity): string {
  return `@${site.username}`;
}

export function siteProfileHref(site: SiteIdentity): string {
  return `/${site.username}`;
}

export function siteDisplayName(site: SiteIdentity): string {
  return site.displayName.trim() || site.title.trim() || "Moment";
}

export function SiteAvatar({
  site,
  eager = false,
}: {
  site: SiteIdentity;
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
  site: SiteIdentity;
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
