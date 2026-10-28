import { AccountName } from "../../components/AccountName";
import { commentDetailPath } from "../../app/routes";
import { MapCard } from "../maps/MapCard";
import type { MapViewport } from "../maps/MapCanvas";
import type { MapCardData } from "../maps/types";
import { AccountIdentityPreview } from "../../components/AccountIdentityPreview";
import { postAuthorSite } from "../../components/SiteIdentity";
import { Popover } from "@base-ui/react/popover";
import {
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type MouseEventHandler,
} from "react";

import {
  isArticlePost,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
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
import { timelineInstant } from "../timeline/timelineItems";
import { PostActions } from "./PostActions";
import { RepostAction } from "./RepostAction";
import { ArticleCard, QuotedArticle } from "../article/ArticleCard";
import { LinkCard } from "../links/LinkCard";
import { postShareCard } from "../share/shareCardInput";
import type { ShareCardSource } from "../share/shareCard";
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

function mapViewKey(postId: string, card: MapCardData): string {
  return JSON.stringify([postId, card]);
}

function weightedTextLength(text: string): number {
  let weight = 0;
  // Count visible text instead of Markdown syntax. Common Latin and
  // punctuation ranges use one unit; CJK and other symbols use two.
  for (const character of text) {
    const codePoint = character.codePointAt(0)!;
    weight +=
      codePoint <= 0x10ff ||
      (codePoint >= 0x2000 && codePoint <= 0x200d) ||
      (codePoint >= 0x2010 && codePoint <= 0x201f) ||
      (codePoint >= 0x2032 && codePoint <= 0x2037)
        ? 1
        : 2;
  }
  return weight;
}

function timelinePreview(post: PublicPost): {
  long: boolean;
  html: string;
  clipped: boolean;
} {
  // An article keeps its body out of `content_md`, so there is no Markdown to
  // measure here. Reading as short is deliberate: the timeline shape an
  // article deserves is its own change, and until then the collapse control
  // would fold a card it was never measured against.
  if (post.contentMd === null)
    return { long: false, html: post.contentHtml, clipped: false };
  const body = new DOMParser().parseFromString(
    post.contentHtml,
    "text/html",
  ).body;
  const blocks = Array.from(body.children);
  let weight = 0;
  for (const [index, block] of blocks.entries()) {
    weight += (index > 0 ? 2 : 0) + weightedTextLength(block.textContent ?? "");
  }
  if (blocks.length === 0) weight = weightedTextLength(body.textContent ?? "");
  if (weight <= 280)
    return { long: false, html: post.contentHtml, clipped: false };

  const visibleBlocks: string[] = [];
  let previewWeight = 0;
  for (const block of blocks) {
    const nextWeight =
      previewWeight +
      (visibleBlocks.length > 0 ? 2 : 0) +
      weightedTextLength(block.textContent ?? "");
    if (visibleBlocks.length > 0 && nextWeight > 280) break;
    visibleBlocks.push(block.outerHTML);
    previewWeight = nextWeight;
  }
  return {
    long: true,
    html: visibleBlocks.join("") || post.contentHtml,
    clipped: previewWeight > 280 || blocks.length === 0,
  };
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
  href,
  actions,
  commentsId = "comments",
  conversation = false,
  connected = false,
  connectedFrom = false,
  shareCard,
  bylineContext,
  replyControl,
  navigable = true,
  embedded = false,
  mediaViewer = false,
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
  href?: string;
  actions?: import("react").ReactNode;
  commentsId?: string;
  conversation?: boolean;
  connected?: boolean;
  connectedFrom?: boolean;
  shareCard?: ShareCardSource;
  bylineContext?: import("react").ReactNode;
  replyControl?: import("react").ReactNode;
  navigable?: boolean;
  embedded?: boolean;
  mediaViewer?: boolean;
}) {
  const { navigate } = useNavigation();
  const [expandedPostId, setExpandedPostId] = useState<string | null>(null);
  const mapViews = useRef(new Map<string, MapViewport>());
  const rememberMapView =
    (postId: string, card: MapCardData) => (viewport: MapViewport) => {
      mapViews.current.set(mapViewKey(postId, card), viewport);
    };
  site = postAuthorSite(post, site);
  const title = siteDisplayName(site);
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const preview = useMemo(
    () => timelinePreview(post),
    [post.contentHtml, post.contentMd],
  );
  const collapsed =
    !detail && !conversation && preview.long && expandedPostId !== post.id;
  const previewHtml = collapsed ? preview.html : post.contentHtml;
  const linkCard =
    post.linkCards.length > 0 ? <LinkCard card={post.linkCards[0]} /> : null;
  const detailHref = href ?? `/m/${encodeURIComponent(post.id)}`;
  const timelineKey = `${post.id}:${post.timelineItemKind}`;
  const useTimelineDensity = (timelineDensity || conversation) && !detail;

  const openFromSurface = (event: MouseEvent<HTMLElement>) => {
    if (
      detail ||
      !navigable ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    if (window.getSelection()?.toString()) return;
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

  const avatar = site.username ? (
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
  ) : (
    <span className={styles.identityLink}>
      <SiteAvatar eager={detail} site={site} />
    </span>
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
        {site.username ? (
          <AccountIdentityPreview
            author={post.author ?? { userId: null, username: null }}
            children={
              <InternalLink
                className={styles.nameLink}
                href={siteProfileHref(site)}
              >
                <strong>
                  <AccountName identity={site} />
                </strong>
              </InternalLink>
            }
          />
        ) : (
          <strong>
            <AccountName identity={site} />
          </strong>
        )}
        {site.username ? (
          <InternalLink
            className={styles.usernameLink}
            href={siteProfileHref(site)}
          >
            @{site.username}
          </InternalLink>
        ) : null}
        {bylineContext}
        {!detail ? (
          <>
            <span className={styles.bylineTime}>
              <span aria-hidden="true">·</span>
              <ExactTimeTooltip
                exact={published.absolute}
                children={
                  navigable ? (
                    <InternalLink
                      className={styles.timeLink}
                      aria-label={
                        post.commentPostId
                          ? "查看" + title + "的评论详情"
                          : undefined
                      }
                      data-focus-key={`${timelineKey}:relative-time`}
                      href={detailHref}
                    >
                      <time dateTime={post.publishedAt}>
                        {published.relative}
                      </time>
                    </InternalLink>
                  ) : (
                    <time dateTime={post.publishedAt}>
                      {published.relative}
                    </time>
                  )
                }
              />
            </span>
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
      {actions ??
        (canPublish ? (
          <PostActions
            onDeleted={onDeleted}
            onSiteChanged={onSiteChanged}
            post={post}
            site={site}
          />
        ) : null)}
    </header>
  );
  const share = (align: "start" | "end") => (
    <ShareMenu
      align={align}
      baseUrl={site.publicBaseUrl}
      card={
        shareCard ??
        (async () => {
          const input = postShareCard(post, site);
          if (post.mapCard)
            input.mapViewport = mapViews.current.get(
              mapViewKey(post.id, post.mapCard),
            );
          const quoted = post.quotedPost;
          if (input.quote && quoted && !quoted.unavailable && quoted.mapCard)
            input.quote.mapViewport = mapViews.current.get(
              mapViewKey(quoted.id, quoted.mapCard),
            );
          return input;
        })
      }
      href={detailHref}
      title={`${title} 的 Moment`}
      trigger={
        <button
          data-post-action="share"
          aria-label={
            post.commentPostId && !detail
              ? "分享 " + title + " 的评论"
              : "分享这条 Moment"
          }
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
        if (window.getSelection()?.toString()) return;
        const interactive =
          event.target instanceof Element
            ? event.target.closest(interactiveSurfaceSelector)
            : null;
        if (interactive && interactive !== event.currentTarget) return;
        navigate(
          quoted.commentPostId
            ? commentDetailPath(quoted.commentPostId, quoted.id)
            : `/m/${encodeURIComponent(quoted.id)}`,
        );
      }}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          navigate(
            quoted.commentPostId
              ? commentDetailPath(quoted.commentPostId, quoted.id)
              : `/m/${encodeURIComponent(quoted.id)}`,
          );
        }
      }}
      role="link"
      tabIndex={0}
    >
      <header className={styles.quoteByline}>
        <SiteAvatar site={postAuthorSite(quoted, site)} />
        <strong>
          <AccountName identity={postAuthorSite(quoted, site)} />
        </strong>
        {postAuthorSite(quoted, site).username ? (
          <span>@{postAuthorSite(quoted, site).username}</span>
        ) : null}
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
      {quoted.mapCard ? (
        <MapCard
          card={quoted.mapCard}
          preview
          onViewport={rememberMapView(quoted.id, quoted.mapCard)}
        />
      ) : null}
      {quoted.location ? <PostLocation location={quoted.location} /> : null}
      {quoted.article ? null : (
        <ImageGrid
          media={quoted.media}
          source={{
            postId: quoted.commentPostId ?? quoted.id,
            commentId: quoted.commentPostId ? quoted.id : undefined,
          }}
        />
      )}
    </div>
  ) : null;

  if (detail) {
    return (
      <article
        aria-label={
          post.commentPostId && !detail
            ? `${title}的回复`
            : `${title}发布的 Moment，${published.absolute}`
        }
        className={`${styles.post} ${styles.detail} ${mediaViewer ? styles.mediaDetail : ""} ${conversation ? styles.conversation : ""} ${fresh ? styles.fresh : ""}`}
        data-connected={connected || undefined}
        data-connected-from={connectedFrom || undefined}
        data-post-id={post.id}
        data-timeline-item-key={timelineKey}
        data-timeline-at={timelineInstant(post)}
        tabIndex={-1}
      >
        {pinLabel}
        {repostLabel}
        <div className={styles.detailIdentity}>
          <div className={styles.avatarColumn}>{avatar}</div>
          <div className={styles.detailHeading}>{byline}</div>
        </div>
        <div className={styles.detailBody}>
          {post.contentHtml ? (
            <MarkdownBody
              className={styles.markdown}
              html={post.contentHtml}
              searchQuery={searchQuery}
            />
          ) : null}
          {post.mapCard ? (
            <MapCard
              card={post.mapCard}
              onViewport={rememberMapView(post.id, post.mapCard)}
            />
          ) : null}
          {linkCard}
          {post.location ? <PostLocation location={post.location} /> : null}
          {/* See the timeline branch: an article's pictures are already in its
              body, so there is no attachment grid to draw. */}
          {isArticlePost(post) || mediaViewer ? null : (
            <ImageGrid
              eagerVideo
              media={post.media}
              source={{
                postId: post.commentPostId ?? post.id,
                commentId: post.commentPostId ? post.id : undefined,
              }}
            />
          )}
          {quoteCard}
          <p className={styles.publishedLine}>
            {!mediaViewer ? <span>发布于</span> : null}
            <time dateTime={post.publishedAt}>{published.absolute}</time>
            {hidden ? (
              <span className={styles.hiddenNote}>
                <Icon name="audience-only-me" />
                仅自己可见
              </span>
            ) : null}
          </p>
          {!mediaViewer && post.commentCount > 0 ? (
            <a className={styles.detailStats} href={"#" + commentsId}>
              <strong>{post.commentCount}</strong>
              <span>条回复</span>
            </a>
          ) : null}
          <div data-post-actions="" className={styles.postActions}>
            <ActionTooltip label="回复">
              <button
                data-post-action="reply"
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
            {canPublish || post.commentPostId ? (
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
      aria-label={
        post.commentPostId && !detail
          ? `${title}的回复`
          : `${title}发布的 Moment，${published.absolute}`
      }
      className={`${styles.post} ${conversation ? styles.conversation : ""} ${useTimelineDensity ? styles.timelineDensity : ""} ${fresh ? styles.fresh : ""}`}
      data-embedded={embedded || undefined}
      data-connected={connected || undefined}
      data-connected-from={connectedFrom || undefined}
      data-post-id={post.id}
      data-timeline-item-key={timelineKey}
      data-timeline-at={timelineInstant(post)}
      onClick={openFromSurface}
      tabIndex={-1}
    >
      {navigable ? (
        <InternalLink
          aria-label="打开这条 Moment 详情"
          className={styles.surfaceLink}
          data-focus-key={`${timelineKey}:surface-link`}
          href={detailHref}
        >
          <span className={styles.srOnly}>打开这条 Moment 详情</span>
        </InternalLink>
      ) : null}
      {pinLabel}
      {repostLabel}
      <div className={styles.avatarColumn}>{avatar}</div>
      <div className={styles.postMain}>
        {byline}
        {/* An article leads with its cover, title and excerpt instead of its
            prose: the body is not in a list response at all, and a card is
            what the reader is choosing between here. */}
        {isArticlePost(post) ? (
          <ArticleCard post={post} />
        ) : (
          <MarkdownBody
            className={`${styles.markdown} ${collapsed && preview.clipped ? styles.clippedPreview : ""}`}
            html={previewHtml}
            searchQuery={searchQuery}
          />
        )}
        {collapsed ? (
          <button
            className={styles.readMore}
            data-focus-key={`${timelineKey}:read-more`}
            onClick={(event) => {
              event.stopPropagation();
              setExpandedPostId(post.id);
            }}
            type="button"
          >
            显示更多
          </button>
        ) : null}
        {post.mapCard ? (
          <MapCard
            card={post.mapCard}
            preview
            onViewport={rememberMapView(post.id, post.mapCard)}
          />
        ) : null}
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
            source={{
              postId: post.commentPostId ?? post.id,
              commentId: post.commentPostId ? post.id : undefined,
            }}
          />
        )}
        {quoteCard}
        <div data-post-actions="" className={styles.postActions}>
          {replyControl !== undefined ? (
            replyControl
          ) : (
            <ActionTooltip label="回复">
              {onReply ? (
                <button
                  data-post-action="reply"
                  aria-label={
                    post.commentPostId ? "回复 " + title : "回复这条 Moment"
                  }
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
          )}
          {canPublish || post.commentPostId ? (
            <RepostAction onChanged={onReposted} post={post} />
          ) : null}
          {share("end")}
        </div>
      </div>
    </article>
  );
}
