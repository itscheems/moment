import { type ReactNode, useCallback, useState } from "react";

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

/**
 * Add autoplay to a stored embed URL.
 *
 * The parameter is set rather than appended so a card stored before playback
 * flags moved out of the snapshot does not end up with the key twice.
 */
function playbackUrl(embedUrl: string): string {
  try {
    const parsed = new URL(embedUrl);
    parsed.searchParams.set("autoplay", "1");
    return parsed.toString();
  } catch {
    return embedUrl;
  }
}

/**
 * Format an engagement count the way the platforms themselves do.
 *
 * Above ten thousand the exact figure stops carrying information a reader
 * wants, so it collapses to 万 — the same unit the source sites display, which
 * keeps the number recognisable next to the card it came from.
 */
function formatCount(value: number): string {
  if (value < 10_000) return value.toLocaleString("zh-CN");
  const scaled = value / 10_000;
  const digits = scaled < 100 ? 1 : 0;
  return `${scaled.toFixed(digits).replace(/\.0$/, "")}万`;
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

  // Stats render from structured numbers, never by re-parsing platform prose,
  // so this row stays identical no matter which site a card came from.
  const stats: Array<{
    key: string;
    icon: ReactNode;
    label: string;
    value: number;
  }> = [];
  if (card.viewCount !== null) {
    stats.push({
      key: "views",
      icon: <Icon name="play-count" />,
      label: "播放",
      value: card.viewCount,
    });
  }
  if (card.commentCount !== null) {
    stats.push({
      key: "comments",
      icon: <Icon name="danmaku-count" />,
      label: "弹幕",
      value: card.commentCount,
    });
  }
  if (card.likeCount !== null) {
    stats.push({
      key: "likes",
      icon: null,
      label: "点赞",
      value: card.likeCount,
    });
  }

  const meta = (
    <div className={styles.meta}>
      <p className={styles.site}>{site}</p>
      {title ? <p className={styles.title}>{title}</p> : null}
      {stats.length > 0 ? (
        <ul className={styles.stats}>
          {stats.map((stat) => (
            <li className={styles.stat} key={stat.key}>
              {/* A glyph carries the meaning where one exists; otherwise the
                  name is spelled out, so no figure is ever left unlabelled. */}
              {stat.icon ?? (
                <span aria-hidden="true" className={styles.statName}>
                  {stat.label}
                </span>
              )}
              <span aria-hidden="true">{formatCount(stat.value)}</span>
              <span className={styles.statLabel}>
                {stat.label} {stat.value.toLocaleString("zh-CN")}
              </span>
            </li>
          ))}
        </ul>
      ) : card.description.trim() ? (
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
            src={playbackUrl(card.embedUrl)}
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
