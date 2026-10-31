import { cloneElement, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ActionTooltip } from "./TimelineInteractions";
import { Icon } from "./icons";
import styles from "./BackControl.module.css";

type BackElement = ReactElement<{
  className?: string;
  children?: ReactNode;
  "data-back-control"?: string;
}>;

/** Keeps each surface's navigation, ref and accessible name on its own element. */
export function BackControl({
  children,
  inset = false,
  container,
}: {
  children: BackElement;
  inset?: boolean;
  container?: HTMLElement | null;
}) {
  const { t } = useTranslation();
  return (
    <ActionTooltip label={t("Back")} container={container}>
      {cloneElement(children, {
        className: [
          styles.control,
          inset ? styles.inset : "",
          children.props.className,
        ]
          .filter(Boolean)
          .join(" "),
        "data-back-control": "",
        children: <Icon name="arrow-left" />,
      })}
    </ActionTooltip>
  );
}
