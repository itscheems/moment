import { useTranslation } from "react-i18next";
import { type LinkCardInput } from "../../api/client";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import styles from "../../components/Forms.module.css";

export function ComposerLinkCard({
  card,
  disabled,
  onRemove,
}: {
  card: LinkCardInput;
  disabled: boolean;
  onRemove: () => void;
}) {
  const { t } = useTranslation();

  const site = card.site_name.trim() || card.canonical_url;
  return (
    <section aria-label={t("链接预览")} className={styles.composerLinkCard}>
      {card.thumbnail_media_id ? (
        <img
          alt=""
          className={styles.composerLinkThumbnail}
          height={card.thumbnail_height ?? undefined}
          src={mediaPreviewUrl(card.thumbnail_media_id)}
          width={card.thumbnail_width ?? undefined}
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
