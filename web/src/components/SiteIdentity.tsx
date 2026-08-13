import type { SiteIdentity } from "../api/public";

import styles from "./SiteIdentity.module.css";

export function siteHandle(site: SiteIdentity): string {
  return `@${site.username}`;
}

export function SiteAvatar({
  site,
  eager = false,
}: {
  site: SiteIdentity;
  eager?: boolean;
}) {
  const title = site.displayName.trim() || site.title.trim() || "Moment";
  if (site.avatarMediaId) {
    return (
      <img
        alt=""
        className={styles.avatar}
        decoding="async"
        height="44"
        loading={eager ? "eager" : "lazy"}
        src={`/api/v1/media/${encodeURIComponent(site.avatarMediaId)}/preview`}
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
      src={`/api/v1/media/${encodeURIComponent(site.coverMediaId)}/preview`}
    />
  );
}
