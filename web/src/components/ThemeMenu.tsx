import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { useState } from "react";

import type { SiteIdentity } from "../api/public";
import {
  useTheme,
  type AccentPreference,
  type ThemePreference,
} from "../theme/ThemeProvider";
import { Icon } from "./icons";
import { SiteAvatar, siteHandle } from "./SiteIdentity";
import styles from "./ThemeMenu.module.css";

const backgrounds: Array<{
  value: ThemePreference;
  label: string;
}> = [
  { value: "system", label: "跟随系统" },
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
];

export function ThemeMenu({ site }: { site: SiteIdentity }) {
  const { accent, preference, setAccent, setPreference } = useTheme();
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const title = site.title.trim() || "Moment";
  const previewHandle = siteHandle(site);

  return (
    <>
      <Menu.Root>
        <Menu.Trigger
          aria-label="更多"
          className={styles.trigger}
          data-compact-label="更多"
        >
          <Icon name="more" />
          <span className={styles.triggerLabel}>更多</span>
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="start"
            className={styles.positioner}
            side="top"
            sideOffset={8}
          >
            <Menu.Popup aria-label="更多选项" className={styles.popup}>
              <Menu.Item
                className={styles.menuItem}
                onClick={() => setAppearanceOpen(true)}
              >
                <Icon name="theme" />
                <span>
                  <strong>显示</strong>
                  <small>背景与颜色</small>
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

              <div aria-label="显示效果预览" className={styles.previewCard}>
                <SiteAvatar site={site} />
                <div className={styles.previewContent}>
                  <p className={styles.previewMeta}>
                    <strong>{title}</strong>
                    <span>{previewHandle} · 刚刚</span>
                  </p>
                  <p>
                    Moment 会用当前显示设置呈现文字、链接和提及，比如{" "}
                    <span className={styles.previewMention}>
                      {previewHandle}
                    </span>
                    。
                  </p>
                </div>
              </div>

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
