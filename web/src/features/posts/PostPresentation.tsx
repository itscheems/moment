import { Popover } from "@base-ui/react/popover";
import { type MouseEvent, type MouseEventHandler } from "react";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { SiteAvatar, siteDisplayName } from "../../components/SiteIdentity";
import { MarkdownContent } from "../../components/MarkdownContent";
import {
  ActionTooltip,
  ExactTimeTooltip,
  ShareMenu,
  SiteIdentityPreview,
} from "../../components/TimelineInteractions";
import { formatMomentDate } from "./dates";
import { OwnerPostActions } from "../owner/OwnerPostActions";
import { OwnerRepostAction } from "../owner/OwnerRepostAction";
import { ImageGrid } from "../media/ImageGrid";
import styles from "./PostPresentation.module.css";

export { SiteAvatar } from "../../components/SiteIdentity";

const interactiveSurfaceSelector = [
  "a",
  "button",
  "input",
  "textarea",
  "select",
  '[role="button"]',
  '[role="link"]',
  '[role="menu"]',
  '[role="menuitem"]',
  "[data-popup-open]",
].join(", ");

function longInTimeline(post: PublicPost): boolean {
  return (
    Array.from(post.contentMd).length > 480 ||
    post.contentMd.split(/\r?\n/).length > 9
  );
}

function PostLocation({
  location,
}: {
  location: NonNullable<PublicPost["location"]>;
}) {
  const name = location.name.trim();
  const address = location.displayName.trim();
  const hasDetails = address !== "" && address !== name;

  if (!hasDetails) {
    return (
      <p aria-label={`地点：${name}`} className={styles.postLocation}>
        <Icon name="location" />
        <span>{name}</span>
      </p>
    );
  }

  return (
    <Popover.Root>
      <Popover.Trigger
        aria-label={`地点：${name}。查看详细地址`}
        className={`${styles.postLocation} ${styles.postLocationTrigger}`}
        closeDelay={120}
        delay={280}
        onClick={(event) => event.stopPropagation()}
        openOnHover
      >
        <Icon name="location" />
        <span>{name}</span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          className={styles.locationPositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="bottom"
          sideOffset={5}
        >
          <Popover.Popup
            className={styles.locationPopup}
            initialFocus={false}
            onClick={(event) => event.stopPropagation()}
          >
            <Popover.Title className={styles.locationTitle}>
              {name}
            </Popover.Title>
            <Popover.Description className={styles.locationAddress}>
              {address}
            </Popover.Description>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
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
  contentHtml,
  onReply,
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
  contentHtml?: string;
  onReply?: MouseEventHandler<HTMLButtonElement>;
}) {
  const { navigate } = useNavigation();
  const title = siteDisplayName(site);
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const collapsed = !detail && longInTimeline(post);
  const longDetail = detail && longInTimeline(post);
  const detailHref = `/m/${encodeURIComponent(post.id)}`;

  const openFromSurface = (event: MouseEvent<HTMLElement>) => {
    if (
      detail ||
      (event.target instanceof Element &&
        event.target.closest(interactiveSurfaceSelector))
    ) {
      return;
    }
    navigate(detailHref);
  };

  const avatar = (
    <SiteIdentityPreview
      site={site}
      children={
        <InternalLink
          aria-label={`查看 ${title} 的个人资料`}
          className={styles.identityLink}
          href={`/@${site.username}`}
        >
          <SiteAvatar eager={detail} site={site} />
        </InternalLink>
      }
    />
  );
  const pinLabel = pinned ? (
    <div className={styles.pinnedLabel}>
      <Icon name="pin" />
      <span>已置顶</span>
    </div>
  ) : null;
  const repostLabel = post.repostedAt ? (
    <div className={styles.repostedLabel}>
      <Icon name="repost" />
      <span>已转发</span>
    </div>
  ) : null;
  const byline = (
    <header className={styles.byline}>
      <div className={styles.bylineIdentity}>
        <SiteIdentityPreview
          site={site}
          children={
            <InternalLink
              className={styles.nameLink}
              href={`/@${site.username}`}
            >
              <strong>{site.displayName}</strong>
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
  );
  const share = (align: "start" | "end") => (
    <ShareMenu
      align={align}
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
        </button>
      }
    />
  );

  if (detail) {
    return (
      <article
        aria-label={`${title}发布的 Moment，${published.absolute}`}
        className={`${styles.post} ${styles.detail} ${fresh ? styles.fresh : ""}`}
        data-post-id={post.id}
        tabIndex={-1}
      >
        <div className={styles.detailIdentity}>
          <div className={styles.avatarColumn}>{avatar}</div>
          <div className={styles.detailHeading}>
            {repostLabel}
            {pinLabel}
            {byline}
          </div>
        </div>
        <div
          className={`${styles.detailBody} ${longDetail ? styles.detailLong : ""}`}
          data-content-density={longDetail ? "long" : "short"}
        >
          <MarkdownContent
            className={styles.markdown}
            html={contentHtml ?? post.contentHtml}
            mentionUsername={site.username}
          />
          {post.location ? <PostLocation location={post.location} /> : null}
          <ImageGrid media={post.media} />
          <p className={styles.publishedLine}>
            <span>发布于</span>
            <time dateTime={post.publishedAt}>{published.absolute}</time>
          </p>
          {post.commentCount > 0 ? (
            <a className={styles.detailStats} href="#comments">
              <strong>{post.commentCount}</strong>
              <span>条回复</span>
            </a>
          ) : null}
          <div className={styles.postActions}>
            <ActionTooltip label="回复">
              <button
                aria-label="回复这条 Moment"
                className={`${styles.postAction} ${styles.replyAction}`}
                onClick={onReply}
                type="button"
              >
                <Icon name="reply" />
              </button>
            </ActionTooltip>
            {owner ? <OwnerRepostAction post={post} /> : null}
            {share("start")}
          </div>
        </div>
      </article>
    );
  }

  return (
    <article
      aria-label={`${title}发布的 Moment，${published.absolute}`}
      className={`${styles.post} ${detail ? styles.detail : ""} ${fresh ? styles.fresh : ""}`}
      data-post-id={post.id}
      onClick={openFromSurface}
      tabIndex={-1}
    >
      <InternalLink
        aria-label="打开这条 Moment 详情"
        className={styles.surfaceLink}
        data-focus-key={`${post.id}:surface-link`}
        href={detailHref}
      >
        <span className={styles.srOnly}>打开这条 Moment 详情</span>
      </InternalLink>
      <div className={styles.avatarColumn}>{avatar}</div>
      <div className={styles.postMain}>
        {repostLabel}
        {pinLabel}
        {byline}
        <MarkdownContent
          className={`${styles.markdown} ${collapsed ? styles.collapsed : ""}`}
          html={contentHtml ?? post.contentHtml}
          mentionUsername={site.username}
        />
        {post.location ? <PostLocation location={post.location} /> : null}
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
        <div className={styles.postActions}>
          <ActionTooltip label="回复">
            {onReply ? (
              <button
                aria-label="回复这条 Moment"
                className={`${styles.postAction} ${styles.replyAction}`}
                data-focus-key={`${post.id}:reply`}
                onClick={onReply}
                type="button"
              >
                <Icon name="reply" />
                {post.commentCount > 0 ? (
                  <span className={styles.actionCount}>
                    {post.commentCount}
                  </span>
                ) : null}
              </button>
            ) : (
              <InternalLink
                aria-label={`打开完整 Moment${post.commentCount > 0 ? `，${post.commentCount} 条回复` : ""}`}
                className={`${styles.postAction} ${styles.replyAction}`}
                data-focus-key={`${post.id}:open-post`}
                href={detailHref}
              >
                <Icon name="reply" />
                {post.commentCount > 0 ? (
                  <span className={styles.actionCount}>
                    {post.commentCount}
                  </span>
                ) : null}
              </InternalLink>
            )}
          </ActionTooltip>
          {owner ? <OwnerRepostAction post={post} /> : null}
          {share("end")}
        </div>
      </div>
    </article>
  );
}
