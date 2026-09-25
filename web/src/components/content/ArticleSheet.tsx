import type { ReactNode } from "react";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import styles from "./ArticleSheet.module.css";

/** The same paper surrounds an editable document, its preview, and a publication. */
export function ArticleSheet({
  cover,
  title,
  byline,
  children,
  className = "",
}: {
  cover?: ReactNode;
  title: ReactNode;
  byline: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={`${styles.sheet} ${className}`} data-article-sheet>
      {cover}
      <div className={styles.heading}>{title}</div>
      <div className={styles.byline}>{byline}</div>
      <div className={styles.body}>{children}</div>
    </div>
  );
}

export function ArticleCover({
  mediaId,
  width,
  height,
  children,
}: {
  mediaId: string;
  width?: number;
  height?: number;
  children?: ReactNode;
}) {
  return (
    <figure className={styles.cover}>
      <img
        alt=""
        src={mediaPreviewUrl(mediaId)}
        width={width}
        height={height}
        decoding="async"
      />
      {children}
    </figure>
  );
}
