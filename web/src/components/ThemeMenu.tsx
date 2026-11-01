import { LanguageDialog } from "./LanguageDialog";
import { t } from "../i18n";
import { useTranslation } from "react-i18next";
import { AccountName } from "./AccountName";
import { Dialog } from "./Dialog";
import { Menu } from "@base-ui/react/menu";
import { useId, useState } from "react";

import type { PublicAccount, SiteIdentity } from "../api/public";
import { MentionProvider } from "../app/mentions";
import {
  useTheme,
  type AccentPreference,
  type ThemePreference,
} from "../theme/ThemeProvider";
import { Icon } from "./icons";
import { SiteAvatar, siteHandle } from "./SiteIdentity";
import { MarkdownBody } from "./content/MarkdownBody";
import { SidebarTooltip } from "./SidebarTooltip";
import styles from "./ThemeMenu.module.css";
import postStyles from "../features/posts/PostPresentation.module.css";

const backgrounds: Array<{
  value: ThemePreference;
  label: string;
}> = [
  {
    value: "light",
    get label() {
      return t("浅色");
    },
  },
  {
    value: "dim",
    get label() {
      return t("柔和深色");
    },
  },
  {
    value: "lights-out",
    get label() {
      return t("纯黑");
    },
  },
];

const accents: Array<{ value: AccentPreference; label: string }> = [
  {
    value: "blue",
    get label() {
      return t("蓝色");
    },
  },
  {
    value: "yellow",
    get label() {
      return t("黄色");
    },
  },
  {
    value: "pink",
    get label() {
      return t("粉色");
    },
  },
  {
    value: "purple",
    get label() {
      return t("紫色");
    },
  },
  {
    value: "orange",
    get label() {
      return t("橙色");
    },
  },
  {
    value: "green",
    get label() {
      return t("绿色");
    },
  },
  {
    value: "black",
    get label() {
      return t("纯黑色");
    },
  },
];

export function ThemeMenu({
  account = null,
  navigation = false,
  site,
}: {
  account?: PublicAccount | null;
  navigation?: boolean;
  site: SiteIdentity;
}) {
  const { t } = useTranslation();

  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const [languageOpen, setLanguageOpen] = useState(false);

  const triggerContent = (
    <>
      <Icon name="more" variant={navigation ? "navigation" : "default"} />
      <span className={styles.triggerLabel}>{t("更多")}</span>
    </>
  );

  return (
    <>
      <Menu.Root>
        <SidebarTooltip label={t("更多")}>
          <Menu.Trigger
            aria-label={t("更多")}
            className={styles.trigger}
            data-navigation={navigation || undefined}
            data-compact-label={t("更多")}
          >
            {navigation ? (
              <span className={styles.triggerPill}>{triggerContent}</span>
            ) : (
              triggerContent
            )}
          </Menu.Trigger>
        </SidebarTooltip>
        <Menu.Portal>
          <Menu.Positioner
            align="start"
            className={styles.positioner}
            side="top"
            sideOffset={8}
          >
            <Menu.Popup
              aria-label={t("更多选项")}
              className={styles.popup}
              data-navigation={navigation || undefined}
            >
              <Menu.Item
                className={styles.menuItem}
                onClick={() => setAppearanceOpen(true)}
              >
                <Icon name="theme" />
                <span>
                  <strong>{t("显示")}</strong>
                  {!navigation ? <small>{t("背景与颜色")}</small> : null}
                </span>
              </Menu.Item>
              <Menu.Item
                className={styles.menuItem}
                onClick={() => setLanguageOpen(true)}
              >
                <Icon name="globe" />
                <span>
                  <strong>{t("语言")}</strong>
                </span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      {languageOpen ? (
        <LanguageDialog onClose={() => setLanguageOpen(false)} />
      ) : null}
      <AppearanceDialog
        open={appearanceOpen}
        onOpenChange={setAppearanceOpen}
        account={account}
        site={site}
      />
    </>
  );
}

export function AppearanceDialog({
  open,
  onOpenChange,
  account = null,
  site,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  account?: PublicAccount | null;
  site: SiteIdentity;
}) {
  const { t } = useTranslation();
  return (
    <Dialog.Root onOpenChange={onOpenChange} open={open}>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.appearanceDialog}>
            <div className={styles.dialogHeader}>
              <Dialog.Close aria-label={t("关闭显示设置")}>×</Dialog.Close>
              <Dialog.Title>{t("自定义显示")}</Dialog.Title>
              <span />
            </div>
            <Dialog.Description className={styles.description}>
              {t("这些设置只保存在当前浏览器，会立即应用到所有页面。")}
            </Dialog.Description>

            <AppearanceSettings account={account} site={site} />

            <Dialog.Close className={styles.doneButton}>
              {t("完成")}
            </Dialog.Close>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function AppearanceSettings({
  account = null,
  site,
}: {
  account?: PublicAccount | null;
  site: SiteIdentity;
}) {
  const { t } = useTranslation();
  const colorId = useId();
  const backgroundId = useId();
  const { accent, preference, setAccent, setPreference } = useTheme();
  const identity = account ?? site;
  const previewHandle = siteHandle(identity);
  return (
    <div className={styles.appearanceOptions}>
      <MentionProvider identities={[identity]}>
        <div
          aria-label={t("显示效果预览")}
          className={`${styles.previewCard} ${postStyles.timelineDensity}`}
        >
          <SiteAvatar site={identity} size={40} />
          <div className={postStyles.postMain}>
            <div className={postStyles.byline}>
              <div
                className={`${postStyles.bylineIdentity} ${styles.previewIdentity}`}
              >
                <strong className={postStyles.nameLink}>
                  <AccountName identity={identity} />
                </strong>
                <span className={postStyles.usernameLink}>{previewHandle}</span>
                <span>·</span>
                <span>{t("刚刚")}</span>
              </div>
            </div>
            <MarkdownBody
              className={postStyles.markdown}
              html={t(
                "<p>Moment 会用当前显示设置呈现文字、链接和提及，比如 {{value1}}。</p>",
                { value1: previewHandle },
              )}
            />
          </div>
        </div>
      </MentionProvider>

      <section aria-labelledby={colorId}>
        <h3 id={colorId}>{t("颜色")}</h3>
        <div className={styles.accentGrid}>
          {accents.map((option) => (
            <button
              aria-pressed={accent === option.value}
              className={styles.accentChoice}
              data-accent-choice={option.value}
              key={option.value}
              onClick={() => setAccent(option.value)}
              title={option.label}
              type="button"
            >
              <span>{accent === option.value ? "✓" : ""}</span>
              <span className={styles.srOnly}>{option.label}</span>
            </button>
          ))}
        </div>
      </section>

      <section aria-labelledby={backgroundId}>
        <h3 id={backgroundId}>{t("背景")}</h3>
        <div className={styles.backgroundGrid}>
          {backgrounds.map((option) => (
            <button
              aria-label={option.label}
              aria-pressed={preference === option.value}
              className={styles.backgroundChoice}
              data-background-choice={option.value}
              key={option.value}
              onClick={() => setPreference(option.value)}
              type="button"
            >
              <span className={styles.radioMark}>
                {preference === option.value ? "✓" : ""}
              </span>
              <strong>{option.label}</strong>
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}
