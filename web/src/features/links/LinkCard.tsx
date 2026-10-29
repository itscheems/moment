import { getLocale } from "../../i18n";
import { useTranslation } from "react-i18next";
import { type ReactNode, useCallback, useState } from "react";

import type { PublicLinkCard } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import { SiteMark } from "./SiteMark";
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
 *
 * Below that the digits are written plainly, without a thousands separator.
 * Grouping only ever applies to part of the range, so a four-digit figure
 * would be the single value in the row wearing a comma, sitting between a bare
 * 370 and a bare 28.9万. The precise, grouped number stays in the accessible
 * label, where reading it out is the whole point.
 */
function formatCount(value: number): string {
  return new Intl.NumberFormat(getLocale(), {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
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
  const { t } = useTranslation();

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

  // Stats render from structured fields, never by re-parsing platform prose,
  // so this row stays identical no matter which site a card came from.
  const stats: Array<{
    key: string;
    icon: ReactNode;
    label: string;
    display: string;
    exact: string;
  }> = [];
  if (card.authorName.trim()) {
    stats.push({
      key: "author",
      icon: <Icon name="uploader" />,
      label: t("作者"),
      display: card.authorName.trim(),
      exact: card.authorName.trim(),
    });
  }
  for (const entry of [
    {
      key: "views",
      icon: "play-count",
      label: t("播放"),
      value: card.viewCount,
    },
    {
      key: "comments",
      icon: "danmaku-count",
      label: t("弹幕"),
      value: card.commentCount,
    },
    {
      key: "likes",
      icon: "like-count",
      label: t("点赞"),
      value: card.likeCount,
    },
  ] as const) {
    if (entry.value === null) continue;
    stats.push({
      key: entry.key,
      icon: <Icon name={entry.icon} />,
      label: entry.label,
      display: formatCount(entry.value),
      exact: entry.value.toLocaleString(getLocale()),
    });
  }

  const meta = (
    <div className={styles.meta}>
      <p className={styles.site}>
        <SiteMark
          canonicalUrl={card.canonicalUrl}
          faviconMediaId={card.faviconMediaId}
        />
        <span>{site}</span>
      </p>
      {title ? <p className={styles.title}>{title}</p> : null}
      {/* The description and the figures are independent: a platform that
          publishes statistics puts them here instead of prose, while an
          ordinary page keeps its summary and may still name an author. */}
      {card.description.trim() ? (
        <p className={styles.description}>{card.description.trim()}</p>
      ) : null}
      {stats.length > 0 ? (
        <ul className={styles.stats}>
          {stats.map((stat) => (
            <li className={styles.stat} key={stat.key}>
              {/* The glyph carries the meaning visually; the name and the exact
                  figure stay available to assistive technology. */}
              {stat.icon}
              <span aria-hidden="true">{stat.display}</span>
              <span className={styles.statLabel}>
                {stat.label} {stat.exact}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );

  if (playing && card.embedUrl) {
    return (
      <section aria-label={t("链接预览")} className={styles.card}>
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
          <Icon name="open" />
          {t("在")} {site} {t("打开")}
        </a>
      </section>
    );
  }

  return (
    <section aria-label={t("链接预览")} className={styles.card}>
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
          aria-label={
            title ? t("播放 {{value1}}", { value1: title }) : t("播放")
          }
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
          {t("无法在此播放，请在")} {site} {t("打开。")}
        </p>
      ) : null}
    </section>
  );
}
