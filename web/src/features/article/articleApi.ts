import { mediaPreviewUrl } from "../../api/mediaUrls";
import {
  PublicApiError,
  isArticlePost,
  isReadArticlePost,
  type PublicApi,
  type PublicArticleCover,
  type PublicArticlePost,
  type PublicReadArticlePost,
  type TimelineRequest,
} from "../../api/public";

/**
 * One page of articles, the same shape a timeline page has.
 *
 * It is a separate type only because every item is known to be an article:
 * a caller that has one never has to ask whether `post.article` is there.
 */
export interface ArticleListPage {
  items: PublicArticlePost[];
  nextCursor: string | null;
  prevCursor: string | null;
}

function notAnArticle(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

/**
 * Read the timeline with the moments left out.
 *
 * `kind` is fixed here rather than passed through: a caller asking this
 * function for moments is a caller that wanted `api.timeline` instead, and
 * leaving the choice open is how a reading list ends up quietly listing
 * everything. Every row is checked on the way out, because a page of articles
 * that contains a moment is a server answering a question nobody asked.
 */
export async function listArticles(
  api: PublicApi,
  request: Omit<TimelineRequest, "kind"> = {},
): Promise<ArticleListPage> {
  const page = await api.timeline({ ...request, kind: "article" });
  const items = page.items.map((post) => {
    if (!isArticlePost(post)) {
      throw notAnArticle("the article timeline answered with a moment");
    }
    return post;
  });
  return {
    items,
    nextCursor: page.nextCursor,
    prevCursor: page.prevCursor,
  };
}

/**
 * One article, read in full.
 *
 * A detail response carries the body and the HTML rendered from it; a row
 * that arrived with neither is a list projection that reached a reader, and
 * rendering half an article is worse than saying the read failed.
 */
export async function readArticle(
  api: PublicApi,
  id: string,
  signal?: AbortSignal,
): Promise<PublicReadArticlePost> {
  const post = await api.post(id, signal);
  if (!isArticlePost(post)) {
    throw notAnArticle("that post is a moment, not an article");
  }
  if (!isReadArticlePost(post)) {
    throw notAnArticle("that article arrived without its body");
  }
  return post;
}

/** Where an article is read. Articles share the post detail route. */
export function articleHref(id: string): string {
  return `/m/${encodeURIComponent(id)}`;
}

/**
 * The cover as a card shows it. The preview rendition is what every other
 * managed picture on a timeline loads, so a cover costs a reader no more than
 * a photo does.
 */
export function articleCoverUrl(cover: PublicArticleCover): string {
  return mediaPreviewUrl(cover.mediaId);
}
