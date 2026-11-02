import { useTranslation } from "react-i18next";
import {
  type PublicLinkCard,
  type LinkCardDisplayMode,
} from "../../api/public";
import { Menu } from "@base-ui/react/menu";
import audienceStyles from "./Audience.module.css";
import { type LinkCardInput } from "../../api/client";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import styles from "../../components/Forms.module.css";

export function ComposerLinkCard({
  card,
  disabled,
  onRemove,
  displayMode = "cover",
  onDisplayModeChange,
}: {
  card: LinkCardInput | PublicLinkCard;
  disabled: boolean;
  onRemove: () => void;
  displayMode?: LinkCardDisplayMode;
  onDisplayModeChange: (mode: LinkCardDisplayMode) => void;
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
      <Menu.Root>
        <Menu.Trigger
          aria-label={t("链接卡片展示方式")}
          className={styles.composerLinkRemove}
          disabled={disabled}
        >
          <Icon name="ellipsis" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="end"
            side="bottom"
            sideOffset={4}
            className={audienceStyles.positioner}
          >
            <Menu.Popup
              aria-label={t("链接卡片展示方式")}
              className={styles.actionsMenuPopup}
            >
              <Menu.RadioGroup
                value={displayMode}
                onValueChange={(value) => {
                  if (value === "cover" || value === "compact")
                    onDisplayModeChange(value);
                }}
              >
                {(["compact", "cover"] as const).map((mode) => (
                  <Menu.RadioItem
                    key={mode}
                    value={mode}
                    closeOnClick
                    className={audienceStyles.option}
                  >
                    <span className={audienceStyles.optionLabel}>
                      {mode === "compact" ? t("小图卡片") : t("大图封面")}
                    </span>
                    <Menu.RadioItemIndicator>
                      <Icon name="check" />
                    </Menu.RadioItemIndicator>
                  </Menu.RadioItem>
                ))}
              </Menu.RadioGroup>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
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
