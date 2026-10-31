import { useTranslation } from "react-i18next";
import { useNotifications } from "./NotificationProvider";
import styles from "./Notifications.module.css";
export function notificationBadgeText(count: number): string {
  return count < 1 ? "" : count < 20 ? String(count) : "20+";
}
export function NotificationBadge() {
  const { t } = useTranslation();
  const count = useNotifications().progress?.newCount ?? 0;
  const label = notificationBadgeText(count);
  return label ? (
    <span
      className={styles.badge}
      data-size={count >= 20 ? "capped" : count >= 10 ? "double" : "single"}
      aria-label={t("{{count}} 条新通知", { count })}
    >
      {label}
    </span>
  ) : null;
}
