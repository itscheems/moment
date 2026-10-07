import type { MouseEventHandler } from "react";

import type {
  PublicPost,
  PublicReadArticlePost,
  SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { ArticleReadingContent } from "./reader";
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
import {
  ArticleSheet,
  ArticleCover,
} from "../../components/content/ArticleSheet";
import styles from "./Article.module.css";

/** Read the document using the same grammar, paper, and typography as writing. */
export function ArticlePage({
  post,
  site,
  now,
  owner = false,
  focusReading = false,
  onDeleted,
  onReposted,
  onSiteChanged,
  onReply,
}: {
  post: PublicReadArticlePost;
  site: SiteIdentity;
  now?: Date;
  owner?: boolean;
  focusReading?: boolean;
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
      data-article-theme
      tabIndex={-1}
    >
      {post.timelineItemKind === "repost" ? (
        <div className={postStyles.repostedLabel}>
          <Icon name="repost" />
          <span>已转发</span>
        </div>
      ) : null}
      <ArticleSheet
        enableReadingLayout={focusReading}
        cover={
          cover ? (
            <ArticleCover
              mediaId={cover.mediaId}
              width={cover.width}
              height={cover.height}
            />
          ) : null
        }
        title={<h1>{title}</h1>}
        byline={
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
                    <time dateTime={post.publishedAt}>
                      {published.absolute}
                    </time>
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
              <div className={styles.bylineActions}>
                <OwnerPostActions
                  showArticleEdit
                  onDeleted={onDeleted}
                  onSiteChanged={onSiteChanged}
                  post={post}
                  site={site}
                />
              </div>
            ) : null}
          </div>
        }
      >
        <ArticleReadingContent
          key={`${post.id}:${focusReading ? "focus" : "detail"}`}
          document={post.article.body}
          searchSlot={focusReading ? undefined : null}
          enableSearch
          enableAnnotations
          enableExcerptPanel={focusReading}
          enableReadingLayout={focusReading}
          showClearAllAnnotations={focusReading}
          media={post.article.bodyMedia}
          excerptSource={{
            title,
            displayName: siteName,
            username: site.username,
            avatarUrl: site.avatarMediaId
              ? mediaPreviewUrl(site.avatarMediaId)
              : null,
            publishedLabel: `文章摘录 · ${published.absolute}`,
          }}
        />
      </ArticleSheet>
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
