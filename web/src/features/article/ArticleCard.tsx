import { useTranslation } from "react-i18next";
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

function ArticleBadge() {
  const { t } = useTranslation();

  return (
    <span className={styles.badge}>
      <span aria-hidden="true" className={styles.badgeLogo} />
      <span className={styles.srOnly}>{t("Type:")}</span>
      {t("Article")}
    </span>
  );
}

/**
 * QuotedArticle is the article shown inside somebody's quote.
 *
 * It is the same three things a card carries, one size down and without the
 * outer frame: the quote already has one, and nesting a second would read as
 * two posts rather than one quoting another.
 */
export function QuotedArticle({ article }: { article: PublicArticle }) {
  const { title, excerpt, cover } = article;
  return (
    <div className={styles.quoted}>
      {cover ? (
        <img
          alt=""
          className={styles.quotedCover}
          decoding="async"
          height={cover.height}
          loading="lazy"
          src={articleCoverUrl(cover)}
          width={cover.width}
        />
      ) : null}
      <p className={styles.quotedBadge}>
        <ArticleBadge />
      </p>
      <h3 className={styles.quotedTitle}>{title}</h3>
      {excerpt.trim() ? (
        <p className={styles.quotedExcerpt}>{excerpt}</p>
      ) : null}
    </div>
  );
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
          <span className={styles.coverBadge}>
            <ArticleBadge />
          </span>
        </div>
      ) : null}
      <div className={styles.meta}>
        <div className={styles.cardHeader}>
          {cover ? null : <ArticleBadge />}
          <h2 className={styles.cardTitle}>{title}</h2>
        </div>
        {excerpt.trim() ? (
          <p className={styles.cardExcerpt}>{excerpt}</p>
        ) : null}
      </div>
    </div>
  );
}
