import { useTranslation } from "react-i18next";
import { useState } from "react";
import { Popover } from "@base-ui/react/popover";
import { Radio } from "@base-ui/react/radio";
import { RadioGroup } from "@base-ui/react/radio-group";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { VideoControlIcon } from "./VideoControlIcon";
import styles from "./VideoPlayer.module.css";

export type VideoQuality = "preview" | "original";
export function VideoSettings({
  rate,
  quality,
  resolution,
  hasOriginal,
  container,
  onRateChange,
  onQualityChange,
  onOpenChange,
}: {
  rate: number;
  quality: VideoQuality;
  resolution: number;
  hasOriginal: boolean;
  container?: HTMLElement | null;
  onRateChange: (value: number) => void;
  onQualityChange: (value: VideoQuality) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();

  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<"root" | "speed" | "quality">("root");
  const changeOpen = (value: boolean) => {
    setOpen(value);
    onOpenChange(value);
    if (value) setPage("root");
  };
  const auto =
    resolution > 0
      ? t("自动 ({{value1}}p)", { value1: resolution })
      : t("自动");
  return (
    <Popover.Root open={open} onOpenChange={changeOpen}>
      <ActionTooltip
        label={t("视频设置")}
        placement={{ side: "top" }}
        container={container}
      >
        <Popover.Trigger
          aria-label={t("视频设置")}
          data-video-action="settings"
          className={styles.control}
        >
          <VideoControlIcon name="settings" />
        </Popover.Trigger>
      </ActionTooltip>
      <Popover.Portal container={container}>
        <Popover.Positioner
          side="bottom"
          align="center"
          sideOffset={page === "root" ? 10 : 0}
          collisionPadding={8}
          className={styles.positioner}
        >
          <Popover.Popup
            className={styles.settings}
            aria-label={t("视频设置")}
            data-video-settings=""
            data-settings-page={page}
            onClick={(event) => event.stopPropagation()}
            onPointerDown={(event) => event.stopPropagation()}
            onPointerUp={(event) => event.stopPropagation()}
          >
            {page === "root" ? (
              <>
                <button
                  className={styles.settingRow}
                  type="button"
                  role="menuitem"
                  onClick={() => setPage("speed")}
                >
                  <VideoControlIcon name="speed" />
                  <span>
                    <strong>{t("播放速度")}</strong>
                    <span className={styles.settingValue}> · {rate}x</span>
                  </span>
                </button>
                <button
                  className={styles.settingRow}
                  type="button"
                  role="menuitem"
                  onClick={() => setPage("quality")}
                >
                  <VideoControlIcon name="quality" />
                  <span>
                    <strong>{t("视频质量")}</strong>
                    <span className={styles.settingValue}>
                      {" "}
                      · {quality === "original" ? t("原画质") : auto}
                    </span>
                  </span>
                </button>
              </>
            ) : (
              <>
                <button
                  className={styles.settingsTitle}
                  type="button"
                  role="menuitem"
                  onClick={() => setPage("root")}
                >
                  <VideoControlIcon name="back" />
                  <strong>
                    {page === "speed" ? t("播放速度") : t("视频质量")}
                  </strong>
                </button>
                <RadioGroup
                  className={styles.options}
                  aria-label={page === "speed" ? t("播放速度") : t("视频质量")}
                  value={page === "speed" ? String(rate) : quality}
                  onValueChange={(value) => {
                    if (page === "speed") onRateChange(Number(value));
                    else onQualityChange(value as VideoQuality);
                    changeOpen(false);
                  }}
                >
                  {(page === "speed"
                    ? [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2].map((v) => ({
                        value: String(v),
                        label: t("{{value1}}x", { value1: v }),
                      }))
                    : [
                        { value: "preview", label: auto },
                        ...(hasOriginal
                          ? [{ value: "original", label: t("原画质") }]
                          : []),
                      ]
                  ).map((option) => (
                    <label className={styles.option} key={option.value}>
                      <span>{option.label}</span>
                      <span className={styles.radioTarget}>
                        <Radio.Root
                          value={option.value}
                          className={styles.radio}
                        >
                          <Radio.Indicator className={styles.radioCheck}>
                            <svg viewBox="0 0 24 24" aria-hidden="true">
                              <path
                                d="M9.64 18.952l-5.55-4.861 1.317-1.504 3.951 3.459 8.459-10.948L19.4 6.32 9.64 18.952z"
                                fill="currentColor"
                              />
                            </svg>
                          </Radio.Indicator>
                        </Radio.Root>
                      </span>
                    </label>
                  ))}
                </RadioGroup>
              </>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
