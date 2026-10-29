import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { useRef } from "react";
import { Menu } from "@base-ui/react/menu";
import type { CommentSort } from "../../api/public";
import { Icon } from "../../components/icons";
import styles from "./ReplySort.module.css";

const options = [
  {
    value: "oldest",
    get label() {
      return t("最早");
    },
  },
  {
    value: "newest",
    get label() {
      return t("最新");
    },
  },
] as const;

export function ReplySort({
  value,
  busy,
  onChange,
}: {
  value: CommentSort;
  busy: boolean;
  onChange: (sort: CommentSort) => void;
}) {
  const { t } = useTranslation();

  const firstItem = useRef<HTMLDivElement>(null);
  return (
    <div className={styles.bar} aria-busy={busy} data-reply-sort>
      <Menu.Root
        onOpenChangeComplete={(open) => {
          if (open) firstItem.current?.focus({ preventScroll: true });
        }}
      >
        <Menu.Trigger
          className={styles.trigger}
          aria-label={t("回复排序：{{value1}}", {
            value1: options.find((option) => option.value === value)!.label,
          })}
        >
          {options.find((option) => option.value === value)!.label}
          <Icon name="chevron-down" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            className={styles.positioner}
            side="bottom"
            align="end"
            sideOffset={-16}
            collisionPadding={8}
          >
            <Menu.Popup className={styles.popup}>
              <Menu.Group>
                <Menu.GroupLabel className={styles.title}>
                  {t("回复排序")}
                </Menu.GroupLabel>
                <Menu.RadioGroup
                  value={value}
                  onValueChange={(next) => {
                    if (next === "oldest" || next === "newest") onChange(next);
                  }}
                >
                  {options.map((option) => (
                    <Menu.RadioItem
                      ref={option.value === "oldest" ? firstItem : undefined}
                      key={option.value}
                      value={option.value}
                      className={styles.option}
                      closeOnClick
                    >
                      <span>{option.label}</span>
                      <span className={styles.check}>
                        <Menu.RadioItemIndicator>
                          <Icon name="audience-check" />
                        </Menu.RadioItemIndicator>
                      </span>
                    </Menu.RadioItem>
                  ))}
                </Menu.RadioGroup>
              </Menu.Group>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </div>
  );
}
