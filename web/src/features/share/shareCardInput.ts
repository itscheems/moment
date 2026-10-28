import { accountVerification } from "../../components/accountVerification";
import {
  isArticlePost,
  type CommentContext,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
  type PublicApi,
} from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { postAuthorSite } from "../../components/SiteIdentity";
import { articleShareBlocks } from "../article/ArticleCard";
import { formatMomentDate } from "../posts/dates";
import {
  shareCardBlocks,
  shareCardLink,
  type ShareCardInput,
} from "./shareCard";

/** A comment uses post presentation without changing its persisted identity. */
export function commentAsPost(
  comment: PublicComment,
  original: Pick<PublicPost, "id" | "visibility">,
  count = comment.replyCount,
): PublicPost {
  return {
    id: comment.id,
    commentPostId: original.id,
    author: comment.author,
    kind: "moment",
    article: null,
    contentMd: comment.contentMd,
    contentHtml: comment.contentHtml,
    createdAt: comment.createdAt,
    updatedAt: comment.createdAt,
    publishedAt: comment.createdAt,
    repostedAt: null,
    timelineItemKind: "original",
    visibility: original.visibility,
    commentCount: count,
    media: comment.media,
    linkCards: [],
    location: comment.location,
  };
}

export function postShareCard(
  post: PublicPost,
  site: SiteIdentity,
): ShareCardInput {
  const author = postAuthorSite(post, site);
  return {
    displayName: author.displayName,
    verification: accountVerification(author),
    username: author.username,
    avatarUrl: author.avatarMediaId
      ? mediaPreviewUrl(author.avatarMediaId)
      : null,
    publishedLabel: formatMomentDate(post.publishedAt, site.timezone).absolute,
    blocks: isArticlePost(post)
      ? articleShareBlocks(post.article)
      : shareCardBlocks(post.contentHtml),
    media: isArticlePost(post) ? [] : post.media,
    mapCard: post.mapCard,
    location:
      post.location?.name?.trim() || post.location?.displayName?.trim() || null,
    link: post.linkCards.length ? shareCardLink(post.linkCards[0]) : null,
    ...(post.quotedPost
      ? post.quotedPost.unavailable
        ? { quoteUnavailable: true }
        : {
            quote: postShareCard(
              { ...post, ...post.quotedPost, quotedPost: null, linkCards: [] },
              site,
            ),
          }
      : {}),
  };
}

export function commentShareCard(
  context: CommentContext,
  original: PublicPost,
  site: SiteIdentity,
): ShareCardInput {
  const parent = context.replyTarget;
  const parentCard = postShareCard(
    parent ? commentAsPost(parent, original) : original,
    site,
  );
  if (parent?.replyTo)
    parentCard.replyTo = parent.replyTo.author.username
      ? "@" + parent.replyTo.author.username
      : parent.replyTo.author.displayName;
  return {
    ...postShareCard(
      commentAsPost(context.comment, original, context.directReplyCount),
      site,
    ),
    context: [parentCard],
    replyCount: context.directReplyCount,
  };
}

export async function loadCommentShareCard(
  api: PublicApi,
  original: PublicPost,
  id: string,
  site: SiteIdentity,
  signal?: AbortSignal,
): Promise<ShareCardInput> {
  return commentShareCard(
    await api.commentContext(original.id, id, signal),
    original,
    site,
  );
}
