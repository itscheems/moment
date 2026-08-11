import { type MouseEvent, type ReactNode, useState } from "react";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { formatMomentDate } from "./dates";
import { OwnerPostActions } from "../owner/OwnerPostActions";
import { ImageGrid } from "../media/ImageGrid";
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

function ShareMoment({
  baseUrl,
  href,
  title,
}: {
  baseUrl: string | null;
  href: string;
  title: string;
}) {
  const [status, setStatus] = useState("");

  const share = async () => {
    const url = new URL(
      href,
      baseUrl?.trim() || window.location.origin,
    ).toString();
    try {
      if (typeof navigator.share === "function") {
        await navigator.share({ title, url });
        setStatus("已分享");
      } else if (navigator.clipboard) {
        await navigator.clipboard.writeText(url);
        setStatus("链接已复制");
      } else {
        setStatus("可从地址栏复制链接");
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      setStatus("暂时无法分享");
    }
    window.setTimeout(() => setStatus(""), 2_000);
  };

  return (
    <>
      <button
        aria-label="分享这条 Moment"
        className={styles.postAction}
        onClick={() => void share()}
        type="button"
      >
        <Icon name="share" />
        <span>分享</span>
      </button>
      {status ? (
        <span className={styles.shareStatus} role="status">
          {status}
        </span>
      ) : (
        <span aria-live="polite" className={styles.srOnly} />
      )}
    </>
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
  context,
}: {
  post: PublicPost;
  site: SiteIdentity;
  detail?: boolean;
  now?: Date;
  owner?: boolean;
  fresh?: boolean;
  onDeleted?: (post: PublicPost) => void;
  context?: ReactNode;
}) {
  const { navigate } = useNavigation();
  const title = site.title.trim() || "Moment";
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const collapsed = !detail && longInTimeline(post);
  const detailHref = `/m/${encodeURIComponent(post.id)}`;

  const openFromSurface = (event: MouseEvent<HTMLElement>) => {
    if (
      detail ||
      (event.target instanceof Element &&
        event.target.closest("a, button, input, textarea, select"))
    ) {
      return;
    }
    navigate(detailHref);
  };

  return (
    <article
      aria-label={`${title}发布的 Moment，${published.absolute}`}
      className={`${styles.post} ${detail ? styles.detail : ""} ${fresh ? styles.fresh : ""}`}
      data-post-id={post.id}
      onClick={openFromSurface}
      tabIndex={-1}
    >
      <div className={styles.avatarColumn}>
        <SiteAvatar eager={detail} site={site} />
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
        {context}
        <div
          className={`${styles.markdown} ${collapsed ? styles.collapsed : ""}`}
          dangerouslySetInnerHTML={{ __html: post.contentHtml }}
        />
        <ImageGrid media={post.media} />
        {collapsed ? (
          <InternalLink
            className={styles.readMore}
            data-focus-key={`${post.id}:read-more`}
            href={detailHref}
          >
            阅读全文
          </InternalLink>
        ) : null}
        {detail ? (
          <p className={styles.publishedLine}>
            <span>发布于</span>
            <time dateTime={post.publishedAt}>{published.absolute}</time>
          </p>
        ) : null}
        <div className={styles.postActions}>
          {!detail ? (
            <InternalLink
              aria-label="打开完整 Moment"
              className={styles.postAction}
              data-focus-key={`${post.id}:open-post`}
              href={detailHref}
            >
              <Icon name="reply" />
              <span>回应</span>
            </InternalLink>
          ) : null}
          <ShareMoment
            baseUrl={site.publicBaseUrl}
            href={detailHref}
            title={`${title} 的 Moment`}
          />
        </div>
        {owner ? <OwnerPostActions onDeleted={onDeleted} post={post} /> : null}
      </div>
    </article>
  );
}
