import { useRef, type ReactNode } from "react";
import { TocProvider } from "@content-notion/components/nodes/toc-node/context/toc-context";
import { StaticArticleOutline } from "./ArticleOutline";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import styles from "./ArticleSheet.module.css";

/** The same paper surrounds an editable document, its preview, and a publication. */
export function ArticleSheet({
  cover,
  title,
  byline,
  children,
  className = "",
  outline,
}: {
  cover?: ReactNode;
  title: ReactNode;
  byline: ReactNode;
  children: ReactNode;
  className?: string;
  outline?: ReactNode;
}) {
  const body = useRef<HTMLDivElement>(null);
  return (
    <TocProvider>
      <div className={`${styles.sheet} ${className}`} data-article-sheet>
        {cover}
        <div className={styles.heading}>{title}</div>
        <div className={styles.byline}>{byline}</div>
        <div ref={body} className={styles.body}>
          {children}
        </div>
        {outline ?? <StaticArticleOutline container={body} />}
      </div>
    </TocProvider>
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
