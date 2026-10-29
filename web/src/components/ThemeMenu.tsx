import { AccountName } from "./AccountName";
import { Dialog } from "./Dialog";
import { Menu } from "@base-ui/react/menu";
import { useState } from "react";

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
  { value: "light", label: "浅色" },
  { value: "dim", label: "柔和深色" },
  { value: "lights-out", label: "纯黑" },
];

const accents: Array<{ value: AccentPreference; label: string }> = [
  { value: "blue", label: "蓝色" },
  { value: "yellow", label: "黄色" },
  { value: "pink", label: "粉色" },
  { value: "purple", label: "紫色" },
  { value: "orange", label: "橙色" },
  { value: "green", label: "绿色" },
  { value: "black", label: "纯黑色" },
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
  const { accent, preference, setAccent, setPreference } = useTheme();
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const identity = account ?? site;
  const previewHandle = siteHandle(identity);

  const triggerContent = (
    <>
      <Icon name="more" variant={navigation ? "navigation" : "default"} />
      <span className={styles.triggerLabel}>更多</span>
    </>
  );

  return (
    <>
      <Menu.Root>
        <SidebarTooltip label="更多">
          <Menu.Trigger
            aria-label="更多"
            className={styles.trigger}
            data-navigation={navigation || undefined}
            data-compact-label="更多"
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
              aria-label="更多选项"
              className={styles.popup}
              data-navigation={navigation || undefined}
            >
              <Menu.Item
                className={styles.menuItem}
                onClick={() => setAppearanceOpen(true)}
              >
                <Icon name="theme" />
                <span>
                  <strong>显示</strong>
                  {!navigation ? <small>背景与颜色</small> : null}
                </span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <Dialog.Root onOpenChange={setAppearanceOpen} open={appearanceOpen}>
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.backdrop} />
          <Dialog.Viewport className={styles.viewport}>
            <Dialog.Popup className={styles.appearanceDialog}>
              <div className={styles.dialogHeader}>
                <Dialog.Close aria-label="关闭显示设置">×</Dialog.Close>
                <Dialog.Title>自定义显示</Dialog.Title>
                <span />
              </div>
              <Dialog.Description className={styles.description}>
                这些设置只保存在当前浏览器，会立即应用到所有页面。
              </Dialog.Description>

              <MentionProvider identities={[identity]}>
                <div
                  aria-label="显示效果预览"
                  className={`${styles.previewCard} ${postStyles.timelineDensity}`}
                >
                  <SiteAvatar site={identity} size={40} />
                  <div className={postStyles.postMain}>
                    <div className={postStyles.byline}>
                      <div className={postStyles.bylineIdentity}>
                        <strong className={postStyles.nameLink}>
                          <AccountName identity={identity} />
                        </strong>
                        <span className={postStyles.usernameLink}>
                          {previewHandle}
                        </span>
                        <span>·</span>
                        <span>刚刚</span>
                      </div>
                    </div>
                    <MarkdownBody
                      className={postStyles.markdown}
                      html={`<p>Moment 会用当前显示设置呈现文字、链接和提及，比如 ${previewHandle}。</p>`}
                    />
                  </div>
                </div>
              </MentionProvider>

              <section aria-labelledby="appearance-color-title">
                <h3 id="appearance-color-title">颜色</h3>
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

              <section aria-labelledby="appearance-background-title">
                <h3 id="appearance-background-title">背景</h3>
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

              <Dialog.Close className={styles.doneButton}>完成</Dialog.Close>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
