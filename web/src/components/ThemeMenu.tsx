import { Menu } from "@base-ui/react/menu";

import { useTheme, type ThemePreference } from "../theme/ThemeProvider";
import { Icon } from "./icons";
import styles from "./ThemeMenu.module.css";

const options: Array<{ value: ThemePreference; label: string }> = [
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
];

export function ThemeMenu() {
  const { preference, setPreference } = useTheme();

  return (
    <Menu.Root>
      <Menu.Trigger aria-label="选择主题" className={styles.trigger}>
        <Icon name="theme" />
        <span className={styles.triggerLabel}>主题</span>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="start"
          className={styles.positioner}
          side="top"
          sideOffset={8}
        >
          <Menu.Popup aria-label="主题" className={styles.popup}>
            <Menu.Group>
              <Menu.GroupLabel className={styles.label}>
                显示主题
              </Menu.GroupLabel>
              <Menu.RadioGroup
                onValueChange={(value) =>
                  setPreference(value as ThemePreference)
                }
                value={preference}
              >
                {options.map((option) => (
                  <Menu.RadioItem
                    className={styles.item}
                    closeOnClick
                    key={option.value}
                    value={option.value}
                  >
                    <Menu.RadioItemIndicator className={styles.indicator}>
                      ●
                    </Menu.RadioItemIndicator>
                    {option.label}
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
