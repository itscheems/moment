import type { MouseEventHandler } from "react";

import type {
  PublicPost,
  PublicReadArticlePost,
  SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { MarkdownContent } from "../../components/MarkdownContent";
import {
  SiteAvatar,
  siteDisplayName,
  siteProfileHref,
} from "../../components/SiteIdentity";
import {
  ActionTooltip,
  ExactTimeTooltip,
  ShareMenu,
  SiteIdentityPreview,
} from "../../components/TimelineInteractions";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { OwnerPostActions } from "../owner/OwnerPostActions";
import { OwnerRepostAction } from "../owner/OwnerRepostAction";
import { formatMomentDate } from "../posts/dates";
import postStyles from "../posts/PostPresentation.module.css";
import { shareCardBlocks } from "../share/shareCard";
import { articleCoverUrl } from "./articleApi";
import styles from "./Article.module.css";

/**
 * An article, read in full.
 *
 * The body is the HTML the server rendered from the stored document; nothing
 * here reads that document. An article has exactly one rendering and the
 * server owns it, which is also what keeps this surface free of an editor's
 * schema: a second renderer on this side would be a second answer to what a
 * body means, and the two would drift the first time the grammar grew.
 *
 * The chrome -- the identity line, the reply, repost and share row, the owner's
 * menu -- is the post detail page's own, reused class for class. An article is
 * a post, and a reader who has learned where the share button sits should not
 * have to learn it twice. Only the reading column is the article's: a longer
 * measure, a title that leads the page, and a cover that runs edge to edge.
 */
export function ArticlePage({
  post,
  site,
  now,
  owner = false,
  onDeleted,
  onReposted,
  onSiteChanged,
  onReply,
}: {
  post: PublicReadArticlePost;
  site: SiteIdentity;
  now?: Date;
  owner?: boolean;
  onDeleted?: (post: PublicPost) => void;
  onReposted?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  onReply?: MouseEventHandler<HTMLButtonElement>;
}) {
  const { title, cover } = post.article;
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const hidden = post.visibility === "only_me";
  const siteName = siteDisplayName(site);

  return (
    <article
      aria-label={`${siteName}发布的文章：${title}`}
      className={`${postStyles.post} ${postStyles.detail} ${styles.page}`}
      data-post-id={post.id}
      tabIndex={-1}
    >
      {post.timelineItemKind === "repost" ? (
        <div className={postStyles.repostedLabel}>
          <Icon name="repost" />
          <span>已转发</span>
        </div>
      ) : null}
      <header className={`${styles.column} ${styles.head}`}>
        <p className={styles.headBadge}>
          <span className={styles.badge}>
            <span className={styles.srOnly}>类型：</span>文章
          </span>
        </p>
        <h1 className={styles.title}>{title}</h1>
        {/* No lede. The excerpt is the opening of the body, so printing it
            above the body says the same sentence twice -- it earns its place
            on a card, where the body is not there to say it. */}
        <div className={styles.byline}>
          <SiteIdentityPreview
            site={site}
            children={
              <InternalLink
                aria-label={`查看 ${siteName} 的个人资料`}
                className={postStyles.identityLink}
                href={siteProfileHref(site)}
              >
                <SiteAvatar eager site={site} />
              </InternalLink>
            }
          />
          <div className={styles.bylineText}>
            <SiteIdentityPreview
              site={site}
              children={
                <InternalLink
                  className={postStyles.nameLink}
                  href={siteProfileHref(site)}
                >
                  <strong>{site.displayName}</strong>
                </InternalLink>
              }
            />
            <p className={styles.bylineMeta}>
              <InternalLink
                className={postStyles.usernameLink}
                href={siteProfileHref(site)}
              >
                @{site.username}
              </InternalLink>
              <span aria-hidden="true">·</span>
              <ExactTimeTooltip
                exact={published.absolute}
                children={
                  <time dateTime={post.publishedAt}>{published.absolute}</time>
                }
              />
              {hidden ? (
                <ActionTooltip label="仅自己可见">
                  <span
                    aria-label="仅自己可见"
                    className={postStyles.hiddenMark}
                    role="img"
                  >
                    <Icon name="audience-only-me" />
                  </span>
                </ActionTooltip>
              ) : null}
            </p>
          </div>
          {owner ? (
            <OwnerPostActions
              onDeleted={onDeleted}
              onSiteChanged={onSiteChanged}
              post={post}
              site={site}
            />
          ) : null}
        </div>
      </header>
      {cover ? (
        <figure className={styles.hero}>
          {/* Above the fold and never lazy, but still sized from the geometry
              the card carries so the title does not jump when it lands. */}
          <img
            alt=""
            className={styles.heroImage}
            decoding="async"
            height={cover.height}
            src={articleCoverUrl(cover)}
            width={cover.width}
          />
        </figure>
      ) : null}
      <MarkdownContent
        className={`${styles.column} ${styles.body}`}
        html={post.contentHtml}
      />
      {post.commentCount > 0 ? (
        <a
          className={`${postStyles.detailStats} ${styles.column}`}
          href="#comments"
        >
          <strong>{post.commentCount}</strong>
          <span>条回复</span>
        </a>
      ) : null}
      <div className={`${postStyles.postActions} ${styles.column}`}>
        <ActionTooltip label="回复">
          <button
            aria-label={`回复这篇文章${post.commentCount > 0 ? `，${post.commentCount} 条回复` : ""}`}
            className={`${postStyles.postAction} ${postStyles.replyAction}`}
            onClick={onReply}
            type="button"
          >
            <Icon name="reply" />
            {post.commentCount > 0 ? (
              <span className={postStyles.actionCount}>
                {post.commentCount}
              </span>
            ) : null}
          </button>
        </ActionTooltip>
        {owner ? (
          <OwnerRepostAction onChanged={onReposted} post={post} />
        ) : null}
        <ShareMenu
          align="start"
          baseUrl={site.publicBaseUrl}
          card={{
            displayName: site.displayName,
            username: site.username,
            avatarUrl: site.avatarMediaId
              ? mediaPreviewUrl(site.avatarMediaId)
              : null,
            publishedLabel: published.absolute,
            blocks: shareCardBlocks(post.contentHtml),
            // An article places its pictures inside its body, where the blocks
            // above already carry them in reading order. The grid a moment
            // shares is for pictures attached to a post, and an article has
            // none of those.
            media: [],
            location: null,
            link: null,
          }}
          href={`/m/${encodeURIComponent(post.id)}`}
          title={title}
          trigger={
            <button
              aria-label="分享这篇文章"
              className={`${postStyles.postAction} ${postStyles.shareAction}`}
              type="button"
            >
              <Icon name="share" />
            </button>
          }
        />
      </div>
    </article>
  );
}
