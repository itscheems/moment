import { Menu } from "@base-ui/react/menu";
import { useState } from "react";

import type { PostVisibility } from "../../api/public";
import { Icon, type IconName } from "../../components/icons";
import styles from "./Audience.module.css";

/**
 * Localized audience options, in the order the card lists them.
 */
export const AUDIENCE_OPTIONS: readonly {
  value: PostVisibility;
  label: string;
  icon: IconName;
}[] = [
  { value: "everyone", label: "所有人", icon: "audience-everyone" },
  { value: "only_me", label: "仅自己", icon: "audience-only-me" },
];

export function audienceLabel(value: PostVisibility): string {
  return (
    AUDIENCE_OPTIONS.find((option) => option.value === value)?.label ??
    AUDIENCE_OPTIONS[0].label
  );
}

/**
 * A bordered pill names the current audience and opens a selection card
 * centred beneath it. Base UI handles keyboard navigation, focus return,
 * and dismissal.
 *
 * Radio items announce the selected option as checked and show a check mark.
 */
export function AudienceSelector({
  value,
  onChange,
  onOpenChange,
  onOpenChangeComplete,
  disabled = false,
}: {
  value: PostVisibility;
  onChange: (next: PostVisibility) => void;
  onOpenChange?: (open: boolean) => void;
  onOpenChangeComplete?: (open: boolean) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const change = (next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
  };
  return (
    <Menu.Root
      onOpenChangeComplete={onOpenChangeComplete}
      onOpenChange={(next, details) => {
        // Open the card on click, not on press, so the pill keeps
        // its pressed fill for as long as the button is held. Base UI opens
        // on mousedown; that request is declined and the click handler on
        // the trigger opens instead. Every other reason -- an item chosen,
        // Escape, a press outside, focus leaving -- is Base UI's to decide.
        if (details.reason === "trigger-press") {
          details.cancel();
          return;
        }
        change(next);
      }}
      open={open}
    >
      <Menu.Trigger
        aria-label="选择受众"
        className={styles.trigger}
        data-audience={value}
        disabled={disabled}
        onClick={() => change(!open)}
      >
        <span className={styles.triggerLabel}>{audienceLabel(value)}</span>
        <Icon className={styles.triggerChevron} name="chevron-down" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="center"
          className={styles.positioner}
          side="bottom"
          sideOffset={10}
        >
          <Menu.Popup className={styles.card}>
            <Menu.Group>
              <Menu.GroupLabel className={styles.cardTitle}>
                选择受众
              </Menu.GroupLabel>
              <Menu.RadioGroup
                onValueChange={(next) => {
                  if (next === "everyone" || next === "only_me") {
                    onChange(next);
                  }
                }}
                value={value}
              >
                {AUDIENCE_OPTIONS.map((option) => (
                  <Menu.RadioItem
                    className={styles.option}
                    closeOnClick
                    key={option.value}
                    value={option.value}
                  >
                    <span aria-hidden="true" className={styles.optionIcon}>
                      <Icon name={option.icon} />
                    </span>
                    <span className={styles.optionLabel}>{option.label}</span>
                    <span className={styles.optionCheck}>
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
  );
}
