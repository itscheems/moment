import { Menu } from "@base-ui/react/menu";
import { useId } from "react";

import { Icon } from "../../components/icons";
import styles from "./ReplyAudience.module.css";

export function ReplyAudience({
  disabled,
  onOpenChange,
  onOpenChangeComplete,
}: {
  disabled: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenChangeComplete: (open: boolean) => void;
}) {
  const titleId = useId();
  return (
    <Menu.Root
      onOpenChange={onOpenChange}
      onOpenChangeComplete={onOpenChangeComplete}
    >
      <Menu.Trigger className={styles.trigger} disabled={disabled}>
        <Icon name="reply-everyone" />
        <span>Everyone can reply</span>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="center"
          className={styles.positioner}
          side="bottom"
          sideOffset={22}
        >
          <Menu.Popup aria-labelledby={titleId} className={styles.card}>
            <Menu.Group>
              <Menu.GroupLabel className={styles.title} id={titleId}>
                Who can reply?
              </Menu.GroupLabel>
              <Menu.RadioGroup value="everyone">
                <Menu.RadioItem
                  className={styles.option}
                  closeOnClick
                  value="everyone"
                >
                  <Icon name="audience-everyone" />
                  <span>Everyone</span>
                  <Menu.RadioItemIndicator className={styles.check}>
                    <Icon name="audience-check" />
                  </Menu.RadioItemIndicator>
                </Menu.RadioItem>
              </Menu.RadioGroup>
            </Menu.Group>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
