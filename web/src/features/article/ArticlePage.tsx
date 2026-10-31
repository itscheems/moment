import { SourceMentionProvider } from "../../app/mentions";
import { LikeAction } from "../notifications/LikeAction";
import { useTranslation } from "react-i18next";
import { AccountName } from "../../components/AccountName";
import { accountVerification } from "../../components/accountVerification";
import { useOptionalSession } from "../auth/SessionProvider";
import { postAuthorSite } from "../../components/SiteIdentity";
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
import { PostActions } from "../posts/PostActions";
import { RepostAction } from "../posts/RepostAction";
import { formatMomentDate } from "../posts/dates";
import postStyles from "../posts/PostPresentation.module.css";
import { shareCardBlocks } from "../share/shareCard";
import {
  ArticleSheet,
  ArticleCover,
} from "../../components/content/ArticleSheet";
import styles from "./Article.module.css";

/** Share the document renderer while keeping detail and paper layouts distinct. */
function ArticlePageBody({
  post,
  site,
  now,
  canPublish = false,
  focusReading = false,
  layout = "document",
  showActions = true,
  onDeleted,
  onReposted,
  onSiteChanged,
  onReply,
}: {
  post: PublicReadArticlePost;
  site: SiteIdentity;
  now?: Date;
  canPublish?: boolean;
  focusReading?: boolean;
  layout?: "document" | "detail";
  showActions?: boolean;
  onDeleted?: (post: PublicPost) => void;
  onReposted?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  onReply?: MouseEventHandler<HTMLButtonElement>;
}) {
  const { t } = useTranslation();

  const session = useOptionalSession();
  canPublish = post.author?.userId
    ? session?.account?.userId === post.author.userId &&
      session?.account?.status === "active"
    : canPublish;
  site = postAuthorSite(post, site);
  const { title, cover } = post.article;
  const published = formatMomentDate(post.publishedAt, site.timezone, now);
  const hidden = post.visibility === "only_me";
  const siteName = siteDisplayName(site);
  const showDiscussionActions = showActions && !focusReading;

  return (
    <article
      aria-label={t("{{value1}}发布的文章：{{value2}}", {
        value1: siteName,
        value2: title,
      })}
      className={`${postStyles.post} ${postStyles.detail} ${styles.page} ${focusReading ? styles.focusReading : ""} ${layout === "detail" ? styles.detailLayout : ""}`}
      data-post-id={post.id}
      data-article-theme
      tabIndex={-1}
    >
      {post.timelineItemKind === "repost" ? (
        <div className={postStyles.repostedLabel}>
          <Icon name="repost" />
          <span>{t("已转发")}</span>
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
                  aria-label={t("查看 {{value1}} 的个人资料", {
                    value1: siteName,
                  })}
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
                    <strong>
                      <AccountName identity={site} />
                    </strong>
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
                  <ActionTooltip label={t("仅自己可见")}>
                    <span
                      aria-label={t("仅自己可见")}
                      className={postStyles.hiddenMark}
                      role="img"
                    >
                      <Icon name="audience-only-me" />
                    </span>
                  </ActionTooltip>
                ) : null}
              </p>
            </div>
            {canPublish && showActions ? (
              <div className={styles.bylineActions}>
                <PostActions
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
            verification: accountVerification(site),
            username: site.username,
            avatarUrl: site.avatarMediaId
              ? mediaPreviewUrl(site.avatarMediaId)
              : null,
            publishedLabel: t("文章摘录 · {{value1}}", {
              value1: published.absolute,
            }),
          }}
        />
        {focusReading ? (
          <footer aria-label={t("文章作者")} className={styles.readingFooter}>
            <SiteIdentityPreview
              site={site}
              children={
                <InternalLink
                  aria-label={t("查看 {{value1}} 的个人资料", {
                    value1: siteName,
                  })}
                  className={styles.readingAuthor}
                  href={siteProfileHref(site)}
                >
                  <span className={styles.readingAuthorAvatar}>
                    <SiteAvatar site={site} size={40} />
                  </span>
                  <span className={styles.readingAuthorText}>
                    <strong>
                      <AccountName identity={site} name={siteName} />
                    </strong>
                    <span>@{site.username}</span>
                  </span>
                </InternalLink>
              }
            />
          </footer>
        ) : null}
      </ArticleSheet>
      {showDiscussionActions && post.commentCount > 0 ? (
        <a
          className={`${postStyles.detailStats} ${styles.column}`}
          href="#comments"
        >
          <strong>{post.commentCount}</strong>
          <span>{t("条回复")}</span>
        </a>
      ) : null}
      {showDiscussionActions ? (
        <div className={`${postStyles.postActions} ${styles.column}`}>
          <ActionTooltip label={t("回复")}>
            <button
              aria-label={t("回复这篇文章{{value1}}", {
                value1:
                  post.commentCount > 0 ? `，${post.commentCount} 条回复` : "",
              })}
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
          <RepostAction onChanged={onReposted} post={post} />
          <LikeAction kind="post" id={post.id} />
          <ShareMenu
            align="start"
            baseUrl={site.publicBaseUrl}
            card={{
              displayName: site.displayName,
              verification: accountVerification(site),
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
                aria-label={t("分享这篇文章")}
                className={`${postStyles.postAction} ${postStyles.shareAction}`}
                type="button"
              >
                <Icon name="share" />
              </button>
            }
          />
        </div>
      ) : null}
    </article>
  );
}

export function ArticlePage(props: Parameters<typeof ArticlePageBody>[0]) {
  return (
    <SourceMentionProvider
      kind="post"
      id={props.post.id}
      revision={props.post.updatedAt}
    >
      <ArticlePageBody {...props} />
    </SourceMentionProvider>
  );
}
