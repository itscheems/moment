import type { PublicPost, SiteIdentity } from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { formatMomentDate } from "./dates";
import { OwnerPostActions } from "../owner/OwnerPostActions";
import styles from "./PostPresentation.module.css";

export function SiteAvatar({
  site,
  eager = false,
}: {
  site: SiteIdentity;
  eager?: boolean;
}) {
  const title = site.title.trim() || "Moment";
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

function longInTimeline(post: PublicPost): boolean {
  return (
    Array.from(post.contentMd).length > 480 ||
    post.contentMd.split(/\r?\n/).length > 9
  );
}

export function PostPresentation({
  post,
  site,
  detail = false,
  now,
  owner = false,
  fresh = false,
  onDeleted,
}: {
  post: PublicPost;
  site: SiteIdentity;
  detail?: boolean;
  now?: Date;
  owner?: boolean;
  fresh?: boolean;
  onDeleted?: (post: PublicPost) => void;
}) {
  const title = site.title.trim() || "Moment";
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const collapsed = !detail && longInTimeline(post);
  const detailHref = `/m/${encodeURIComponent(post.id)}`;

  return (
    <article
      aria-label={`${title}发布的 Moment，${published.absolute}`}
      className={`${styles.post} ${detail ? styles.detail : ""} ${fresh ? styles.fresh : ""}`}
      data-post-id={post.id}
      tabIndex={-1}
    >
      <div className={styles.timeColumn}>
        <SiteAvatar eager={detail} site={site} />
        <time
          aria-label={published.absolute}
          className={styles.dateMark}
          dateTime={post.publishedAt}
          title={published.absolute}
        >
          {published.dateMark}
        </time>
      </div>
      <div className={styles.postMain}>
        <header className={styles.byline}>
          <strong>{title}</strong>
          <span aria-hidden="true">·</span>
          <InternalLink
            data-focus-key={`${post.id}:relative-time`}
            href={detailHref}
            title={published.absolute}
          >
            <time dateTime={post.publishedAt}>{published.relative}</time>
          </InternalLink>
        </header>
        <div
          className={`${styles.markdown} ${collapsed ? styles.collapsed : ""}`}
          dangerouslySetInnerHTML={{ __html: post.contentHtml }}
        />
        {collapsed ? (
          <InternalLink
            className={styles.readMore}
            data-focus-key={`${post.id}:read-more`}
            href={detailHref}
          >
            阅读全文
          </InternalLink>
        ) : !detail ? (
          <InternalLink
            className={styles.openPost}
            data-focus-key={`${post.id}:open-post`}
            href={detailHref}
          >
            打开完整 Moment
          </InternalLink>
        ) : null}
        {detail ? (
          <p className={styles.publishedLine}>
            <span>发布于</span>
            <time dateTime={post.publishedAt}>{published.absolute}</time>
          </p>
        ) : null}
        {owner ? <OwnerPostActions onDeleted={onDeleted} post={post} /> : null}
      </div>
    </article>
  );
}
