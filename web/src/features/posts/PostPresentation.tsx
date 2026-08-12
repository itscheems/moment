import { type MouseEvent, type ReactNode } from "react";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { SiteAvatar } from "../../components/SiteIdentity";
import {
  ExactTimeTooltip,
  ShareMenu,
  SiteIdentityPreview,
} from "../../components/TimelineInteractions";
import { formatMomentDate } from "./dates";
import { OwnerPostActions } from "../owner/OwnerPostActions";
import { ImageGrid } from "../media/ImageGrid";
import styles from "./PostPresentation.module.css";

export { SiteAvatar } from "../../components/SiteIdentity";

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
  onSiteChanged,
  pinned = false,
  context,
}: {
  post: PublicPost;
  site: SiteIdentity;
  detail?: boolean;
  now?: Date;
  owner?: boolean;
  fresh?: boolean;
  onDeleted?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  pinned?: boolean;
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
        <SiteIdentityPreview
          site={site}
          children={
            <InternalLink
              aria-label={`查看 ${title} 的个人资料`}
              className={styles.identityLink}
              href="/profile"
            >
              <SiteAvatar eager={detail} site={site} />
            </InternalLink>
          }
        />
      </div>
      <div className={styles.postMain}>
        {pinned ? (
          <div className={styles.pinnedLabel}>
            <Icon name="pin" />
            <span>已置顶</span>
          </div>
        ) : null}
        <header className={styles.byline}>
          <div className={styles.bylineIdentity}>
            <SiteIdentityPreview
              site={site}
              children={
                <InternalLink className={styles.nameLink} href="/profile">
                  <strong>{title}</strong>
                </InternalLink>
              }
            />
            <span aria-hidden="true">·</span>
            <ExactTimeTooltip
              exact={published.absolute}
              children={
                <InternalLink
                  data-focus-key={`${post.id}:relative-time`}
                  href={detailHref}
                >
                  <time dateTime={post.publishedAt}>{published.relative}</time>
                </InternalLink>
              }
            />
          </div>
          {owner ? (
            <OwnerPostActions
              onDeleted={onDeleted}
              onSiteChanged={onSiteChanged}
              post={post}
              site={site}
            />
          ) : null}
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
          <ShareMenu
            baseUrl={site.publicBaseUrl}
            href={detailHref}
            title={`${title} 的 Moment`}
            trigger={
              <button
                aria-label="分享这条 Moment"
                className={styles.postAction}
                type="button"
              >
                <Icon name="share" />
                <span>分享</span>
              </button>
            }
          />
        </div>
      </div>
    </article>
  );
}
