import { useTranslation } from "react-i18next";
import { type PublicLinkCard } from "../../api/public";
import { type LinkCardInput } from "../../api/client";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import styles from "../../components/Forms.module.css";

export function ComposerLinkCard({
  card,
  disabled,
  onRemove,
}: {
  card: LinkCardInput | PublicLinkCard;
  disabled: boolean;
  onRemove: () => void;
}) {
  const { t } = useTranslation();

  const saved = "canonicalUrl" in card;
  const site =
    (saved ? card.siteName : card.site_name).trim() ||
    (saved ? card.canonicalUrl : card.canonical_url);
  const thumbnail = saved ? card.thumbnailMediaId : card.thumbnail_media_id;
  const width = saved ? card.thumbnailWidth : card.thumbnail_width;
  const height = saved ? card.thumbnailHeight : card.thumbnail_height;
  return (
    <section aria-label={t("链接预览")} className={styles.composerLinkCard}>
      {thumbnail ? (
        <img
          alt=""
          className={styles.composerLinkThumbnail}
          height={height ?? undefined}
          src={mediaPreviewUrl(thumbnail)}
          width={width ?? undefined}
        />
      ) : null}
      <div className={styles.composerLinkText}>
        <p className={styles.composerLinkSite}>{site}</p>
        {card.title.trim() ? (
          <p className={styles.composerLinkTitle}>{card.title.trim()}</p>
        ) : null}
      </div>
      <button
        aria-label={t("移除链接卡片")}
        className={styles.composerLinkRemove}
        disabled={disabled}
        onClick={onRemove}
        type="button"
      >
        <Icon name="close" />
      </button>
    </section>
  );
}
