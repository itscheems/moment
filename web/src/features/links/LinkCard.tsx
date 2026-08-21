import { useCallback, useState } from "react";

import type { PublicLinkCard } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import styles from "./LinkCard.module.css";

function formatDuration(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const remaining = total % 60;
  const pad = (value: number) => value.toString().padStart(2, "0");
  return hours > 0
    ? `${hours}:${pad(minutes)}:${pad(remaining)}`
    : `${minutes}:${pad(remaining)}`;
}

function hostLabel(card: PublicLinkCard): string {
  if (card.siteName.trim()) return card.siteName.trim();
  try {
    return new URL(card.canonicalUrl).hostname.replace(/^www\./, "");
  } catch {
    return card.canonicalUrl;
  }
}

/**
 * A captured link preview.
 *
 * Everything on the first paint comes from this instance: the thumbnail is
 * managed media, and the text was snapshotted at publish time. That is what
 * lets the card keep working when the origin is unreachable — or when the video
 * itself is gone years later.
 *
 * A player is loaded only after an explicit click, so simply reading a timeline
 * never contacts a third party, and the frame is only ever offered when the
 * server still considers its origin embeddable.
 */
export function LinkCard({ card }: { card: PublicLinkCard }) {
  const [playing, setPlaying] = useState(false);
  const [playbackFailed, setPlaybackFailed] = useState(false);
  const embeddable = card.embedUrl !== null && card.cardKind === "video";
  const site = hostLabel(card);
  const title = card.title.trim();

  const startPlayback = useCallback(() => {
    setPlaybackFailed(false);
    setPlaying(true);
  }, []);

  const thumbnail = card.thumbnailMediaId ? (
    <img
      alt=""
      className={styles.thumbnail}
      decoding="async"
      height={card.thumbnailHeight ?? undefined}
      loading="lazy"
      src={mediaPreviewUrl(card.thumbnailMediaId)}
      width={card.thumbnailWidth ?? undefined}
    />
  ) : null;

  const meta = (
    <div className={styles.meta}>
      <p className={styles.site}>{site}</p>
      {title ? <p className={styles.title}>{title}</p> : null}
      {card.description.trim() ? (
        <p className={styles.description}>{card.description.trim()}</p>
      ) : null}
    </div>
  );

  if (playing && card.embedUrl) {
    return (
      <section aria-label="链接预览" className={styles.card}>
        <div className={styles.frame}>
          <iframe
            allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
            allowFullScreen
            className={styles.player}
            referrerPolicy="strict-origin-when-cross-origin"
            src={`${card.embedUrl}${card.embedUrl.includes("?") ? "&" : "?"}autoplay=1`}
            title={title || site}
          />
        </div>
        <a
          className={styles.sourceLink}
          href={card.canonicalUrl}
          rel="nofollow noopener noreferrer"
          target="_blank"
        >
          <Icon name="open" />在 {site} 打开
        </a>
      </section>
    );
  }

  return (
    <section aria-label="链接预览" className={styles.card}>
      <a
        className={styles.surface}
        href={card.canonicalUrl}
        rel="nofollow noopener noreferrer"
        target="_blank"
      >
        {thumbnail ? (
          <div className={styles.media}>
            {thumbnail}
            {card.durationSeconds !== null ? (
              <span className={styles.duration}>
                {formatDuration(card.durationSeconds)}
              </span>
            ) : null}
          </div>
        ) : null}
        {meta}
      </a>
      {embeddable ? (
        <button
          aria-label={title ? `播放 ${title}` : "播放"}
          className={styles.play}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            startPlayback();
          }}
          type="button"
        >
          <Icon name="video-play" />
        </button>
      ) : null}
      {playbackFailed ? (
        <p className={styles.playbackError} role="status">
          无法在此播放，请在 {site} 打开。
        </p>
      ) : null}
    </section>
  );
}
