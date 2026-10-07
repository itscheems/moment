import { useState } from "react";
import { Radio } from "@base-ui/react/radio";
import { RadioGroup } from "@base-ui/react/radio-group";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@notion/components/primitives/popover";
import {
  Card,
  CardBody,
  CardHeader,
} from "@notion/components/primitives/card";
import {
  Button,
  ButtonGroup,
} from "@notion/components/primitives/button";
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
  const { preference, setPreference, reset } = useReadingLayout();
  const [open, setOpen] = useState(false);
  const mobile = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          aria-label="阅读排版"
          tooltip="阅读排版"
          data-active-state={open ? "on" : "off"}
        >
          <span className="tiptap-button-text">Aa</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        collisionPadding={16}
        align="end"
        fade
        className={styles.excerptPopup}
        aria-label="阅读排版设置"
      >
        <Card className={styles.layoutCard}>
          <CardHeader className={styles.layoutHeader}>
            <strong>阅读排版</strong>
            <Button
              variant="ghost"
              aria-label="关闭阅读排版"
              onClick={() => setOpen(false)}
            >
              <Icon name="close" className="tiptap-button-icon" />
            </Button>
          </CardHeader>
          <CardBody className={styles.layoutBody}>
            <div className={styles.layoutField}>
              <span>正文宽度</span>
              <Choices<ReadingLayoutPreference["width"]>
                label="正文宽度"
                value={preference.width}
                disabled={mobile}
                onChange={(width) => setPreference({ width })}
                options={[
                  { value: "standard", label: "标准" },
                  { value: "wide", label: "宽" },
                  { value: "wider", label: "更宽" },
                ]}
              />
              {mobile && <small>自动适配屏幕</small>}
            </div>
            <div className={styles.layoutField}>
              <span>字号</span>
              <ButtonGroup
                orientation="horizontal"
                className={styles.layoutChoices}
                aria-label="字号"
              >
                <Button
                  variant="ghost"
                  className={styles.layoutChoice}
                  aria-label="减小字号"
                  disabled={preference.fontSize === 14}
                  onClick={() =>
                    setPreference({ fontSize: preference.fontSize - 2 })
                  }
                >
                  A−
                </Button>
                <output aria-label="当前字号" className={styles.layoutValue}>
                  {preference.fontSize}px
                </output>
                <Button
                  variant="ghost"
                  className={styles.layoutChoice}
                  aria-label="增大字号"
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
              <span>行距</span>
              <Choices<ReadingLayoutPreference["lineHeight"]>
                label="行距"
                value={preference.lineHeight}
                onChange={(lineHeight) => setPreference({ lineHeight })}
                options={[
                  { value: "compact", label: "紧凑" },
                  { value: "normal", label: "标准" },
                  { value: "relaxed", label: "宽松" },
                ]}
              />
            </div>
            <Button
              variant="ghost"
              onClick={reset}
              className={styles.layoutReset}
            >
              <span className="tiptap-button-text">恢复默认</span>
            </Button>
          </CardBody>
        </Card>
      </PopoverContent>
    </Popover>
  );
}
