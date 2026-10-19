import { MapCard } from "../maps/MapCard";
import { AccountIdentityPreview } from "../../components/AccountIdentityPreview";
import { postAuthorSite } from "../../components/SiteIdentity";
import { Popover } from "@base-ui/react/popover";
import { type MouseEvent, type MouseEventHandler } from "react";

import {
  isArticlePost,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import {
  SiteAvatar,
  siteDisplayName,
  siteProfileHref,
} from "../../components/SiteIdentity";
import { MarkdownBody } from "../../components/content/MarkdownBody";
import {
  ActionTooltip,
  ExactTimeTooltip,
  ShareMenu,
} from "../../components/TimelineInteractions";
import { formatMomentDate } from "./dates";
import { PostActions } from "./PostActions";
import { RepostAction } from "./RepostAction";
import {
  ArticleCard,
  QuotedArticle,
  articleShareBlocks,
} from "../article/ArticleCard";
import { LinkCard } from "../links/LinkCard";
import { shareCardBlocks, shareCardLink } from "../share/shareCard";
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
  const body = post.contentMd;
  // An article keeps its body out of `content_md`, so there is no Markdown to
  // measure here. Reading as short is deliberate: the timeline shape an
  // article deserves is its own change, and until then the collapse control
  // would fold a card it was never measured against.
  if (body === null) return false;
  return Array.from(body).length > 480 || body.split(/\r?\n/).length > 9;
}

export function PostLocation({
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
  canPublish = false,
  fresh = false,
  onDeleted,
  onReposted,
  onSiteChanged,
  pinned = false,
  searchQuery,
  onReply,
  eagerVideo = false,
  compactTallVideo,
  timelineDensity = false,
}: {
  post: PublicPost;
  site: SiteIdentity;
  detail?: boolean;
  now?: Date;
  canPublish?: boolean;
  fresh?: boolean;
  onDeleted?: (post: PublicPost) => void;
  onReposted?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  pinned?: boolean;
  searchQuery?: string;
  onReply?: MouseEventHandler<HTMLButtonElement>;
  eagerVideo?: boolean;
  compactTallVideo?: boolean;
  timelineDensity?: boolean;
}) {
  const { navigate } = useNavigation();
  site = postAuthorSite(post, site);
  const title = siteDisplayName(site);
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const collapsed = !detail && longInTimeline(post);
  const linkCard =
    post.linkCards.length > 0 ? <LinkCard card={post.linkCards[0]} /> : null;
  const detailHref = `/m/${encodeURIComponent(post.id)}`;
  const timelineKey = `${post.id}:${post.timelineItemKind}`;
  const useTimelineDensity = timelineDensity && !detail;

  const openFromSurface = (event: MouseEvent<HTMLElement>) => {
    if (detail) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    // A portal puts its markup under the body and still leaves it a descendant
    // in the React tree, so what it is clicked on arrives here -- a dialog's
    // backdrop, a lightbox, the share preview. None of those are the card's own
    // surface, and dismissing one used to open the post underneath it. Whole-card
    // navigation answers only for what is actually inside the card.
    if (!event.currentTarget.contains(target)) return;
    if (target.closest(interactiveSurfaceSelector)) return;
    navigate(detailHref);
  };

  const avatar = (
    <AccountIdentityPreview
      author={post.author ?? { userId: null, username: null }}
      children={
        <InternalLink
          aria-label={`查看 ${title} 的个人资料`}
          className={styles.identityLink}
          href={siteProfileHref(site)}
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
  const hidden = post.visibility === "only_me";
  const repostLabel =
    post.timelineItemKind === "repost" ? (
      <div className={styles.repostedLabel}>
        <Icon name="repost" />
        <span>已转发</span>
      </div>
    ) : null;
  const byline = (
    <header className={`${styles.byline} ${detail ? styles.detailByline : ""}`}>
      <div className={styles.bylineIdentity}>
        <AccountIdentityPreview
          author={post.author ?? { userId: null, username: null }}
          children={
            <InternalLink
              className={styles.nameLink}
              href={siteProfileHref(site)}
            >
              <strong>{site.displayName}</strong>
            </InternalLink>
          }
        />
        <InternalLink
          className={styles.usernameLink}
          href={siteProfileHref(site)}
        >
          @{site.username}
        </InternalLink>
        {!detail ? (
          <>
            <span aria-hidden="true">·</span>
            <ExactTimeTooltip
              exact={published.absolute}
              children={
                <InternalLink
                  className={styles.timeLink}
                  data-focus-key={`${timelineKey}:relative-time`}
                  href={detailHref}
                >
                  <time dateTime={post.publishedAt}>{published.relative}</time>
                </InternalLink>
              }
            />
            {hidden ? (
              <ActionTooltip label="仅自己可见">
                <span
                  aria-label="仅自己可见"
                  className={styles.hiddenMark}
                  role="img"
                >
                  <Icon name="audience-only-me" />
                </span>
              </ActionTooltip>
            ) : null}
          </>
        ) : null}
      </div>
      {canPublish ? (
        <PostActions
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
      card={{
        displayName: site.displayName,
        username: site.username,
        avatarUrl: site.avatarMediaId
          ? mediaPreviewUrl(site.avatarMediaId)
          : null,
        publishedLabel: published.absolute,
        // An article's pictures belong to its body, and in a list its body has
        // not been rendered at all, so the card is built from the title and
        // the excerpt the server already wrote for exactly this purpose.
        blocks: isArticlePost(post)
          ? articleShareBlocks(post.article)
          : shareCardBlocks(post.contentHtml),
        media: isArticlePost(post) ? [] : post.media,
        location:
          post.location?.name?.trim() ||
          post.location?.displayName?.trim() ||
          post.mapCard?.places.map((place) => place.name).join(" · ") ||
          null,
        link:
          post.linkCards.length > 0 ? shareCardLink(post.linkCards[0]) : null,
      }}
      href={detailHref}
      title={`${title} 的 Moment`}
      trigger={
        <button
          aria-label="分享这条 Moment"
          className={`${styles.postAction} ${styles.shareAction}`}
          type="button"
        >
          <Icon name="share" />
        </button>
      }
    />
  );
  const quoted = post.quotedPost;
  const quoteCard = quoted?.unavailable ? (
    <div className={styles.quoteUnavailable} role="status">
      This post is unavailable.
    </div>
  ) : quoted ? (
    <div
      aria-label="打开被引用的 Moment"
      className={styles.quoteCard}
      onClick={(event) => {
        event.stopPropagation();
        const interactive =
          event.target instanceof Element
            ? event.target.closest(interactiveSurfaceSelector)
            : null;
        if (interactive && interactive !== event.currentTarget) return;
        navigate(`/m/${encodeURIComponent(quoted.id)}`);
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          navigate(`/m/${encodeURIComponent(quoted.id)}`);
        }
      }}
      role="link"
      tabIndex={0}
    >
      <header className={styles.quoteByline}>
        <SiteAvatar site={postAuthorSite(quoted, site)} />
        <strong>{postAuthorSite(quoted, site).displayName}</strong>
        <span>@{postAuthorSite(quoted, site).username}</span>
        <span>·</span>
        <time dateTime={quoted.publishedAt}>
          {formatMomentDate(quoted.publishedAt, site.timezone, now).relative}
        </time>
      </header>
      {quoted.article ? (
        // A quoted article is a card inside a card: its title and the run of
        // prose under it, never the article itself.
        <QuotedArticle article={quoted.article} />
      ) : (
        <MarkdownBody
          className={styles.quoteMarkdown}
          html={quoted.contentHtml}
        />
      )}
      {quoted.mapCard ? <MapCard card={quoted.mapCard} /> : null}
      {quoted.location ? <PostLocation location={quoted.location} /> : null}
      {quoted.article ? null : <ImageGrid media={quoted.media} />}
    </div>
  ) : null;

  if (detail) {
    return (
      <article
        aria-label={`${title}发布的 Moment，${published.absolute}`}
        className={`${styles.post} ${styles.detail} ${fresh ? styles.fresh : ""}`}
        data-post-id={post.id}
        data-timeline-item-key={timelineKey}
        tabIndex={-1}
      >
        {repostLabel}
        <div className={styles.detailIdentity}>
          <div className={styles.avatarColumn}>{avatar}</div>
          <div className={styles.detailHeading}>
            {pinLabel}
            {byline}
          </div>
        </div>
        <div className={styles.detailBody}>
          <MarkdownBody
            className={styles.markdown}
            html={post.contentHtml}
            searchQuery={searchQuery}
          />
          {post.mapCard ? <MapCard card={post.mapCard} /> : null}
          {linkCard}
          {post.location ? <PostLocation location={post.location} /> : null}
          {/* See the timeline branch: an article's pictures are already in its
              body, so there is no attachment grid to draw. */}
          {isArticlePost(post) ? null : (
            <ImageGrid eagerVideo media={post.media} />
          )}
          {quoteCard}
          <p className={styles.publishedLine}>
            <span>发布于</span>
            <time dateTime={post.publishedAt}>{published.absolute}</time>
            {hidden ? (
              <span className={styles.hiddenNote}>
                <Icon name="audience-only-me" />
                仅自己可见
              </span>
            ) : null}
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
                aria-label={`回复这条 Moment${post.commentCount > 0 ? `，${post.commentCount} 条回复` : ""}`}
                className={`${styles.postAction} ${styles.replyAction}`}
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
            </ActionTooltip>
            {canPublish ? (
              <RepostAction onChanged={onReposted} post={post} />
            ) : null}
            {share("start")}
          </div>
        </div>
      </article>
    );
  }

  return (
    <article
      aria-label={`${title}发布的 Moment，${published.absolute}`}
      className={`${styles.post} ${useTimelineDensity ? styles.timelineDensity : ""} ${fresh ? styles.fresh : ""}`}
      data-post-id={post.id}
      data-timeline-item-key={timelineKey}
      onClick={openFromSurface}
      tabIndex={-1}
    >
      <InternalLink
        aria-label="打开这条 Moment 详情"
        className={styles.surfaceLink}
        data-focus-key={`${timelineKey}:surface-link`}
        href={detailHref}
      >
        <span className={styles.srOnly}>打开这条 Moment 详情</span>
      </InternalLink>
      {repostLabel}
      <div className={styles.avatarColumn}>{avatar}</div>
      <div className={styles.postMain}>
        {pinLabel}
        {byline}
        {/* An article leads with its cover, title and excerpt instead of its
            prose: the body is not in a list response at all, and a card is
            what the reader is choosing between here. */}
        {isArticlePost(post) ? (
          <ArticleCard post={post} />
        ) : (
          <MarkdownBody
            className={`${styles.markdown} ${collapsed ? styles.collapsed : ""}`}
            html={post.contentHtml}
            searchQuery={searchQuery}
          />
        )}
        {collapsed ? (
          <InternalLink
            className={styles.readMore}
            data-focus-key={`${timelineKey}:read-more`}
            href={detailHref}
          >
            阅读全文
          </InternalLink>
        ) : null}
        {post.mapCard ? <MapCard card={post.mapCard} /> : null}
        {linkCard}
        {post.location ? <PostLocation location={post.location} /> : null}
        {/* An article's `media` is the pictures its body places, projected
            into the same rows a moment's attachments live in so that reference
            counting stays one predicate. They are not attachments, and a grid
            of them under the card would show the article twice. */}
        {isArticlePost(post) ? null : (
          <ImageGrid
            compactTallVideo={compactTallVideo ?? useTimelineDensity}
            eagerVideo={eagerVideo}
            media={post.media}
          />
        )}
        {quoteCard}
        <div className={styles.postActions}>
          <ActionTooltip label="回复">
            {onReply ? (
              <button
                aria-label="回复这条 Moment"
                className={`${styles.postAction} ${styles.replyAction}`}
                data-focus-key={`${timelineKey}:reply`}
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
                data-focus-key={`${timelineKey}:open-post`}
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
          {canPublish ? (
            <RepostAction onChanged={onReposted} post={post} />
          ) : null}
          {share("end")}
        </div>
      </div>
    </article>
  );
}
