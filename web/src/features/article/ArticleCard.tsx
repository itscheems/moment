import type { PublicArticle, PublicArticlePost } from "../../api/public";
import type { ShareCardBlock } from "../share/shareCard";
import { articleCoverUrl } from "./articleApi";
import styles from "./Article.module.css";

/**
 * An article as a shared picture reads it.
 *
 * The share card is drawn from blocks, and a moment's come from parsing its
 * rendered HTML. An article in a list has no rendered HTML -- the server left
 * it behind with the body -- so parsing would draw an empty card. The title
 * and the excerpt are what the card is for, and the server already wrote both.
 */
export function articleShareBlocks(article: PublicArticle): ShareCardBlock[] {
  const blocks: ShareCardBlock[] = [
    { kind: "text", text: article.title, heading: 1 },
  ];
  if (article.excerpt.trim()) {
    blocks.push({ kind: "text", text: article.excerpt });
  }
  return blocks;
}

/**
 * An article as the timeline shows it: a cover, a title and an excerpt inside
 * one bordered card, in place of the prose a moment would print.
 *
 * The card carries no link of its own. It sits inside the post surface, which
 * already holds the focusable overlay that opens the detail page and the click
 * handling that follows it; a second anchor over the same target would give a
 * reader two stops for one destination and a nested link to the same place.
 *
 * The body is deliberately absent. An article's `content_html` is empty in a
 * list -- the server leaves the rendering behind with the body it came from --
 * so there is nothing here to truncate, and the excerpt is the summary the
 * server already made.
 */
export function ArticleCard({ post }: { post: PublicArticlePost }) {
  const { title, excerpt, cover } = post.article;
  const badge = (
    <span className={styles.badge}>
      <span className={styles.srOnly}>类型：</span>文章
    </span>
  );

  return (
    <div className={styles.card}>
      {cover ? (
        <div className={styles.cover}>
          {/* The geometry is authoritative and arrives with the card, so the
              browser reserves the box before the bytes do. */}
          <img
            alt=""
            className={styles.coverImage}
            decoding="async"
            height={cover.height}
            loading="lazy"
            src={articleCoverUrl(cover)}
            width={cover.width}
          />
          <span className={styles.coverBadge}>{badge}</span>
        </div>
      ) : null}
      <div className={styles.meta}>
        {/* With no cover there is nothing for the pill to sit on, so it leads
            the text instead of disappearing. */}
        {cover ? null : <p className={styles.metaBadge}>{badge}</p>}
        <h2 className={styles.cardTitle}>{title}</h2>
        {excerpt.trim() ? (
          <p className={styles.cardExcerpt}>{excerpt}</p>
        ) : null}
      </div>
    </div>
  );
}
