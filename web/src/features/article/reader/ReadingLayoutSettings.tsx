import { useTranslation } from "react-i18next";
import { useState } from "react";
import { Radio } from "@base-ui/react/radio";
import { RadioGroup } from "@base-ui/react/radio-group";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@notion/components/primitives/popover";
import { Card, CardBody, CardHeader } from "@notion/components/primitives/card";
import { Button, ButtonGroup } from "@notion/components/primitives/button";
import { Icon } from "../../../components/icons";
import {
  MOBILE_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../../components/useMediaQuery";
import {
  useReadingLayout,
  type ReadingLayoutPreference,
} from "../ReadingLayout";
import styles from "../Article.module.css";

function Choices<T extends string>({
  label,
  value,
  onChange,
  options,
  disabled = false,
}: {
  label: string;
  value: T;
  onChange: (value: T) => void;
  options: { value: T; label: string }[];
  disabled?: boolean;
}) {
  return (
    <RadioGroup
      aria-label={label}
      value={value}
      disabled={disabled}
      onValueChange={onChange}
      render={
        <ButtonGroup
          orientation="horizontal"
          className={styles.layoutChoices}
        />
      }
    >
      {options.map((option) => (
        <Radio.Root
          key={option.value}
          value={option.value}
          nativeButton
          render={
            <Button
              variant="ghost"
              className={styles.layoutChoice}
              data-active-state={value === option.value ? "on" : "off"}
            />
          }
        >
          <span className="tiptap-button-text">{option.label}</span>
        </Radio.Root>
      ))}
    </RadioGroup>
  );
}

export function ReadingLayoutSettings() {
  const { t } = useTranslation();

  const { preference, setPreference, reset } = useReadingLayout();
  const [open, setOpen] = useState(false);
  const mobile = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          aria-label={t("阅读排版")}
          data-article-header-action
          tooltip={t("阅读排版")}
          data-active-state={open ? "on" : "off"}
        >
          <span className="tiptap-button-text">{t("Aa")}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        collisionPadding={16}
        align="end"
        fade
        className={styles.excerptPopup}
        aria-label={t("阅读排版设置")}
      >
        <Card className={styles.layoutCard}>
          <CardHeader className={styles.layoutHeader}>
            <strong>{t("阅读排版")}</strong>
            <Button
              variant="ghost"
              aria-label={t("关闭阅读排版")}
              onClick={() => setOpen(false)}
            >
              <Icon name="close" className="tiptap-button-icon" />
            </Button>
          </CardHeader>
          <CardBody className={styles.layoutBody}>
            <div className={styles.layoutField}>
              <span>{t("正文宽度")}</span>
              <Choices<ReadingLayoutPreference["width"]>
                label={t("正文宽度")}
                value={preference.width}
                disabled={mobile}
                onChange={(width) => setPreference({ width })}
                options={[
                  { value: "standard", label: t("标准") },
                  { value: "wide", label: t("宽") },
                  { value: "wider", label: t("更宽") },
                ]}
              />
              {mobile && <small>{t("自动适配屏幕")}</small>}
            </div>
            <div className={styles.layoutField}>
              <span>{t("字号")}</span>
              <ButtonGroup
                orientation="horizontal"
                className={styles.layoutChoices}
                aria-label={t("字号")}
              >
                <Button
                  variant="ghost"
                  className={styles.layoutChoice}
                  aria-label={t("减小字号")}
                  disabled={preference.fontSize === 14}
                  onClick={() =>
                    setPreference({ fontSize: preference.fontSize - 2 })
                  }
                >
                  A−
                </Button>
                <output
                  aria-label={t("当前字号")}
                  className={styles.layoutValue}
                >
                  {preference.fontSize}
                  {t("px")}
                </output>
                <Button
                  variant="ghost"
                  className={styles.layoutChoice}
                  aria-label={t("增大字号")}
                  disabled={preference.fontSize === 24}
                  onClick={() =>
                    setPreference({ fontSize: preference.fontSize + 2 })
                  }
                >
                  A+
                </Button>
              </ButtonGroup>
            </div>
            <div className={styles.layoutField}>
              <span>{t("行距")}</span>
              <Choices<ReadingLayoutPreference["lineHeight"]>
                label={t("行距")}
                value={preference.lineHeight}
                onChange={(lineHeight) => setPreference({ lineHeight })}
                options={[
                  { value: "compact", label: t("紧凑") },
                  { value: "normal", label: t("标准") },
                  { value: "relaxed", label: t("宽松") },
                ]}
              />
            </div>
            <Button
              variant="ghost"
              onClick={reset}
              className={styles.layoutReset}
            >
              <span className="tiptap-button-text">{t("恢复默认")}</span>
            </Button>
          </CardBody>
        </Card>
      </PopoverContent>
    </Popover>
  );
}
