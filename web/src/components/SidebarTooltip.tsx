import { Tooltip } from "@base-ui/react/tooltip";
import type { ReactElement } from "react";

import { useMediaQuery } from "./useMediaQuery";
import styles from "./AppShell.module.css";

export function SidebarTooltip({
  children,
  label,
}: {
  children: ReactElement;
  label: string;
}) {
  const compact = useMediaQuery("(min-width: 720px) and (max-width: 1264px)");
  if (!compact) return children;
  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner
          align="center"
          className={styles.sidebarTooltipPositioner}
          side="bottom"
          sideOffset={1}
        >
          <Tooltip.Popup className={styles.sidebarTooltipPopup}>
            {label}
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
