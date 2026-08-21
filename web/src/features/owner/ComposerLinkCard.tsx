import type { OwnerLinkCard } from "../../api/owner";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import styles from "./Owner.module.css";

/**
 * The composer's preview of a resolved card.
 *
 * It shows the snapshot that will actually be stored, not a live rendering of
 * the page, so what the owner approves here is what readers keep. Removing it
 * only detaches the card: the URL stays in the body, where it remains a link.
 */
export function ComposerLinkCard({
  card,
  disabled,
  onRemove,
}: {
  card: OwnerLinkCard;
  disabled: boolean;
  onRemove: () => void;
}) {
  const site = card.site_name.trim() || card.canonical_url;
  return (
    <section aria-label="链接预览" className={styles.composerLinkCard}>
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
        aria-label="移除链接卡片"
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
